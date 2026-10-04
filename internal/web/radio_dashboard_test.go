// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/delivery"
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
	app := &App{Store: s, Relay: delivery.New(s), Base: "http://radio.local"}
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
		if w.Code != http.StatusNotFound {
			t.Fatal(path, w.Code)
		}
	}
}
