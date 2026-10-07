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
	if post("buffer=7&reconnect=on&quality=low&country=GB&agent_voice=ballad&agent_volume=250", "") != 204 {
		t.Fatal("save failed")
	}
	saved := s.Settings()
	if saved.AgentVolume != 250 || saved.AgentVoice != "ballad" || saved.BufferSeconds != 7 || !saved.AutoReconnect || saved.Quality != "low" || saved.Country != "GB" {
		t.Fatal(saved)
	}
	for _, body := range []string{"buffer=0&quality=auto&agent_volume=x", "buffer=0&quality=auto&agent_volume=0", "buffer=0&quality=auto&agent_volume=401", "buffer=0&quality=auto&agent_voice=invalid", "buffer=11&quality=auto", "buffer=-1&quality=auto", "buffer=x&quality=auto", "buffer=0&quality=invalid", "buffer=0&quality=auto&country=ZZ"} {
		if post(body, "") != 400 || s.Settings() != saved {
			t.Fatal("bad settings accepted", body)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/preferences", nil))
	if !strings.Contains(w.Body.String(), `name="agent_volume" type="range" min="25" max="400" step="25" value="250"`) || !strings.Contains(w.Body.String(), `<optgroup label="Male voices">`) || !strings.Contains(w.Body.String(), `<optgroup label="Female voices">`) || !strings.Contains(w.Body.String(), `value="ballad" selected`) || !strings.Contains(w.Body.String(), "Agent FM voice") {
		t.Fatal("saved voice not selected")
	}
	if post("buffer=0&quality=auto", "") != 204 || (s.Settings().AgentVoice != "ballad" || s.Settings().AgentVolume != 250) {
		t.Fatal("older settings form erased voice")
	}
	if post("buffer=0&quality=auto&country=GB", "") != 204 {
		t.Fatal("restore country")
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

func TestLocalSettingsPersistOverrideAndConsent(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	resets := 0
	app := &App{Store: db, AgentLocalReset: func() { resets++ }}
	h := app.Handler()
	post := func(body string) int {
		r := httptest.NewRequest("POST", "/preferences", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	body := "buffer=0&quality=auto&country=GB&agent_local_settings=1&agent_local_enabled=on&agent_location=Winchester%2C+UK&agent_local_sources=https%3A%2F%2Fwww.winchester.gov.uk"
	if post(body) != 204 {
		t.Fatal("local settings save failed")
	}
	saved := db.Settings()
	if !saved.AgentLocalEnabled || saved.AgentAutoLocation || saved.AgentLocation != "Winchester, UK" || saved.AgentLocalSources != "https://www.winchester.gov.uk" || resets != 1 {
		t.Fatal(saved, resets)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/preferences", nil))
	if !strings.Contains(w.Body.String(), `value="Winchester, UK"`) || !strings.Contains(w.Body.String(), "ipwho.is") || !strings.Contains(w.Body.String(), "OpenAI web search") {
		t.Fatal("saved location or disclosure missing")
	}
	if post("buffer=0&quality=auto") != 204 || db.Settings().AgentLocation != saved.AgentLocation || !db.Settings().AgentLocalEnabled {
		t.Fatal("legacy form erased local settings")
	}
	if post("buffer=0&quality=auto&agent_local_settings=1&agent_local_sources=javascript%3Aalert%281%29") != 400 {
		t.Fatal("invalid source accepted")
	}
	if post("buffer=0&quality=auto&agent_local_settings=1&agent_location=Winchester%2C+UK") != 204 || db.Settings().AgentLocalEnabled || db.Settings().AgentAutoLocation {
		t.Fatal("opt-out failed")
	}
}
