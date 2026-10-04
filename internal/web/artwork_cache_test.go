// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestArtworkCacheSharedAcrossSearchRadioAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	uuid := "11111111-1111-1111-1111-111111111111"
	imageURL := "https://1.1.1.1/logo.png"
	db.DB.Exec(`UPDATE stations SET favicon=? WHERE id='1001'`, imageURL)
	db.CachePut("fixture", []model.Candidate{{UUID: uuid, Name: "Smooth Radio", URL: "https://1.1.1.1/audio", Favicon: imageURL}})
	var encoded bytes.Buffer
	png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 32, 32)))
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(encoded.Bytes())), Request: r}, nil
	})}
	app := &App{Store: db, Relay: delivery.New(db), ArtworkClient: client}
	h := app.Handler()
	request := func(path string, radio bool) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		if radio {
			app.ServeRadioArtwork(w, r)
		} else {
			h.ServeHTTP(w, r)
		}
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatal(w.Code, w.Body.String())
		}
		if radio && w.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatal(w.Header())
		}
	}
	request("/stations/candidate/"+uuid+"/artwork", false)
	request("/stations/1001/artwork", false)
	request("/artwork/1001.jpg", true)
	if calls.Load() != 1 {
		t.Fatal("logo downloaded repeatedly", calls.Load())
	}
	db.DB.Close()
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	app = &App{Store: db, Relay: delivery.New(db), ArtworkClient: client}
	h = app.Handler()
	request("/stations/1001/artwork", false)
	request("/artwork/1001.jpg", true)
	if calls.Load() != 1 {
		t.Fatal("cache lost on restart", calls.Load())
	}
	// Saved library and favourites render without load-triggered stream probes.
	device, err := db.Seen("cache-radio", "pure", "8", "192.168.1.5")
	if err != nil {
		t.Fatal(err)
	}
	db.Favourite(device.ID, "1001", true)
	for _, path := range []string{"/stations?mode=library", "/devices?device=" + device.ID} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), `hx-post="/stations/check"`) {
			t.Fatal("saved page probes stream", path, w.Code)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("saved page fetched externally")
	}
}
func TestArtworkNegativeCacheAndStaleOfflineResponse(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "stale.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	db.DB.Exec(`UPDATE stations SET favicon='https://1.1.1.1/logo.png' WHERE id='1001'`)
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if calls.Load() > 1 {
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		return nil, io.ErrUnexpectedEOF
	})}
	app := &App{Store: db, Relay: delivery.New(db), ArtworkClient: client}
	h := app.Handler()
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/stations/1001/artwork", nil))
		return w
	}
	get()
	get()
	if calls.Load() != 1 {
		t.Fatal("missing logo retried immediately")
	}
	station, _ := db.Station("1001", false)
	key := artworkKey(station)
	var img bytes.Buffer
	png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 16, 16)))
	radio, _ := radioJPEG(img.Bytes())
	if err = db.SaveArtwork(key, store.Artwork{Data: img.Bytes(), Kind: "image/png", Radio: radio}); err != nil {
		t.Fatal(err)
	}
	db.DB.Exec(`UPDATE artwork_cache SET updated=?`, time.Now().Add(-8*24*time.Hour).UTC().Format(time.RFC3339Nano))
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() { response <- get() }()
	select {
	case w := <-response:
		if !bytes.Equal(w.Body.Bytes(), img.Bytes()) {
			t.Fatal("last good logo lost")
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("stale image blocked on network")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("refresh not started")
	}
	get()
	if calls.Load() != 2 {
		t.Fatal("duplicate refresh")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		app.artworkMu.Lock()
		pending := len(app.artworkPending)
		app.artworkMu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	cached, err := db.Artwork(key)
	if err != nil || !bytes.Equal(cached.Data, img.Bytes()) || time.Until(cached.RetryAfter) < 14*time.Minute {
		t.Fatal("outage lost image or failed to defer retry", err)
	}
}
