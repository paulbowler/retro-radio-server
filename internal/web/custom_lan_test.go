// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
)

func TestCustomLANAdmissionRequiresExactOperatorConfiguration(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	relay := delivery.New(db)
	app := &App{Store: db, Relay: relay, Base: "http://radio.local"}
	handler := app.Handler()
	raw := "https://192.168.1.4/streams/bowler-fm"
	send := func(target string) string {
		// Cancel the probe so this test cannot contact a real LAN host. The target
		// policy must still distinguish configured and unconfigured literal URLs.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		form := url.Values{"name": {"LAN station"}, "url": {target}, "codec": {"MP3"}}
		req := httptest.NewRequest("POST", "http://radio.local/custom", strings.NewReader(form.Encode())).WithContext(ctx)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Header().Get("HX-Trigger")
	}
	if body := send(raw); !strings.Contains(body, "configure this LAN stream") {
		t.Fatal(body)
	}
	if err := relay.ConfigureLANStreams(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if body := send(raw); strings.Contains(body, "configure this LAN stream") || !strings.Contains(body, "reach this station") {
		t.Fatal(body)
	}
	if body := send(raw + "?other"); !strings.Contains(body, "configure this LAN stream") {
		t.Fatal(body)
	}
	// Default fetching remains public-only, even for an operator-listed URL.
	if delivery.ValidateURL(raw) == nil || delivery.CheckTarget(context.Background(), raw) == nil {
		t.Fatal("global policy weakened")
	}
}
