// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
	"time"
)

func TestListeningActivityHidesInternalRequests(t *testing.T) {
	events := []model.Event{
		{Kind: "Directory request", Detail: "/BrowseXML/loginXML.asp"},
		{Kind: "Compatibility proxy", Detail: "Upstream audio relayed over HTTP"},
		{Kind: "Stream connected", Detail: "PLANET ROCK: Where Rock Lives: HLS converted to MP3"},
		{Kind: "Stream failed", Detail: "Radio: HTTP 503 from https://upstream.example/private-details"},
	}
	rows := listeningEvents(events)
	if len(rows) != 2 || rows[0].Summary != "Playback started: PLANET ROCK: Where Rock Lives" || strings.Contains(rows[1].Summary, "HTTP") || strings.Contains(rows[1].Summary, "upstream") {
		t.Fatal(rows)
	}
}

func TestRadioCopyAndRenameRefresh(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "radio.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	d, err := s.Seen("copy-test", "pure", "8", "192.168.1.5")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{Store: s, Relay: delivery.New(s)}
	h := a.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/devices", nil))
	body := w.Body.String()
	if strings.Contains(body, "FRONTIER") || strings.Contains(body, "Unverified") || strings.Contains(body, "No recent contact") || !strings.Contains(body, "Last connected") {
		t.Fatal(body)
	}
	r := httptest.NewRequest("POST", "http://radio.local/devices/rename", strings.NewReader(url.Values{"device": {d.ID}, "name": {"Kitchen radio"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>Kitchen radio</h1>") || strings.Contains(w.Body.String(), "Choose a radio to set") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestCustomStationDetectsAudioWithoutFormatFields(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "custom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	relay := delivery.New(s)
	relay.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"audio/mpeg"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("audio", 300))), Request: r}, nil
	})}
	a := &App{Store: s, Relay: relay}
	h := a.Handler()
	r := httptest.NewRequest("POST", "http://radio.local/custom", strings.NewReader("name=Simple+station&url=https%3A%2F%2F1.1.1.1%2Faudio&codec=UNKNOWN&bitrate=0"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Trigger"), `"success":true`) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	stations, err := s.Stations("")
	if err != nil {
		t.Fatal(err)
	}
	for _, station := range stations {
		if station.Name == "Simple station" {
			if station.Codec != "MP3" {
				t.Fatal(station)
			}
			return
		}
	}
	t.Fatal("station was not saved")
}

func TestHumanReadableHealthAndContact(t *testing.T) {
	raw := "HTTP 503: unexpected media response from https://upstream.example"
	h := model.Health{Checked: time.Now(), Message: raw}
	if strings.Contains(stationStatus(h), "503") || strings.Contains(stationStatus(h), "upstream") || h.Message != raw {
		t.Fatal(h)
	}
	if got := lastContact(time.Now().Add(-10 * time.Minute)); got != "Last connected 10 minutes ago" {
		t.Fatal(got)
	}
}
