// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"retroradio.local/server/internal/model"
	"testing"
)

func TestICYMetadataAcrossEveryReadBoundary(t *testing.T) {
	block := func(title string) []byte {
		data := []byte("StreamTitle='" + title + "';")
		n := (len(data) + 15) / 16
		return append([]byte{byte(n)}, append(data, make([]byte, n*16-len(data))...)...)
	}
	payload := append([]byte("1234"), block("Artist — Song")...)
	payload = append(payload, []byte("abcd\x00wxyz")...)
	payload = append(payload, block("Next song")...)
	payload = append(payload, []byte("0000")...)
	payload = append(payload, block("")...)
	original := bytes.Clone(payload)
	for size := 1; size <= len(payload); size++ {
		titles := []string{}
		o := newICYObserver("4", func(s string) { titles = append(titles, s) })
		for start := 0; start < len(payload); start += size {
			o.Write(payload[start:min(start+size, len(payload))])
		}
		if len(titles) != 3 || titles[0] != "Artist — Song" || titles[1] != "Next song" || titles[2] != "" {
			t.Fatalf("chunk %d: %#v", size, titles)
		}
	}
	if !bytes.Equal(payload, original) {
		t.Fatal("audio bytes modified")
	}
	for _, interval := range []string{"", "0", "-1", "999999999"} {
		if newICYObserver(interval, func(string) {}) != nil {
			t.Fatal(interval)
		}
	}
}

func TestPlaybackAttributionSeparatesWebAndRadios(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	p := testRelay(t, upstream)
	d, err := p.Store.Seen("dashboard-kitchen", "pure", "", "192.168.1.50")
	if err != nil {
		t.Fatal(err)
	}
	s := model.Station{ID: "1001", Name: "Smooth", Codec: "MP3", Bitrate: 128}
	for _, test := range []struct{ path, ua, remote, want string }{
		{"/stream/x", "Radio", "192.168.1.50:1234", d.ID},
		{"/stream/x?radio=" + d.ID, "Radio", "172.16.0.1:4444", d.ID},
		{"/stream/x?device=" + d.ID, "Mozilla/5.0", "192.168.1.50:4444", ""},
		{"/stream/x?radio=" + d.ID, "Mozilla/5.0", "172.16.0.1:4444", ""},
		{"/stream/x?listener=web&device=" + d.ID, "Web player", "192.168.1.50:4444", ""},
		{"/stream/x?device=" + d.ID, "Unknown client", "192.168.1.60:4444", ""},
	} {
		r := httptest.NewRequest("GET", test.path, nil)
		r.RemoteAddr = test.remote
		r.Header.Set("User-Agent", test.ua)
		key := p.beginPlayback(r, s, http.Header{"Icy-Br": {"192"}})
		p.observeMetadata(key, "Artist — Song")
		p.mu.Lock()
		playing := p.active[key]
		p.mu.Unlock()
		if playing.Device != test.want || playing.Title != "Artist — Song" || playing.StationID != "1001" || playing.Bitrate != 192 {
			t.Fatalf("%s: %+v", test.path, playing)
		}
	}
}

func TestMetadataStrippedForClientsThatDoNotRequestIt(t *testing.T) {
	metadata := []byte("StreamTitle='Test song';")
	n := (len(metadata) + 15) / 16
	payload := append([]byte("ABCD"), append([]byte{byte(n)}, append(metadata, make([]byte, n*16-len(metadata))...)...)...)
	payload = append(payload, []byte("EFGH\x00")...)
	for size := 1; size < len(payload); size++ {
		o := newICYObserver("4", func(string) {})
		var audio []byte
		for start := 0; start < len(payload); start += size {
			audio = append(audio, o.Process(payload[start:min(start+size, len(payload))], true)...)
		}
		if string(audio) != "ABCDEFGH" {
			t.Fatalf("chunk %d: %q", size, audio)
		}
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Icy-MetaData") != "1" {
			t.Error("song information not requested")
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Icy-MetaInt", "4")
		w.Write(payload)
	}))
	defer upstream.Close()
	p := testRelay(t, upstream)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", streamURL(t, p), nil))
	if w.Code != 200 || w.Body.String() != "ABCDEFGH" || w.Header().Get("Icy-MetaInt") != "" || w.Header().Get("Content-Length") != "" {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
}
