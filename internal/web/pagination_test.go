// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/catalogue"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
)

func TestSearchAndLibraryPaginationRetainFilters(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "pages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	entries := []model.Candidate{}
	for i := 0; i < 50; i++ {
		entry := model.Candidate{UUID: fmt.Sprintf("22222222-2222-2222-2222-%012d", i), Name: fmt.Sprintf("Station %02d", i), URL: "https://1.1.1.1/audio", Country: "United Kingdom", Tags: "jazz", Codec: "MP3"}
		entries = append(entries, entry)
		if _, err := s.SaveStation(model.Station{Name: entry.Name, URL: entry.URL, Country: entry.Country, Tags: entry.Tags, Codec: entry.Codec}); err != nil {
			t.Fatal(err)
		}
	}
	cat := &catalogue.Service{Store: s, Mirrors: []string{"https://directory.example"}, Client: &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		offset := 0
		fmt.Sscan(r.URL.Query().Get("offset"), &offset)
		end := offset + catalogue.PageSize
		if end > len(entries) {
			end = len(entries)
		}
		if r.URL.Query().Get("countrycode") != "GB" || r.URL.Query().Get("tag") != "jazz" {
			t.Error("filters were lost", r.URL.Query())
		}
		payload, _ := json.Marshal(entries[offset:end])
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(payload))), Request: r}, nil
	})}}
	app := &App{Store: s, Relay: delivery.New(s), Catalogue: cat}
	h := app.Handler()
	for _, mode := range []string{"discover", "library"} {
		for _, offset := range []int{0, 24, 48} {
			r := httptest.NewRequest("GET", fmt.Sprintf("/stations/results?mode=%s&q=Station&country=GB&genre=jazz&offset=%d", mode, offset), nil)
			r.Header.Set("HX-Request", "true")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			body := w.Body.String()
			want := 24
			if offset == 48 {
				want = 2
			}
			first := offset
			if mode == "library" {
				first = 49 - offset
			}
			if w.Code != 200 || strings.Count(body, "<article") != want || strings.Count(body, `aria-label="Station pages"`) != 2 || strings.Index(body, fmt.Sprintf("<h3>Station %02d</h3>", first)) != strings.Index(body, "<h3>") {
				t.Fatal(mode, offset, w.Code, "incorrect pagination")
			}
			if !strings.Contains(body, "country=GB&amp;genre=jazz&amp;mode="+mode) || !strings.Contains(body, "q=Station") {
				t.Fatal("filter links missing")
			}
			if offset == 48 && strings.Contains(body, ">Next →</a>") {
				t.Fatal("last page has a next link")
			}
			if offset == 24 && (!strings.Contains(body, "offset=0") || !strings.Contains(body, "offset=48")) {
				t.Fatal("previous/next offsets incorrect")
			}
		}
	}
}

func TestDiscoveryCursorHistoryValidation(t *testing.T) {
	for _, raw := range []string{"0,38", "-1", "38,0", "0,0", "0,62", "garbage"} {
		_, err := discoveryTrail(raw, 62)
		if (err == nil) != (raw == "0,38") {
			t.Fatal(raw, err)
		}
	}
}
