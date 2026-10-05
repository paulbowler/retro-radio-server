// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
)

func TestSettingsModalSaveCountryAndNavigation(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	app := &App{Store: s, Relay: delivery.New(s)}
	h := app.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/preferences", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `type="range" min="0" max="10"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	post := func(body, origin string) int {
		r := httptest.NewRequest("POST", "/preferences", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if post("buffer=7&reconnect=on&quality=low&country=GB", "") != 204 {
		t.Fatal("save failed")
	}
	saved := s.Settings()
	if saved.BufferSeconds != 7 || !saved.AutoReconnect || saved.Quality != "low" || saved.Country != "GB" {
		t.Fatal(saved)
	}
	for _, body := range []string{"buffer=11&quality=auto", "buffer=-1&quality=auto", "buffer=x&quality=auto", "buffer=0&quality=invalid", "buffer=0&quality=auto&country=ZZ"} {
		if post(body, "") != 400 || s.Settings() != saved {
			t.Fatal("bad settings accepted", body)
		}
	}
	if post("buffer=0&quality=auto", "https://evil.example") != 403 {
		t.Fatal("cross origin save allowed")
	}
	req := httptest.NewRequest("GET", "/stations", nil)
	req.Header.Set("Accept-Language", "en-US")
	if country, automatic := app.listenerCountry(req); country != "GB" || automatic {
		t.Fatal(country, automatic)
	}
	req = httptest.NewRequest("GET", "/stations?country=FR", nil)
	if country, _ := app.listenerCountry(req); country != "FR" {
		t.Fatal("explicit filter ignored")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/devices", nil))
	body := w.Body.String()
	if strings.Contains(body, ">Dashboard</a>") || strings.Contains(body, ">Help</a>") || !strings.Contains(body, `aria-label="Retro Radio home"`) || !strings.Contains(body, "data-open-preferences") {
		t.Fatal("wrong header", body)
	}
	app.Password = "secret"
	h = app.Handler()
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/preferences", nil))
	if w.Code != 401 {
		t.Fatal("settings bypassed login")
	}
}
