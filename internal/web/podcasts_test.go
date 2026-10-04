// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/podcast"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
)

type podcastTransport func(*http.Request) (*http.Response, error)

func (f podcastTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPodcastWebJourney(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "podcasts.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	service := podcast.New(s)
	service.Client = &http.Client{Transport: podcastTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"results":[{"collectionName":"Example show","artistName":"Presenter","feedUrl":"https://feeds.example.org/feed"}]}`
		if r.URL.Host == "feeds.example.org" {
			body = `<rss><channel><title>Example &amp; show</title><description>Listen &amp; enjoy.</description>`
			for i := 0; i < 30; i++ {
				body += fmt.Sprintf(`<item><title>Episode %d</title><guid>%d</guid><pubDate>Sun, 4 Oct 2026 12:00:00 +0000</pubDate><enclosure url="https://audio.example.org/%d.mp3" type="audio/mpeg"/></item>`, i, i, i)
			}
			body += `</channel></rss>`
		}
		return &http.Response{StatusCode: 200, Request: r, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	app := &App{Store: s, Relay: delivery.New(s), Podcasts: service, Base: "http://radio.local"}
	h := app.Handler()
	request := func(method, path, body string, hx bool, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://radio.local"+path, strings.NewReader(body))
		if method == "POST" {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/podcasts", "", false, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No podcasts yet") || !strings.Contains(w.Body.String(), `aria-current="page">Podcasts`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/podcasts?q=example", "", true, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "<!doctype") || !strings.Contains(w.Body.String(), "Example show") {
		t.Fatal(w.Code, w.Body.String())
	}
	body := url.Values{"feed": {"https://feeds.example.org/feed"}}.Encode()
	w = request("POST", "/podcasts/subscribe", body, true, "http://other.example")
	if w.Code != 403 {
		t.Fatal("cross-origin subscription accepted")
	}
	w = request("POST", "/podcasts/subscribe", body, true, "http://radio.local")
	if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Trigger"), "Podcast added") || !strings.Contains(w.Header().Get("HX-Location"), `"path":"/podcasts"`) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	w = request("POST", "/podcasts/subscribe", body+"&q=example", true, "http://radio.local")
	if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Location"), "/podcasts?q=example") {
		t.Fatal("search lost after addition", w.Header())
	}
	all, _ := s.Podcasts()
	id := all[0].ID
	w = request("GET", "/podcasts?podcast="+id, "", true, "")
	if w.Code != 200 || strings.Count(w.Body.String(), "<audio controls") != 24 || !strings.Contains(w.Body.String(), "Next →") || strings.Contains(w.Body.String(), "audio.example.org") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request("GET", "/podcasts?podcast="+id+"&offset=24", "", true, "")
	if strings.Count(w.Body.String(), "<audio controls") != 6 || !strings.Contains(w.Body.String(), "Page 2") {
		t.Fatal(w.Body.String())
	}
	w = request("GET", "/podcasts?q=example", "", true, "")
	if !strings.Contains(w.Body.String(), ">Added</span>") {
		t.Fatal("subscription not reflected", w.Body.String())
	}
	w = request("POST", "/podcasts/subscribe", url.Values{"feed": {"http://127.0.0.1/feed"}}.Encode(), true, "http://radio.local")
	if w.Code != 204 || !strings.Contains(w.Header().Get("HX-Trigger"), "Couldn") {
		t.Fatal(w.Code, w.Header())
	}
	w = request("POST", "/podcasts/remove", url.Values{"podcast": {id}}.Encode(), true, "http://radio.local")
	if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Location"), "/podcasts") {
		t.Fatal(w.Code, w.Header())
	}
	all, _ = s.Podcasts()
	if len(all) != 0 {
		t.Fatal("not removed")
	}
}
