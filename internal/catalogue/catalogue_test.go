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
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSearchFailoverCacheOfflineAndImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radio.db")
	db, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.DB.Close()
	device, e := db.Seen("catalogue-fixture", "pure", "8", "192.168.1.2")
	if e != nil {
		t.Fatal(e)
	}
	a := model.Candidate{UUID: "11111111-1111-1111-1111-111111111111", Name: "Test FM", Resolved: "https://1.1.1.1/audio", Codec: "MP3", Bitrate: 128}
	b, _ := json.Marshal([]model.Candidate{a})
	calls := 0
	offline := false
	s := &Service{Store: db, Mirrors: []string{"https://first.example", "https://second.example"}, Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		if offline || r.URL.Host == "first.example" {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		if r.URL.Query().Get("limit") != "24" {
			t.Error("unbounded search")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Request: r}, nil
	})}}
	res, e := s.Search(context.Background(), "Test", 0)
	if e != nil || len(res.Stations) != 1 || calls != 2 {
		t.Fatal(res, e, calls)
	}
	res, e = s.Search(context.Background(), "Test", 0)
	if e != nil || !res.Cached || calls != 2 {
		t.Fatal("cache missed")
	}
	_, e = db.DB.Exec(`UPDATE catalogue_cache SET updated=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	if e != nil {
		t.Fatal(e)
	}
	offline = true
	res, e = s.Search(context.Background(), "Test", 0)
	if e != nil || !res.Stale || len(res.Stations) != 1 {
		t.Fatal("offline cache lost", e)
	}
	station, e := s.Import(context.Background(), a.UUID, device.ID)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Import(context.Background(), a.UUID, device.ID)
	if e != nil || again.ID != station.ID || again.StreamID != station.StreamID {
		t.Fatal("duplicate changed identity", e)
	}
	f, e := db.Favourites(device.ID)
	if e != nil || len(f) != 1 {
		t.Fatal(f, e)
	}
	bad := a
	bad.UUID = "22222222-2222-2222-2222-222222222222"
	bad.Resolved = "http://169.254.169.254/latest/meta-data"
	db.CachePut("bad", []model.Candidate{bad})
	if _, e = s.Import(context.Background(), bad.UUID, device.ID); e == nil {
		t.Fatal("private catalogue URL imported")
	}
	if _, e = s.Import(context.Background(), "https://example.com", device.ID); e == nil {
		t.Fatal("arbitrary URL imported")
	}
}

func TestPopularDirectoryCacheAndOffline(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "recent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	entry := model.Candidate{UUID: "11111111-1111-1111-1111-111111111111", Name: "Directory discovery", Codec: "MP3"}
	payload, _ := json.Marshal([]model.Candidate{entry})
	calls := 0
	offline := false
	service := &Service{Store: db, Mirrors: []string{"https://directory.example"}, Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/json/stations/search" || r.URL.Query().Get("countrycode") != "GB" || r.URL.Query().Get("order") != "clickcount" || r.URL.Query().Get("reverse") != "true" || r.URL.Query().Get("hidebroken") != "true" || r.URL.Query().Get("limit") != "24" {
			t.Fatal("unbounded or incorrect directory feed", r.URL)
		}
		status, body := 200, string(payload)
		if offline {
			status, body = 503, ""
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}}
	result, err := service.Popular(context.Background(), "GB", 0)
	if err != nil || len(result.Stations) != 1 {
		t.Fatal(result, err)
	}
	result, err = service.Popular(context.Background(), "GB", 0)
	if err != nil || !result.Cached || calls != 1 {
		t.Fatal("directory cache missed", result, err, calls)
	}
	if _, err = db.Candidate(entry.UUID); err != nil {
		t.Fatal("review candidate not cached", err)
	}
	_, err = db.DB.Exec(`UPDATE catalogue_cache SET updated=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	offline = true
	result, err = service.Popular(context.Background(), "GB", 0)
	if err != nil || !result.Stale || len(result.Stations) != 1 {
		t.Fatal("offline discoveries lost", result, err)
	}
	stations, err := db.Stations("")
	if err != nil || len(stations) != 1 {
		t.Fatal("directory browsing changed library", stations, err)
	}
}

func TestCombinedSearchFiltersAndCacheIsolation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "filters.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	calls := 0
	s := &Service{Store: db, Mirrors: []string{"https://directory.example"}, Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls++
		q := r.URL.Query()
		if q.Get("name") != "BBC" || q.Get("tag") != "jazz" || q.Get("offset") != "24" || q.Get("limit") != "24" || q.Get("hidebroken") != "true" {
			t.Error(q)
		}
		if q.Get("countrycode") != "GB" && q.Get("countrycode") != "US" {
			t.Error(q)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[]`)), Request: r}, nil
	})}}
	for _, country := range []string{"GB", "GB", "US"} {
		if _, err := s.SearchFiltered(context.Background(), "BBC", country, "jazz", 24); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("country filters share cache entries: %d requests", calls)
	}
}
