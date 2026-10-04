// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/store"
)

func TestHomeScreenMetadataIconsAndAuth(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	app := &App{Store: s, Relay: delivery.New(s), User: "admin", Password: "secret"}
	h := app.Handler()
	get := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	w := get("GET", "/app.webmanifest")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatal(w.Code, w.Header())
	}
	var manifest struct {
		ID, Name, ShortName, Display, StartURL, Scope string
		Icons                                         []struct{ Src, Sizes, Type, Purpose string }
	}
	// Map the manifest's underscored members explicitly.
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(raw["name"], &manifest.Name)
	json.Unmarshal(raw["short_name"], &manifest.ShortName)
	json.Unmarshal(raw["id"], &manifest.ID)
	json.Unmarshal(raw["start_url"], &manifest.StartURL)
	json.Unmarshal(raw["scope"], &manifest.Scope)
	json.Unmarshal(raw["display"], &manifest.Display)
	json.Unmarshal(raw["icons"], &manifest.Icons)
	if manifest.Name != "Retro Radio" || manifest.ShortName != "Retro Radio" || manifest.ID != "/" || manifest.StartURL != "/" || manifest.Scope != "/" || manifest.Display != "standalone" {
		t.Fatal(manifest)
	}
	icons := map[string]string{"/apple-touch-icon.png": "180x180", "/static/app-icon-152.png": "152x152", "/static/app-icon-167.png": "167x167", "/static/app-icon-32.png": "32x32"}
	for _, icon := range manifest.Icons {
		icons[icon.Src] = icon.Sizes
		if icon.Type != "image/png" {
			t.Fatal(icon)
		}
	}
	if len(manifest.Icons) != 3 || manifest.Icons[2].Purpose != "maskable" {
		t.Fatal(manifest.Icons)
	}
	for path, size := range icons {
		w := get("GET", path)
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
			t.Fatal(path, w.Code, w.Header())
		}
		config, err := png.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
		if err != nil || fmt.Sprintf("%dx%d", config.Width, config.Height) != size {
			t.Fatal(path, config, err)
		}
		head := get("HEAD", path)
		if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != w.Header().Get("Content-Length") {
			t.Fatal("invalid HEAD", path)
		}
	}
	for _, path := range []string{"/", "/preferences", "/static/app.css"} {
		if w := get("GET", path); w.Code != 401 {
			t.Fatal("management auth changed", path, w.Code)
		}
	}
	request := httptest.NewRequest("GET", "/devices", nil)
	request.SetBasicAuth("admin", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request)
	for _, tag := range []string{`name="apple-mobile-web-app-title" content="Retro Radio"`, `name="apple-mobile-web-app-capable" content="yes"`, `rel="manifest" href="/app.webmanifest"`, `rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png"`, `viewport-fit=cover`} {
		if !strings.Contains(w.Body.String(), tag) {
			t.Fatal("missing app metadata", tag)
		}
	}
}
