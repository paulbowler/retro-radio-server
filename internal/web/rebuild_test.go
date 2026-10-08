package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/maintenance"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/podcast"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
)

func TestDevelopmentRebuildProtectedAndRunsFreshSources(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "radio.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	app := &App{Store: s, Relay: delivery.New(s), Maintenance: &maintenance.Gate{}, User: "admin", Password: "testsecret"}
	disabled := app.Handler()
	r := httptest.NewRequest("POST", "/development/rebuild", nil)
	r.SetBasicAuth("admin", "testsecret")
	w := httptest.NewRecorder()
	disabled.ServeHTTP(w, r)
	if w.Code != 404 && w.Code != 405 {
		t.Fatal("production endpoint accessible", w.Code)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/preferences", nil)
	r.SetBasicAuth("admin", "testsecret")
	disabled.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "/development/rebuild") {
		t.Fatal("production link visible")
	}
	app.Development = true
	sources := 0
	app.RebuildSources = func(ctx context.Context) []string {
		sources++
		plays, _ := s.MusicLastPlays()
		if len(plays) != 0 {
			t.Error("sources ran before clearing history")
		}
		if err := s.CachePut("fresh", []model.Candidate{{UUID: "fresh", Name: "Fresh"}}); err != nil {
			t.Error(err)
		}
		return []string{"Fixture source offline."}
	}
	handler := app.Maintenance.Handler(app.Handler())
	submit := func(token, confirm, origin string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://radio.test/development/rebuild", strings.NewReader(url.Values{"token": {token}, "confirm": {confirm}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if auth {
			r.SetBasicAuth("admin", "testsecret")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := submit(app.rebuildToken, "REBUILD", "", false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := submit(app.rebuildToken, "REBUILD", "http://evil.test", true); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, pair := range [][2]string{{"bad", "REBUILD"}, {app.rebuildToken, "yes"}} {
		if w := submit(pair[0], pair[1], "", true); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if sources != 0 {
		t.Fatal("unconfirmed reset executed")
	}
	d, _ := s.Seen("dev", "pure", "", "192.168.1.1")
	if w := submit(app.rebuildToken, "REBUILD", "http://radio.test", true); w.Code != 200 || !strings.Contains(w.Body.String(), "Database rebuilt") || !strings.Contains(w.Body.String(), "Fixture source offline") {
		t.Fatal(w.Code, w.Body.String())
	}
	if sources != 1 {
		t.Fatal(sources)
	}
	if _, err := s.Device(d.ID); err == nil {
		t.Fatal("old radio retained")
	}
	if _, err := s.Candidate("fresh"); err != nil {
		t.Fatal("normal source output missing", err)
	}
}

func TestRebuildReingestsFeedsAndKeepsOfflineSubscriptionsWithoutStaleEpisodes(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "radio.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	for _, suffix := range []string{"online", "offline"} {
		if _, err := s.SavePodcast(model.Podcast{Feed: "https://1.1.1.1/" + suffix, Title: "Old show"}, []model.Episode{{GUID: "stale", Title: "Stale episode", URL: "https://1.1.1.1/old.mp3", Codec: "MP3"}}); err != nil {
			t.Fatal(err)
		}
	}
	feeds := podcast.New(s)
	feeds.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		status := 200
		body := `<rss><channel><title>Current show</title><item><guid>fresh</guid><title>Current episode</title><enclosure url="https://1.1.1.1/current.mp3" type="audio/mpeg"/></item></channel></rss>`
		if r.URL.Path == "/offline" {
			status = 503
			body = "offline"
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	app := &App{Development: true, Maintenance: &maintenance.Gate{}, Store: s, Relay: delivery.New(s), Podcasts: feeds}
	h := app.Handler()
	r := httptest.NewRequest("POST", "/development/rebuild", strings.NewReader(url.Values{"token": {app.rebuildToken}, "confirm": {"REBUILD"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "publisher was unavailable") {
		t.Fatal(w.Code, w.Body.String())
	}
	subscriptions, err := s.Podcasts()
	if err != nil || len(subscriptions) != 2 {
		t.Fatal(subscriptions, err)
	}
	for _, feed := range subscriptions {
		episodes, err := s.Episodes(feed.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(feed.Feed, "online") {
			if len(episodes) != 1 || episodes[0].GUID != "fresh" || feed.Title != "Current show" {
				t.Fatal(feed, episodes)
			}
		} else if len(episodes) != 0 {
			t.Fatal("stale offline episodes retained", episodes)
		}
	}
}
