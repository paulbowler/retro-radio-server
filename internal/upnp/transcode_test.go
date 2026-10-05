// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"strings"
	"testing"
)

func TestFallbackPreferenceAndCancellation(t *testing.T) {
	p, _, _, _ := fixture(t)
	if _, e := p.Browse(context.Background(), "0", 0, 24); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "fake-ffmpeg")
	if e := os.WriteFile(path, []byte("#!/bin/sh\nexec /bin/cat\n"), 0700); e != nil {
		t.Fatal(e)
	}
	p.ffmpegPath = path
	resources := []content.Resource{{URL: "http://127.0.0.1/flac", Protocol: "http-get", Codec: "FLAC", MIME: "audio/flac"}, {URL: "http://127.0.0.1/native", Protocol: "http-get", Codec: "AAC", MIME: "audio/aac"}}
	r, e := p.SelectResource(resources, model.LegacyXML)
	if e != nil || r.Codec != "AAC" {
		t.Fatal("native resource not preferred", r, e)
	}
	r, e = p.SelectResource(resources[:1], model.LegacyXML)
	if e != nil || r.Codec != "FLAC" {
		t.Fatal("fallback missing", r, e)
	}
	p.DisableTranscode = true
	if _, e = p.SelectResource(resources[:1], model.LegacyXML); e == nil {
		t.Fatal("disabled conversion selected")
	}
	p.DisableTranscode = false
	p.ffmpegPath = ""
	if _, e = p.SelectResource(resources[:1], model.LegacyXML); e == nil {
		t.Fatal("missing FFmpeg selected")
	}
	p.ffmpegPath = path
	// This fake process verifies bounded process/pipe lifecycle; real encoding is tested below.
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 8192)))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer source.Close()
	audio, e := p.openConverted(context.Background(), content.Resource{URL: source.URL, Codec: "FLAC"})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.conversionSlots) != 1 {
		t.Fatal("conversion slot missing")
	}
	audio.Close()
	audio.Close()
	if len(p.conversionSlots) != 0 {
		t.Fatal("conversion slot leaked")
	}
	p.ffmpegPath = "/does/not/exist"
	if _, e = p.openConverted(context.Background(), content.Resource{URL: source.URL, Codec: "FLAC"}); e == nil {
		t.Fatal("missing process accepted")
	}
	if len(p.conversionSlots) != 0 {
		t.Fatal("failed startup leaked slot")
	}
}
func TestConvertedRangeAndHEAD(t *testing.T) {
	p, _, _, _ := fixture(t)
	for _, test := range []struct {
		method, rangeHeader string
		code                int
	}{{"HEAD", "", 200}, {"GET", "bytes=100-", 416}, {"GET", "bytes=0-100", 416}} {
		req := httptest.NewRequest(test.method, "/stream/upnp/test", nil)
		req.Header.Set("Range", test.rangeHeader)
		w := httptest.NewRecorder()
		p.serveConverted(w, req, content.Resource{Codec: "FLAC"})
		if w.Code != test.code {
			t.Fatal(w.Code, test)
		}
		if test.method == "HEAD" && (w.Body.Len() != 0 || w.Header().Get("Content-Type") != "audio/mpeg") {
			t.Fatal(w.Body, w.Header())
		}
	}
}
func TestGeneratedFLACPlayback(t *testing.T) {
	ffmpeg, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("FFmpeg required for generated FLAC encoding test")
	}
	input, e := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "1", "-c:a", "flac", "-f", "flac", "pipe:1").Output()
	if e != nil {
		t.Fatal(e)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/description":
			fmt.Fprint(w, `<root><device><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></root>`)
		case "/control":
			fmt.Fprint(w, soap(didlOpen+`<item id="flac" parentID="0"><title>FLAC track</title><class>object.item.audioItem.musicTrack</class><res protocolInfo="http-get:*:audio/flac:*">`+server.URL+`/flac</res></item></DIDL-Lite>`, 1, 1))
		case "/flac":
			w.Header().Set("Content-Type", "audio/flac")
			w.Write(input)
		}
	}))
	defer server.Close()
	p, e := New(server.URL + "/description")
	if e != nil {
		t.Fatal(e)
	}
	p.allowLoopback = true
	page, e := p.Browse(context.Background(), "0", 0, 24)
	if e != nil {
		t.Fatal(e)
	}
	if !page.Items[0].Transcoded || page.Items[0].PlaybackCodec != "MP3" {
		t.Fatal(page)
	}
	play, e := p.Resolve(context.Background(), page.Items[0].PlaybackID, model.Capabilities{HTTP: true, HTTPS: true, MP3: true})
	if e != nil || !play.Transcode || play.Direct || play.Codec() != "MP3" || play.Bitrate() != 128 {
		t.Fatal(play, e)
	}
	req := httptest.NewRequest("GET", "/stream/upnp/"+page.Items[0].PlaybackID, nil)
	req.Header.Set("Range", "bytes=0-")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("Content-Type") != "audio/mpeg" || w.Body.Len() < 4096 || w.Header().Get("Accept-Ranges") != "none" {
		t.Fatal(w.Code, w.Header(), w.Body.Len())
	}
	decode := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "mp3", "-i", "pipe:0", "-f", "s16le", "pipe:1")
	decode.Stdin = bytes.NewReader(w.Body.Bytes())
	pcm, e := decode.Output()
	if e != nil || len(pcm) < 44100 {
		t.Fatal("MP3 did not decode", len(pcm), e)
	}
	if len(p.conversionSlots) != 0 {
		t.Fatal("completed conversion leaked slot")
	}
	p.DisableTranscode = true
	if _, e = p.Resolve(context.Background(), page.Items[0].PlaybackID, model.LegacyXML); e == nil {
		t.Fatal("disabled fallback still resolved")
	}
}
