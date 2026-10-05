// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/catalogue"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
	"time"
)

func TestRadioDashboardFavouritesLastContactAndLiveRefresh(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	kitchen, _ := s.Seen("kitchen", "pure", "", "192.168.1.50")
	bedroom, _ := s.Seen("bedroom", "pure", "", "192.168.1.51")
	s.Rename(kitchen.ID, "Kitchen")
	s.Rename(bedroom.ID, "Bedroom")
	s.Favourite(kitchen.ID, "1001", true)
	_, err = s.DB.Exec(`UPDATE devices SET last_seen=? WHERE id=?`, time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano), bedroom.ID)
	if err != nil {
		t.Fatal(err)
	}
	directory := &catalogue.Service{Store: s, Mirrors: []string{"https://directory.example"}, Client: &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader("[]")), Request: r}, nil
	})}}
	app := &App{Store: s, Relay: delivery.New(s), Catalogue: directory, Base: "http://radio.local"}
	h := app.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/dashboard/live", nil))
	for _, text := range []string{"Kitchen", "Bedroom", "1 favourites", "0 favourites", "Last connected 2 hours ago"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Fatalf("missing %q: %s", text, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal("refresh returned full page")
	}
	playing := delivery.Active{Station: "Test Radio", StationID: "1001", Device: kitchen.ID, Title: "Artist <script> — Song", Codec: "AAC", Bitrate: 192, Since: time.Now().Add(-9 * time.Minute)}
	v := view{Radios: []radioOverview{{Device: kitchen, Favourites: 1, Playing: &playing, Image: "/stations/1001/artwork"}}}
	var html bytes.Buffer
	if err = page.ExecuteTemplate(&html, "dashboard-status", v); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Playing now", "Test Radio", "Artist &lt;script&gt; — Song", "192 kbps", "Listening for 9 min", "data-station-artwork"} {
		if !strings.Contains(html.String(), text) {
			t.Fatalf("missing %q", text)
		}
	}
	// The image endpoint cannot be used to fetch arbitrary links or local targets.
	for _, path := range []string{"/stations/unknown/artwork?url=http://127.0.0.1", "/stations/1001/artwork"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		want := http.StatusNotFound
		if path == "/stations/1001/artwork" {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatal(path, w.Code)
		}
	}
}

func TestDashboardShowsMusicAndPodcasts(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "sources.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.DB.Close()
	musicRadio, _ := db.Seen("music-kitchen", "pure", "", "192.168.1.60")
	podcastRadio, _ := db.Seen("podcast-bedroom", "pure", "", "192.168.1.61")
	relay := delivery.New(db)
	play := content.Playback{Item: content.Item{PlaybackID: "upnp_dashboard", Title: "Chan Chan", Artist: "Buena Vista Social Club", Album: "Buena Vista Social Club", Duration: "4:17", ArtURL: "http://music/cover"}, Resource: content.Resource{Codec: "MP3", Bitrate: 128}}
	finish := relay.TrackMusic(httptest.NewRequest("GET", "/stream/upnp/upnp_dashboard?radio="+musicRadio.ID, nil), play)
	finish(true)
	show, e := db.SavePodcast(model.Podcast{Title: "The News Quiz", Feed: "https://feeds.example/show"}, []model.Episode{{GUID: "episode", Title: "The latest episode", URL: "https://audio.example/episode.mp3", Codec: "MP3", Duration: "28:00"}})
	if e != nil {
		t.Fatal(e)
	}
	episodes, _ := db.Episodes(show.ID)
	relay.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Request: r, Header: http.Header{"Content-Type": {"audio/mpeg"}, "Content-Length": {"5"}}, Body: io.NopCloser(strings.NewReader("audio"))}, nil
	})}
	relay.ServeEpisode(httptest.NewRecorder(), httptest.NewRequest("GET", "/episode/"+episodes[0].ID+"?radio="+podcastRadio.ID, nil))
	app := &App{Store: db, Relay: relay, Base: "http://radio.local"}
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/dashboard/live", nil))
	for _, want := range []string{"Chan Chan", "Buena Vista Social Club", "/artwork/upnp/upnp_dashboard.jpg", "Music", "The latest episode", "The News Quiz", "Podcast"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatal("missing", want, w.Body)
		}
	}
	v, _ := app.baseView(httptest.NewRequest("GET", "/", nil))
	if e = app.dashboardView(&v); e != nil || v.PlayingRadios != 2 {
		t.Fatal(e, v.PlayingRadios)
	}
}
