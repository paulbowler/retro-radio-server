// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func automaticFixture(t *testing.T) (*Manager, DiscoveredServer) {
	t.Helper()
	_, server, _, _ := fixture(t)
	m, _ := NewManager("")
	m.newProvider = func(raw string) (*Provider, error) {
		p, e := New(raw)
		if e == nil {
			p.allowLoopback = true
		}
		return p, e
	}
	candidate := DiscoveredServer{Location: server.URL + "/description.xml", USN: "uuid:music::urn:schemas-upnp-org:device:MediaServer:1", Address: "127.0.0.1"}
	m.discover = func(context.Context) ([]DiscoveredServer, error) { return []DiscoveredServer{candidate}, nil }
	return m, candidate
}
func TestAutomaticServerBrowsePlaybackAndArtwork(t *testing.T) {
	m, _ := automaticFixture(t)
	root, e := m.Browse(context.Background(), "0", 0, 24)
	if e != nil || root.Total != 0 {
		t.Fatal(root, e)
	}
	m.Refresh(context.Background())
	root, e = m.Browse(context.Background(), "0", 0, 24)
	if e != nil || root.Total != 1 || root.Items[0].Title != "MinimServer [NAS]" {
		t.Fatal(root, e)
	}
	page, e := m.Browse(context.Background(), root.Items[0].ID, 48, 24)
	if e != nil || page.ParentID != "0" || page.Total != 1000 {
		t.Fatal(page, e)
	}
	folder, e := m.Browse(context.Background(), page.Items[0].ID, 0, 24)
	if e != nil || folder.ParentID != root.Items[0].ID {
		t.Fatal(folder, e)
	}
	play, e := m.Resolve(context.Background(), page.Items[1].PlaybackID, model.LegacyXML)
	if e != nil || !strings.HasPrefix(play.Item.ParentID, strings.Split(root.Items[0].ID, ":")[0]+":") {
		t.Fatal(play, e)
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "/stream/upnp/"+play.Item.PlaybackID, nil))
	if w.Code != 200 || w.Body.String() != "0123456789" {
		t.Fatal(w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	m.ServeArtwork(w, httptest.NewRequest("GET", "/artwork/upnp/"+play.Item.PlaybackID+".jpg", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if _, e = m.Resolve(context.Background(), "unknown", model.LegacyXML); e != ErrUnknownID {
		t.Fatal(e)
	}
}
func TestDiscoveryCannotFetchAnotherSenderHost(t *testing.T) {
	m, c := automaticFixture(t)
	c.Address = "127.0.0.2"
	m.discover = func(context.Context) ([]DiscoveredServer, error) { return []DiscoveredServer{c}, nil }
	m.Refresh(context.Background())
	page, _ := m.Browse(context.Background(), "0", 0, 24)
	if page.Total != 0 {
		t.Fatal("cross-host location connected", page)
	}
}
func TestAutomaticIdentityExpiryAndFailureIsolation(t *testing.T) {
	m, c := automaticFixture(t)
	now := time.Now()
	m.now = func() time.Time { return now }
	m.Refresh(context.Background())
	first := m.snapshot()[0]
	c.USN = "uuid:music::urn:schemas-upnp-org:service:ContentDirectory:1"
	m.discover = func(context.Context) ([]DiscoveredServer, error) { return []DiscoveredServer{c}, nil }
	m.Refresh(context.Background())
	if len(m.snapshot()) != 1 || m.snapshot()[0].provider != first.provider {
		t.Fatal("duplicate or replaced server")
	}
	m.discover = func(context.Context) ([]DiscoveredServer, error) { return nil, errors.New("network down") }
	m.Refresh(context.Background())
	if len(m.snapshot()) != 1 {
		t.Fatal("discovery failure erased server")
	}
	now = now.Add(6 * time.Minute)
	m.discover = func(context.Context) ([]DiscoveredServer, error) { return nil, nil }
	m.Refresh(context.Background())
	if len(m.snapshot()) != 0 {
		t.Fatal("offline server never expires")
	}
}
func TestManualFallbackAndSettings(t *testing.T) {
	m, e := NewManager("http://192.168.1.10:9791/device.xml")
	if e != nil {
		t.Fatal(e)
	}
	m.SetTranscoding(false)
	if len(m.snapshot()) != 1 || !m.snapshot()[0].provider.DisableTranscode {
		t.Fatal("manual settings not inherited")
	}
	m.discover = func(context.Context) ([]DiscoveredServer, error) { return nil, nil }
	m.now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	m.Refresh(context.Background())
	if len(m.snapshot()) != 1 {
		t.Fatal("manual fallback expired")
	}
	bad, e := NewManager("file:///bad")
	if e == nil || bad == nil {
		t.Fatal("invalid manual config disabled automatic discovery")
	}
}
func TestManagerBackgroundDiscoveryAndCoalescedRefresh(t *testing.T) {
	m, _ := automaticFixture(t)
	for i := 0; i < 100; i++ {
		m.Rediscover()
	}
	if len(m.refresh) != 1 {
		t.Fatal("unbounded refresh queue")
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); m.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for len(m.snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	wg.Wait()
	if len(m.snapshot()) != 1 {
		t.Fatal("automatic startup discovery missing")
	}
}

func TestEmptyAdvertisedLibraryHiddenAndLaterAvailable(t *testing.T) {
	var available atomic.Bool
	var broken atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/description.xml" {
			fmt.Fprint(w, `<root><device><friendlyName>Player with optional library</friendlyName><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></root>`)
			return
		}
		if broken.Load() {
			http.Error(w, "offline", 503)
			return
		}
		if available.Load() {
			fmt.Fprint(w, soap(didlOpen+container("album", "0", "Album")+`</DIDL-Lite>`, 1, 1))
		} else {
			fmt.Fprint(w, soap(didlOpen+`</DIDL-Lite>`, 0, 0))
		}
	}))
	defer server.Close()
	m, c := automaticFixture(t)
	c.Location = server.URL + "/description.xml"
	m.discover = func(context.Context) ([]DiscoveredServer, error) { return []DiscoveredServer{c}, nil }
	m.Refresh(context.Background())
	if len(m.snapshot()) != 0 {
		t.Fatal("empty renderer appeared as a music source")
	}
	available.Store(true)
	m.Refresh(context.Background())
	if len(m.snapshot()) != 1 {
		t.Fatal("newly available library was not discovered")
	}
	broken.Store(true)
	m.Refresh(context.Background())
	if len(m.snapshot()) != 1 {
		t.Fatal("temporary browse error erased working library")
	}
	broken.Store(false)
	available.Store(false)
	m.Refresh(context.Background())
	if len(m.snapshot()) != 0 {
		t.Fatal("empty library was retained")
	}
}

func TestMusicPlaybackObserverOnlyForDeliveredAudio(t *testing.T) {
	m, _ := automaticFixture(t)
	starts, completions := 0, 0
	m.SetPlaybackObserver(func(r *http.Request, p content.Playback) func(bool) {
		starts++
		if p.Item.Title == "" {
			t.Fatal("missing metadata")
		}
		return func(complete bool) {
			if complete {
				completions++
			}
		}
	})
	m.Refresh(context.Background())
	root, _ := m.Browse(context.Background(), "0", 0, 24)
	tracks, e := m.Browse(context.Background(), root.Items[0].ID, 0, 24)
	if e != nil {
		t.Fatal(e)
	}
	path := "/stream/upnp/" + tracks.Items[1].PlaybackID
	m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("HEAD", path, nil))
	m.ServeArtwork(httptest.NewRecorder(), httptest.NewRequest("GET", "/artwork/upnp/"+tracks.Items[1].PlaybackID+".jpg", nil))
	m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/stream/upnp/missing", nil))
	if starts != 0 {
		t.Fatal("non-audio request counted")
	}
	m.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	if starts != 1 || completions != 1 {
		t.Fatal(starts, completions)
	}
}
