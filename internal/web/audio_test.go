// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
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
)

func TestChannelAdmissionTestsAlternativesAndKeepsOneRadioFavourite(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "audio.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	relay := delivery.New(s)
	relay.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		status, codec := 200, "audio/mpeg"
		if r.URL.Path == "/bad" {
			status = 503
		}
		if r.URL.Path == "/aac" {
			codec = "audio/aac"
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {codec}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("audio", 300))), Request: r}, nil
	})}
	app := &App{Store: s, Relay: relay, Base: "http://radio.local"}
	channel := model.Station{Name: "Jazz FM", Country: "United Kingdom", Source: "radio-browser", Variants: []model.StreamVariant{
		{ID: "bad", UUID: "11111111-1111-1111-1111-111111111111", URL: "https://1.1.1.1/bad", Codec: "MP3", Bitrate: 320},
		{ID: "aac", UUID: "22222222-2222-2222-2222-222222222222", URL: "https://1.1.1.1/aac", Codec: "AAC", Bitrate: 192},
		{ID: "mp3", UUID: "33333333-3333-3333-3333-333333333333", URL: "https://1.1.1.1/mp3", Codec: "MP3", Bitrate: 128},
	}}
	saved, err := app.admitChannel(context.Background(), channel)
	if err != nil || len(saved.Variants) != 2 {
		t.Fatal(saved, err)
	}
	if _, err = s.ByUUID(channel.Variants[0].UUID); err == nil {
		t.Fatal("failed stream saved")
	}
	device, _ := s.Seen("audio-radio", "pure", "", "127.0.0.1")
	s.Favourite(device.ID, saved.ID, true)
	var selected string
	for _, v := range saved.Variants {
		if v.Codec == "MP3" {
			selected = v.ID
		}
	}
	r := httptest.NewRequest("POST", "http://radio.local/stations/audio", strings.NewReader(url.Values{"station": {saved.ID}, "device": {device.ID}, "variant": {selected}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, r)
	if w.Code != 200 || s.Preferred(device.ID, saved.ID) != selected || strings.Contains(w.Body.String(), `Audio options`) || strings.Contains(w.Body.String(), `name="variant"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	f, _ := s.Favourites(device.ID)
	if len(f) != 1 {
		t.Fatal("audio preference changed favourite")
	}
	other, _ := s.Seen("other-radio", "pure", "", "127.0.0.1")
	if s.Preferred(other.ID, saved.ID) != "" {
		t.Fatal("preference leaked to another radio")
	}
	s.CachePut("new-alternative", []model.Candidate{{UUID: "44444444-4444-4444-4444-444444444444", Name: "Jazz FM (192k)", Country: "The United Kingdom Of Great Britain And Northern Ireland", Homepage: "https://jazz.example", URL: "https://1.1.1.1/extra", Codec: "MP3", Bitrate: 192}})
	enriched := app.enrichChannel(context.Background(), saved)
	if enriched.ID != saved.ID || len(enriched.Variants) != 3 {
		t.Fatal("existing library channel not enriched", enriched)
	}
	favs, _ := s.Favourites(device.ID)
	if len(favs) != 1 || favs[0].ID != saved.ID {
		t.Fatal("enrichment changed favourite")
	}
	again, err := app.admitChannel(context.Background(), channel)
	if err != nil || again.ID != saved.ID || len(again.Variants) != 3 {
		t.Fatal("re-add duplicated channel", again, err)
	}
}
