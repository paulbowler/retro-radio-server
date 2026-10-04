// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"strings"
	"testing"
	"time"
)

func TestAdaptiveManifestAddressPolicy(t *testing.T) {
	g := &mediaGateway{origin: "http://127.0.0.1:12345", token: "fixture"}
	base, _ := url.Parse("https://audio.example/path/master.m3u8")
	hls := []byte("#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\n../segment.ts?token=public\n")
	out, e := g.hls(hls, base)
	if e != nil || !strings.Contains(string(out), "/fixture/https/audio.example/path/init.mp4") || !strings.Contains(string(out), "/fixture/https/audio.example/segment.ts?token=public") {
		t.Fatal(string(out), e)
	}
	for _, bad := range []string{"http://127.0.0.1/private.ts", "file:///etc/passwd", "http://169.254.169.254/credentials"} {
		if _, e = g.hls([]byte("#EXTM3U\n"+bad+"\n"), base); e == nil {
			t.Fatal("unsafe segment accepted", bad)
		}
		if _, e = g.dash([]byte(`<MPD><Period><BaseURL>`+bad+`</BaseURL></Period></MPD>`), base); e == nil {
			t.Fatal("unsafe DASH accepted", bad)
		}
	}
	out, e = g.dash([]byte(`<MPD><Period><BaseURL>https://cdn.example/audio/</BaseURL><AdaptationSet><SegmentTemplate media="chunk-$Number$.m4s" initialization="https://cdn.example/init.mp4"/></AdaptationSet></Period></MPD>`), base)
	if e != nil || !strings.Contains(string(out), "chunk-$Number$.m4s") || !strings.Contains(string(out), "/fixture/https/cdn.example/audio/") {
		t.Fatal(string(out), e)
	}
	if _, e = g.hls([]byte("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\nsegment.ts\n"), base); e == nil {
		t.Fatal("encrypted HLS accepted")
	}
	if _, e = g.dash([]byte(`<MPD><ContentProtection/></MPD>`), base); e == nil {
		t.Fatal("protected DASH accepted")
	}
}
func TestAdaptiveGatewayChecksEveryRequest(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/octet-stream"}}, Request: r, Body: io.NopCloser(strings.NewReader("#EXTM3U\nhttps://1.1.1.1/segment.ts\n"))}, nil
	})}
	g := &mediaGateway{client: client, origin: "http://127.0.0.1:12345", token: "fixture"}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("GET", "/fixture/https/1.1.1.1/no-extension", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/fixture/https/1.1.1.1/segment.ts") {
		t.Fatal("untyped manifest not rewritten", w.Body.String())
	}
	w = httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("GET", "/fixture/http/127.0.0.1/private", nil))
	if w.Code != 502 || calls != 1 {
		t.Fatal("private fetch attempted", calls, w.Code)
	}
}
func TestAdaptiveFFmpegHLSAndDASH(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("FFmpeg integration runs in the Docker runtime")
	}
	for _, kind := range []string{"hls", "dash"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			filename := "radio.m3u8"
			if kind == "dash" {
				filename = "radio.mpd"
			}
			args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=1000:duration=8", "-c:a", "aac", "-b:a", "64k", "-f", kind}
			if kind == "hls" {
				args = append(args, "-hls_time", "1", "-hls_list_size", "0")
			}
			args = append(args, filepath.Join(dir, filename))
			if out, e := exec.Command("ffmpeg", args...).CombinedOutput(); e != nil {
				t.Fatal(e, string(out))
			}
			up := httptest.NewServer(http.FileServer(http.Dir(dir)))
			defer up.Close()
			p := testRelay(t, up)
			// Keep the public logical URL on responses so relative manifests resolve safely.
			original := p.Client.Transport
			p.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				res, e := original.RoundTrip(r)
				if res != nil {
					res.Request = r
				}
				return res, e
			})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			s := model.Station{URL: "https://audio.example/" + filename, HLS: true, Codec: "AAC"}
			h, e := p.Probe(ctx, s)
			if e != nil || !h.Working || h.Codec != "MP3" || h.Adaptive != kind {
				_, decodeErr := p.openAdaptive(ctx, s.URL, kind)
				t.Fatal("adaptive probe failed", h, e, decodeErr)
			}
			audio, e := p.openAdaptive(ctx, s.URL, kind)
			if e != nil {
				t.Fatal(e)
			}
			buf := make([]byte, 8192)
			if _, e = io.ReadFull(audio, buf); e != nil {
				audio.Close()
				t.Fatal(e)
			}
			cancel()
			audio.Close()
			if len(p.adaptiveSlots) != 0 {
				t.Fatal("process slot leaked after cancellation")
			}
			if strings.Contains(string(buf), "EXTM3U") || strings.Contains(string(buf), "<MPD") {
				t.Fatal("playlist leaked to radio")
			}
		})
	}
}

func TestLiveBBCAdaptive(t *testing.T) {
	raw := os.Getenv("RETRO_TEST_BBC_URL")
	if raw == "" {
		t.Skip("opt-in live BBC check")
	}
	up := httptest.NewServer(http.NotFoundHandler())
	defer up.Close()
	p := testRelay(t, up)
	p.Client = NewClient()
	transport := p.Client.Transport
	p.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		res, e := transport.RoundTrip(r)
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		t.Log("BBC upstream", r.URL.Host, r.URL.Path, "status", status, "error", e)
		return res, e
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	kind := adaptiveKind(raw, "", false)
	audio, e := p.openAdaptive(ctx, raw, kind)
	if e != nil {
		t.Fatal(e)
	}
	defer audio.Close()
	buf := make([]byte, 256<<10)
	if _, e = io.ReadFull(audio, buf); e != nil {
		t.Fatal(e)
	}
	t.Log("BBC stream converted", len(buf), "MP3 bytes")
}

func TestAutomaticHTTPHTTPSNegotiation(t *testing.T) {
	for _, secureWorks := range []bool{true, false} {
		t.Run(map[bool]string{true: "HTTPS available", false: "HTTP fallback"}[secureWorks], func(t *testing.T) {
			var schemes []string
			p := New(nil)
			p.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				schemes = append(schemes, r.URL.Scheme)
				status := 200
				if r.URL.Scheme == "https" && !secureWorks {
					status = 503
				}
				return &http.Response{StatusCode: status, Request: r, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("audio"))}, nil
			})}
			r, _ := http.NewRequest("GET", "http://audio.example/live", nil)
			r.Header.Set("Range", "bytes=0-")
			res, e := p.request(r)
			if e != nil {
				t.Fatal(e)
			}
			res.Body.Close()
			want := "https"
			count := 1
			if !secureWorks {
				want = "http"
				count = 2
			}
			if len(schemes) != count || res.Request.URL.Scheme != want || res.Request.Header.Get("Range") != "bytes=0-" {
				t.Fatal(schemes, res.Request)
			}
		})
	}
}
