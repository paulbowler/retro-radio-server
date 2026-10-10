// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"retroradio.local/server/internal/model"
	"strings"
	"testing"
)

func TestLANConfigurationAndScope(t *testing.T) {
	raw := "https://station.home.paulbowler.co.uk/streams/bowler-fm"
	private := netip.MustParseAddr("192.168.1.4")
	p := &Relay{}
	calls := 0
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		calls++
		return []netip.Addr{private}, nil
	}
	if err := p.configureLANStreams(context.Background(), raw, lookup); err != nil {
		t.Fatal(err)
	}
	custom := model.Station{URL: raw, Source: "custom"}
	if err := p.CheckStationTarget(context.Background(), custom); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("pinned target resolved again")
	}
	if p.lanClient(model.Station{URL: raw, Source: "radio-browser"}) != nil {
		t.Fatal("catalogue granted LAN access")
	}
	if p.lanClient(model.Station{URL: raw + "?other", Source: "custom"}) != nil {
		t.Fatal("another URL granted LAN access")
	}
	client := p.lanClient(custom)
	for _, target := range []string{raw + "/other", "https://station.home.paulbowler.co.uk/admin", "http://192.168.1.5/audio", "http://169.254.169.254/latest", "https://public.example/audio", "http://station.home.paulbowler.co.uk/streams/bowler-fm"} {
		req, _ := http.NewRequest("GET", target, nil)
		if !errors.Is(client.CheckRedirect(req, nil), ErrUnsafeTarget) {
			t.Fatal("redirect accepted", target)
		}
		if _, err := client.Transport.RoundTrip(req); !errors.Is(err, ErrUnsafeTarget) {
			t.Fatal("request accepted", target, err)
		}
	}
	for _, ips := range [][]netip.Addr{
		nil, {private, netip.MustParseAddr("1.1.1.1")}, {netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("169.254.169.254")}, {netip.MustParseAddr("100.100.100.200")},
		{netip.MustParseAddr("::1")}, {netip.MustParseAddr("fe80::1")}, {netip.MustParseAddr("224.0.0.1")},
	} {
		if err := (&Relay{}).configureLANStreams(context.Background(), raw, func(context.Context, string, string) ([]netip.Addr, error) { return ips, nil }); err == nil {
			t.Fatal("unsafe DNS accepted", ips)
		}
	}
	for _, target := range []string{"http://user:password@192.168.1.4/audio", "http://192.168.1.4:22/audio", "file:///audio", "http://192.168.1.4/audio#fragment", "http://192.168.1.4/live.m3u8", "http://[fe80::1%25en0]/audio"} {
		if err := (&Relay{}).configureLANStreams(context.Background(), target, lookup); err == nil {
			t.Fatal("unsafe configuration accepted", target)
		}
	}
	for _, ip := range []string{"10.1.2.3", "172.16.1.4", "fc00::4", "::ffff:192.168.1.4"} {
		if !lanIP(netip.MustParseAddr(ip)) {
			t.Fatal("LAN address rejected", ip)
		}
	}
}

func TestLANProbeRelayAndReconnect(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		io.WriteString(w, strings.Repeat("audio", 300))
	}))
	defer up.Close()
	p := testRelay(t, up)
	u, _ := url.Parse(up.URL)
	raw := "http://unresolvable.invalid:" + u.Port() + "/audio"
	// Test-only pinned loopback fixture. Production configuration rejects loopback.
	p.lanStreams = map[string]*http.Client{raw: newLANStreamClient(raw, []netip.Addr{netip.MustParseAddr("127.0.0.1")})}
	s := model.Station{Name: "LAN station", Source: "custom", URL: raw, Codec: "MP3"}
	h, err := p.Probe(context.Background(), s)
	if err != nil || !h.Working || h.Codec != "MP3" {
		t.Fatal(h, err)
	}
	s, err = p.Store.SaveStation(s)
	if err != nil {
		t.Fatal(err)
	}
	s, err = p.Store.Station(s.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "/stream/"+s.StreamID, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "audio") {
		t.Fatal(w.Code, w.Body.String())
	}
	body, _, err := p.reopenLive(s)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
	s.URL = "http://192.168.1.4/audio"
	p.lanStreams[s.URL] = p.lanStreams[raw]
	play, err := PlayURL("http://radio.local", s, model.LegacyXML)
	if err != nil || play != "http://radio.local/stream/"+s.StreamID {
		t.Fatal(play, err)
	}
	if _, _, err := RadioPlayURL("http://radio.local", s, model.Device{Capabilities: model.LegacyXML}, ""); err != nil {
		t.Fatal(err)
	}
	s.Source = "radio-browser"
	if p.validateStation(s) == nil {
		t.Fatal("non-custom literal LAN URL accepted")
	}
	delete(p.lanStreams, s.URL)
	s.Source = "custom"
	if p.validateStation(s) == nil {
		t.Fatal("unconfigured LAN URL accepted")
	}
}

func TestLANTransportRejectsRedirectBeforeFetchingDestination(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "/admin", http.StatusFound)
	}))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	raw := "http://unresolvable.invalid:" + u.Port() + "/audio"
	client := newLANStreamClient(raw, []netip.Addr{netip.MustParseAddr("127.0.0.1")})
	defer client.CloseIdleConnections()
	res, err := client.Get(raw)
	if res != nil {
		res.Body.Close()
	}
	if !errors.Is(err, ErrUnsafeTarget) || calls != 1 {
		t.Fatal("redirect fetched", calls, err)
	}
}

func TestLANTransportRetainsTLSVerification(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "audio") }))
	defer up.Close()
	u, _ := url.Parse(up.URL)
	raw := "https://unresolvable.invalid:" + u.Port() + "/audio"
	client := newLANStreamClient(raw, []netip.Addr{netip.MustParseAddr("127.0.0.1")})
	defer client.CloseIdleConnections()
	if res, err := client.Get(raw); err == nil {
		res.Body.Close()
		t.Fatal("untrusted/mismatched certificate accepted")
	}
	transport := client.Transport.(scopedStreamTransport).transport
	if transport.Proxy != nil {
		t.Fatal("environment proxy enabled")
	}
}

func TestLANProbeAcceptsOnlyDirectSupportedAudio(t *testing.T) {
	for _, tc := range []struct {
		content string
		working bool
	}{
		{"audio/mpeg", true}, {"audio/aacp", true}, {"audio/flac", false},
		{"application/vnd.apple.mpegurl", false}, {"text/html", false},
	} {
		t.Run(tc.content, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.content)
				io.WriteString(w, strings.Repeat("audio", 300))
			}))
			defer up.Close()
			p := testRelay(t, up)
			u, _ := url.Parse(up.URL)
			raw := "http://unresolvable.invalid:" + u.Port() + "/audio"
			p.lanStreams = map[string]*http.Client{raw: newLANStreamClient(raw, []netip.Addr{netip.MustParseAddr("127.0.0.1")})}
			defer p.lanStreams[raw].CloseIdleConnections()
			h, err := p.Probe(context.Background(), model.Station{URL: raw, Source: "custom", Codec: "UNKNOWN"})
			if err != nil || h.Working != tc.working {
				t.Fatal(h, err)
			}
		})
	}
}

func TestLANOriginWildcardScope(t *testing.T) {
	scope := "https://station.home.paulbowler.co.uk/*"
	p := &Relay{}
	calls := 0
	if err := p.configureLANStreams(context.Background(), scope, func(context.Context, string, string) ([]netip.Addr, error) {
		calls++
		return []netip.Addr{netip.MustParseAddr("192.168.1.4")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	client := p.lanStreams[scope]
	for _, raw := range []string{
		"https://station.home.paulbowler.co.uk/streams/bowler-fm",
		"https://station.home.paulbowler.co.uk/streams/another?quality=high",
		"https://station.home.paulbowler.co.uk/",
		"https://STATION.home.paulbowler.co.uk:443/streams/another",
	} {
		s := model.Station{Source: "custom", URL: raw}
		if p.lanClient(s) != client || p.CheckStationTarget(context.Background(), s) != nil {
			t.Fatal("allowed stream rejected", raw)
		}
		req, _ := http.NewRequest("GET", raw, nil)
		if err := client.CheckRedirect(req, nil); err != nil {
			t.Fatal(err)
		}
		s.Source = "radio-browser"
		if p.lanClient(s) != nil {
			t.Fatal("non-custom scope granted")
		}
	}
	if calls != 1 {
		t.Fatal("origin resolved again")
	}
	for _, raw := range []string{
		"http://station.home.paulbowler.co.uk/audio",
		"https://station.home.paulbowler.co.uk:8443/audio",
		"https://station.home.paulbowler.co.uk.attacker.example/audio",
		"https://other.station.home.paulbowler.co.uk/audio",
		"https://192.168.1.4/audio",
		"https://user:password@station.home.paulbowler.co.uk/audio",
		"https://station.home.paulbowler.co.uk/audio#fragment",
		"http://169.254.169.254/latest",
	} {
		if p.lanClient(model.Station{Source: "custom", URL: raw}) != nil {
			t.Fatal("scope escaped", raw)
		}
		req, _ := http.NewRequest("GET", raw, nil)
		if client.CheckRedirect(req, nil) == nil {
			t.Fatal("redirect escaped", raw)
		}
		if _, err := client.Transport.RoundTrip(req); !errors.Is(err, ErrUnsafeTarget) {
			t.Fatal("fetch escaped", raw, err)
		}
	}
	for _, raw := range []string{
		"station.home.paulbowler.co.uk", "https://*.home.paulbowler.co.uk/*",
		"https://station.home.paulbowler.co.uk/streams/*", "https://station.home.paulbowler.co.uk/**",
		"https://station.home.paulbowler.co.uk/*?query=value",
	} {
		if err := (&Relay{}).configureLANStreams(context.Background(), raw, func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("192.168.1.4")}, nil
		}); err == nil {
			t.Fatal("invalid wildcard accepted", raw)
		}
	}
}

func TestLANOriginWildcardPinnedRedirectAndProbe(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/streams/redirect" {
			http.Redirect(w, r, "/streams/bowler-fm", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, strings.Repeat("audio", 300))
	}))
	defer up.Close()
	p := testRelay(t, up)
	u, _ := url.Parse(up.URL)
	origin := "http://unresolvable.invalid:" + u.Port()
	scope := origin + "/*"
	client := newLANStreamClient(scope, []netip.Addr{netip.MustParseAddr("127.0.0.1")})
	defer client.CloseIdleConnections()
	p.lanStreams = map[string]*http.Client{scope: client}
	for _, path := range []string{"/streams/bowler-fm", "/streams/second?quality=high", "/streams/redirect"} {
		h, err := p.Probe(context.Background(), model.Station{Source: "custom", URL: origin + path, Codec: "UNKNOWN"})
		if err != nil || !h.Working {
			t.Fatal(path, h, err)
		}
	}
}

func TestLANEncodedStarRemainsExactURL(t *testing.T) {
	if lanScopeMatches("https://station.example/%2A", "https://station.example/admin") {
		t.Fatal("encoded star became wildcard")
	}
}
