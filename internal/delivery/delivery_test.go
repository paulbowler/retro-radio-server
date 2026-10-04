// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testRelay(t *testing.T, up *httptest.Server) *Relay {
	t.Helper()
	s, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	_, e = s.DB.Exec(`UPDATE stations SET url=?`, "https://audio.example/live")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`UPDATE stream_variants SET url=?`, "https://audio.example/live"); e != nil {
		t.Fatal(e)
	}
	p := New(s)
	target, _ := url.Parse(up.URL)
	// Test-only transport maps an ordinary public HTTPS origin to the TLS fixture.
	p.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		clone.URL = &u
		return up.Client().Transport.RoundTrip(clone)
	})}
	p.Client.CheckRedirect = NewClient().CheckRedirect
	return p
}
func streamURL(t *testing.T, p *Relay) string {
	t.Helper()
	s, e := p.Store.Station("1001", false)
	if e != nil {
		t.Fatal(e)
	}
	return "/stream/" + s.StreamID
}
func TestPublicAddressPolicy(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "::1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.100.100.200", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "224.0.0.1", "0.1.2.3", "198.18.0.1", "64:ff9b::7f00:1"} {
		if SafeIP(netip.MustParseAddr(raw)) {
			t.Fatal("accepted", raw)
		}
	}
	if !SafeIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public rejected")
	}
	for _, raw := range []string{"file:///etc/passwd", "http://user:secret@example.com", "http://example.com:22", "unix:///socket"} {
		if ValidateURL(raw) == nil {
			t.Fatal(raw)
		}
	}
}
func TestProductionClientBlocksPrivateAndRedirect(t *testing.T) {
	c := NewClient()
	for _, raw := range []string{"http://127.0.0.1", "http://[::1]", "http://169.254.169.254"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		r, _ := http.NewRequestWithContext(ctx, "GET", raw, nil)
		res, e := c.Do(r)
		cancel()
		if res != nil {
			res.Body.Close()
		}
		if e == nil {
			t.Fatal("private access allowed")
		}
	}
	r, _ := http.NewRequest("GET", "file:///etc/passwd", nil)
	if c.CheckRedirect(r, []*http.Request{}) == nil {
		t.Fatal("unsafe redirect allowed")
	}
}
func TestHTTPSICYChunkedStreamingAndCancellation(t *testing.T) {
	disconnected := make(chan struct{})
	payload := []byte("ABCD\x01StreamTitle='x';")
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Icy-MetaData") != "1" {
			t.Error("metadata negotiation lost")
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Icy-MetaInt", "4")
		w.Header().Set("Icy-Name", "Test")
		w.Write(payload)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(disconnected)
	}))
	defer up.Close()
	p := testRelay(t, up)
	down := httptest.NewServer(p)
	defer down.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", down.URL+streamURL(t, p), nil)
	req.Header.Set("Icy-MetaData", "1")
	res, e := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if e != nil {
		t.Fatal(e)
	}
	if res.Header.Get("Icy-MetaInt") != "4" || res.Header.Get("Icy-Name") != "Test" {
		t.Fatal("ICY headers lost")
	}
	data := make([]byte, len(payload))
	if _, e = io.ReadFull(res.Body, data); e != nil {
		t.Fatal("stream buffered", e)
	}
	if string(data) != string(payload) {
		t.Fatal("ICY bytes modified")
	}
	active := p.Active()
	if len(active) != 1 || active[0].Title != "x" {
		t.Fatalf("song metadata not observed: %+v", active)
	}
	cancel()
	res.Body.Close()
	select {
	case <-disconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream not cancelled")
	}
}
func TestHTTP10AndRange(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		if r.Header.Get("Range") != "bytes=2-4" {
			t.Error("range missing")
		}
		w.Header().Set("Content-Range", "bytes 2-4/8")
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(206)
		w.Write([]byte("cde"))
	}))
	defer up.Close()
	p := testRelay(t, up)
	down := httptest.NewServer(p)
	defer down.Close()
	conn, e := net.Dial("tcp", strings.TrimPrefix(down.URL, "http://"))
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	io.WriteString(conn, "GET "+streamURL(t, p)+" HTTP/1.0\r\nRange: bytes=2-4\r\n\r\n")
	res, e := http.ReadResponse(bufio.NewReader(conn), nil)
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	if res.StatusCode != 206 || string(data) != "cde" || res.Header.Get("Content-Range") == "" {
		t.Fatal(res.StatusCode, string(data))
	}
	if res.Header.Get("Transfer-Encoding") != "" {
		t.Fatal("chunking HTTP/1.0")
	}
}
func TestOpaqueOnlyAndContentType(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("not music"))
	}))
	defer up.Close()
	p := testRelay(t, up)
	for _, path := range []string{"/stream/not-known?url=http://127.0.0.1", streamURL(t, p)} {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code < 400 {
			t.Fatal("accepted unsafe stream")
		}
	}
}
func TestCapabilitySelection(t *testing.T) {
	s := model.Station{Codec: "MP3", URL: "https://example.com/audio", StreamID: "opaque"}
	got, e := PlayURL("http://radio.local", s, model.LegacyXML)
	if e != nil || got != "http://radio.local/stream/opaque" {
		t.Fatal(got, e)
	}
	c := model.LegacyXML
	c.HTTPS = true
	got, e = PlayURL("http://radio.local", s, c)
	if e != nil || got != s.URL {
		t.Fatal(got, e)
	}
	s.Codec = "OPUS"
	if _, e = PlayURL("http://radio.local", s, c); e == nil {
		t.Fatal("unsupported codec accepted")
	}
}

func TestRedirectRelayAndUnsafeScheme(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(map[bool]string{false: "https redirect", true: "unsafe redirect"}[unsafe], func(t *testing.T) {
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/live" {
					target := "https://audio.example/final"
					if unsafe {
						target = "file:///etc/passwd"
					}
					http.Redirect(w, r, target, 302)
					return
				}
				w.Header().Set("Content-Type", "audio/mpeg")
				w.Write([]byte("music"))
			}))
			defer up.Close()
			p := testRelay(t, up)
			w := httptest.NewRecorder()
			p.ServeHTTP(w, httptest.NewRequest("GET", streamURL(t, p), nil))
			if unsafe {
				if w.Code != 502 {
					t.Fatal("unsafe redirect accepted")
				}
				return
			}
			if w.Code != 200 || w.Body.String() != "music" || w.Header().Get("Location") != "" {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestHealthAndDirectHTTPDecision(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Icy-Br", "192")
		w.Write([]byte(strings.Repeat("audio", 300)))
	}))
	defer up.Close()
	p := testRelay(t, up)
	s, _ := p.Store.Station("1001", false)
	h, e := p.Check(context.Background(), s)
	if e != nil || !h.Working || h.Codec != "MP3" || h.Bitrate != 192 || h.LastSuccess.IsZero() {
		t.Fatal(h, e)
	}
	saved, e := p.Store.Health(s.ID)
	if e != nil || saved.FinalURL == "" {
		t.Fatal("health final URL not persisted", e)
	}
	a := model.Station{Codec: "MP3", URL: "http://1.1.1.1/audio", StreamID: "opaque"}
	healthy := model.Health{Working: true, FinalURL: a.URL, Checked: time.Now()}
	got, e := PlayURL("http://radio.local", a, model.LegacyXML, healthy)
	if e != nil || got != a.URL {
		t.Fatal(got, e)
	}
	healthy.HTTPS = true
	got, e = PlayURL("http://radio.local", a, model.LegacyXML, healthy)
	if e != nil || got != "http://radio.local/stream/opaque" {
		t.Fatal("HTTPS redirect not relayed")
	}
	healthy.HTTPS = false
	healthy.Checked = time.Now().Add(-2 * time.Hour)
	got, _ = PlayURL("http://radio.local", a, model.LegacyXML, healthy)
	if got == a.URL {
		t.Fatal("stale validation bypassed relay")
	}
	a.HLS = true
	if got, err := PlayURL("http://radio.local", a, model.LegacyXML); err != nil || got != "http://radio.local/stream/opaque" {
		t.Fatal("HLS did not select MP3 relay", got, err)
	}
	if ValidateURL("http://127.0.0.1:8000/audio") == nil {
		t.Fatal("private high port allowed")
	}
	if ValidateURL("http://1.1.1.1:8000/audio") != nil {
		t.Fatal("public broadcaster port blocked")
	}
}

func TestRejectPlaylistAudioMime(t *testing.T) {
	for _, content := range []string{"audio/x-mpegurl", "audio/x-scpls", "application/vnd.apple.mpegurl"} {
		up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", content)
			w.Write([]byte("https://private.example/segments"))
		}))
		p := testRelay(t, up)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("GET", streamURL(t, p), nil))
		up.Close()
		if w.Code != 502 || strings.Contains(w.Body.String(), "https://private") {
			t.Fatal("playlist escaped through relay", content, w.Code)
		}
	}
}
