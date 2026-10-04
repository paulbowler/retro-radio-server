// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
	"time"
)

func TestStreamRankingCompatibilityQualityAndOverride(t *testing.T) {
	s := model.Station{StreamID: "channel", Variants: []model.StreamVariant{
		{ID: "low", URL: "https://audio.example/low", Codec: "MP3", Bitrate: 64},
		{ID: "high", URL: "https://audio.example/high", Codec: "MP3", Bitrate: 320},
		{ID: "aac", URL: "https://audio.example/aac", Codec: "AAC", Bitrate: 256},
		{ID: "adaptive", URL: "https://audio.example/live.m3u8", Codec: "AAC", Bitrate: 512, HLS: true},
		{ID: "flac", URL: "https://audio.example/flac", Codec: "FLAC", Bitrate: 1024},
	}}
	choices := RankedStreams(s, model.LegacyXML, "")
	if len(choices) != 4 || choices[0].ID != "aac" || choices[3].ID != "adaptive" {
		t.Fatal(choices)
	}
	choices = RankedStreams(s, model.Capabilities{HTTP: true, MP3: true}, "")
	if len(choices) != 3 || choices[0].ID != "high" {
		t.Fatal(choices)
	}
	choices = RankedStreams(s, model.LegacyXML, "low")
	if choices[0].ID != "low" {
		t.Fatal("override ignored")
	}
	s.Variants[2].Health = model.Health{Checked: time.Now(), Working: false}
	choices = RankedStreams(s, model.LegacyXML, "")
	if choices[0].ID != "high" {
		t.Fatal("recent failure preferred")
	}
	play, chosen, err := RadioPlayURL("http://radio.local", s, model.Device{ID: "kitchen", Capabilities: model.LegacyXML}, "low")
	if err != nil || chosen.VariantID != "low" || play != "http://radio.local/stream/channel?radio=kitchen&device=kitchen" {
		t.Fatal(play, chosen, err)
	}
}
func TestChannelFallbackBeforeCommittingAudio(t *testing.T) {
	for _, failure := range []string{"status", "empty", "connection", "blocked", "format"} {
		t.Run(failure, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "fallback.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.DB.Close()
			first, err := db.SaveStation(model.Station{Name: "Fallback FM AAC", URL: "https://audio.example/high", Country: "United Kingdom", Source: "radio-browser", RBUUID: "11111111-1111-1111-1111-111111111111", Codec: "AAC", Bitrate: 256})
			if err != nil {
				t.Fatal(err)
			}
			second, err := db.SaveStation(model.Station{Name: "Fallback FM MP3", URL: "https://audio.example/backup", Country: first.Country, Source: first.Source, RBUUID: "22222222-2222-2222-2222-222222222222", Codec: "MP3", Bitrate: 128})
			if err != nil {
				t.Fatal(err)
			}
			if second.ID != first.ID {
				t.Fatal("not grouped")
			}
			relay := New(db)
			calls := []string{}
			relay.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.Path)
				status, body, ct := 200, "backup music", "audio/mpeg"
				if r.URL.Path == "/high" {
					ct = "audio/aac"
					switch failure {
					case "status":
						status = 503
					case "empty":
						body = ""
					case "connection":
						return nil, errors.New("fixture connection failed")
					case "blocked":
						return nil, ErrUnsafeTarget
					case "format":
						ct = "audio/flac"
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {ct}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			w := httptest.NewRecorder()
			relay.ServeHTTP(w, httptest.NewRequest("GET", "/stream/"+first.StreamID, nil))
			if w.Code != 200 || w.Body.String() != "backup music" || w.Header().Get("Content-Type") != "audio/mpeg" || len(calls) != 2 {
				t.Fatal(w.Code, w.Body.String(), calls)
			}
			got, _ := db.Station(first.ID, false)
			if got.Variants[0].Health.Working {
				t.Fatal("failed stream marked healthy")
			}
			h, _ := db.Health(first.ID)
			if !h.Working || h.Codec != "MP3" {
				t.Fatal("fallback health not saved", h)
			}
		})
	}
}
func TestProbeUsesAlternativeWithoutSavingUnsavedChannel(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	relay := New(db)
	relay.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := 503
		if r.URL.Path == "/backup" {
			status = 200
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"audio/mpeg"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 1024))), Request: r}, nil
	})}
	s := model.Station{Name: "Test", Variants: []model.StreamVariant{{ID: "high", URL: "https://audio.example/high", Codec: "MP3", Bitrate: 320}, {ID: "backup", URL: "https://audio.example/backup", Codec: "MP3", Bitrate: 128}}}
	h, err := relay.Probe(context.Background(), s)
	if err != nil || !h.Working || h.VariantID != "backup" {
		t.Fatal(h, err)
	}
	var count int
	db.DB.QueryRow(`SELECT COUNT(*) FROM stream_health`).Scan(&count)
	if count != 0 {
		t.Fatal("probe persisted health")
	}
}
