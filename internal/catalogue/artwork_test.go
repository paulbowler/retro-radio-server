// SPDX-License-Identifier: GPL-3.0-only
package catalogue

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
)

func TestArtworkForLegacyStationsMatchesStreamOrChannel(t *testing.T) {
	for _, test := range []struct {
		name, source, stationName, candidateName, candidateURL string
		cached, hasImage, want                                 bool
	}{
		{"seed cached channel", "seed", "Smooth Radio", "Smooth Radio", "https://1.1.1.1/regional", true, false, true},
		{"exact URL different name", "seed", "Smooth Radio", "Smooth UK", "http://1.1.1.1/live", false, true, true},
		{"cached image", "seed", "Smooth Radio", "Smooth Radio", "https://1.1.1.1/live", true, true, true},
		{"name alone", "seed", "Smooth Radio", "Smooth Radio", "https://8.8.8.8/different", true, true, false},
		{"custom exact URL", "custom", "My radio", "Smooth Radio", "https://1.1.1.1/live", true, true, true},
		{"custom same host is insufficient", "custom", "Smooth Radio", "Smooth Radio", "https://1.1.1.1/different", true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "artwork.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.DB.Close()
			candidate := model.Candidate{UUID: "11111111-1111-1111-1111-111111111111", Name: test.candidateName, Country: "United Kingdom", URL: test.candidateURL}
			image := "https://images.example/logo.png"
			if test.hasImage {
				candidate.Favicon = image
			}
			if test.cached {
				db.CachePut("fixture", []model.Candidate{candidate})
			}
			station := model.Station{ID: "1001", Name: test.stationName, Source: test.source, Country: "United Kingdom", URL: "https://1.1.1.1/live"}
			calls := 0
			service := &Service{Store: db, Mirrors: []string{"https://directory.example"}, Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				calls++
				items := []model.Candidate{}
				if strings.HasPrefix(r.URL.Path, "/json/stations/byuuid/") {
					fresh := candidate
					fresh.Favicon = image
					items = append(items, fresh)
				}
				if r.URL.Path == "/json/stations/byurl" && test.want {
					items = append(items, candidate)
				}
				payload, _ := json.Marshal(items)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(payload))), Request: r}, nil
			})}}
			before, _ := db.Stations("")
			got, err := service.ArtworkForStation(context.Background(), station)
			if test.want && (err != nil || got != image) {
				t.Fatal(got, err)
			}
			if !test.want && got != "" {
				t.Fatal("wrong station image selected", got)
			}
			if !test.want {
				previous := calls
				service.ArtworkForStation(context.Background(), station)
				if calls != previous {
					t.Fatal("missing artwork was not cached")
				}
			}
			after, _ := db.Stations("")
			if len(after) != len(before) || after[0].URL != before[0].URL || after[0].Name != before[0].Name {
				t.Fatal("artwork lookup changed the library")
			}
		})
	}
}

func TestArtworkPublisherMatchRetainsCountryAndProgramme(t *testing.T) {
	a := model.Station{Name: "Smooth Radio", Country: "United Kingdom", Source: "seed", URL: "https://media-ice.musicradio.com/live"}
	b := model.Station{Name: "Smooth Radio", Country: "GB", Source: "radio-browser", URL: "https://media-the.musicradio.com/regional"}
	if !sameArtworkChannel(a, b) {
		t.Fatal("broadcaster subdomains failed to match")
	}
	b.Name = "Smooth Country"
	if sameArtworkChannel(a, b) {
		t.Fatal("different programme matched")
	}
	b.Name = a.Name
	b.Country = "Hungary"
	if sameArtworkChannel(a, b) {
		t.Fatal("different country matched")
	}
	b.Country = a.Country
	a.URL = "https://first.co.uk/live"
	b.URL = "https://second.co.uk/live"
	if sameArtworkChannel(a, b) {
		t.Fatal("unrelated country-code domains matched")
	}
}
