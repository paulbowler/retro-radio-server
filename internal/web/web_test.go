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
	"retroradio.local/server/internal/protocol/frontierxml"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
	"time"
)

func TestDashboardAuthAndForms(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	a := &App{Store: s, Relay: delivery.New(s), Base: "http://radio.local", User: "admin", Password: "testsecret"}
	h := a.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 401 {
		t.Fatal("missing auth")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("admin", "testsecret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "● Ready") || !strings.Contains(w.Body.String(), "Your radios") || strings.Contains(w.Body.String(), "Find a station →") || strings.Contains(w.Body.String(), "Playback confirmed") {
		t.Fatal(w.Code, w.Body.String())
	}
	d, e := s.Seen("fixture-token", "pure", "8", "192.168.1.100")
	if e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("admin", "testsecret")
	r.Header.Set("HX-Request", "true")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "<!doctype") || !strings.Contains(w.Body.String(), "Your radios") {
		t.Fatal("bad HTMX fragment")
	}
	r = httptest.NewRequest("POST", "http://radio.local/favourites", strings.NewReader("device="+d.ID+"&station=1001&action=add"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://evil.example")
	r.SetBasicAuth("admin", "testsecret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin write accepted")
	}
	r.Header.Set("Origin", "http://radio.local")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	f, e := s.Favourites(d.ID)
	if e != nil || len(f) != 1 {
		t.Fatal(f, e)
	}
	r = httptest.NewRequest("GET", "/diagnostics", nil)
	r.SetBasicAuth("admin", "testsecret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "fixture-token") || strings.Contains(w.Body.String(), "192.168.1.100") {
		t.Fatal("diagnostics leaked identity")
	}
}

func TestMilestone2JourneyAndFragments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radio.db")
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Seen("journey-radio", "pure", "8", "192.168.1.2")
	if e != nil {
		t.Fatal(e)
	}
	candidate := model.Candidate{UUID: "33333333-3333-3333-3333-333333333333", Name: "Journey FM", Resolved: "https://1.1.1.1/audio", Codec: "MP3", Bitrate: 128}
	payload, _ := json.Marshal([]model.Candidate{candidate})
	cat := &catalogue.Service{Store: s, Mirrors: []string{"https://catalogue.example"}, Client: &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(payload))), Request: r}, nil
	})}}
	relay := delivery.New(s)
	relay.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"audio/mpeg"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("audio", 300))), Request: r}, nil
	})}
	app := &App{Store: s, Relay: relay, Catalogue: cat, Base: "http://radio.local"}
	handler := app.Handler()
	send := func(method, path, body string, hx bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://radio.local"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://radio.local")
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, route := range []string{"/", "/devices", "/stations", "/custom", "/activity", "/settings"} {
		full := send("GET", route, "", false)
		fragment := send("GET", route, "", true)
		if full.Code != 200 || fragment.Code != 200 || !strings.Contains(full.Body.String(), "<!doctype") || strings.Contains(fragment.Body.String(), "<!doctype") {
			t.Fatal(route, full.Code, fragment.Code)
		}
		if !strings.Contains(fragment.Body.String(), `hx-swap-oob="outerHTML"`) {
			t.Fatal("navigation not updated")
		}
	}
	r := httptest.NewRequest("GET", "/stations", nil)
	r.Header.Set("HX-Request", "true")
	r.Header.Set("HX-History-Restore-Request", "true")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "<!doctype") {
		t.Fatal("history restore missing shell")
	}
	w = send("GET", "/stations/results?q=Journey&device="+d.ID, "", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Journey FM") || strings.Contains(w.Body.String(), "Your saved stations") {
		t.Fatal("search did not target results", w.Body.String())
	}
	w = send("POST", "/stations/select", "uuid="+candidate.UUID, true)
	if w.Code != 200 || !strings.Contains(w.Header().Get("HX-Trigger"), `"success":true`) || strings.Contains(w.Body.String(), "Add to radio") || !strings.Contains(w.Body.String(), "Ready to play") {
		t.Fatal("selection did not automatically check", w.Body.String())
	}
	saved, e := s.ByUUID(candidate.UUID)
	if e != nil {
		t.Fatal(e)
	}
	if s.Assigned(d.ID, saved.ID) {
		t.Fatal("selection unexpectedly assigned station")
	}
	library := send("GET", "/stations?mode=library&device="+d.ID, "", true)
	if !strings.Contains(library.Body.String(), "Journey FM") || strings.Contains(library.Body.String(), `name="device" value="`+d.ID) || strings.Contains(library.Body.String(), "Add to radio") || strings.Contains(library.Body.String(), "Manage on radio") {
		t.Fatal("library depends on a radio", library.Body.String())
	}

	w = send("POST", "/favourites", "station="+saved.ID+"&device="+d.ID+"&context=/devices&action=add", true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f, e := s.Favourites(d.ID)
	if e != nil || len(f) != 1 {
		t.Fatal(f, e)
	}
	w = send("GET", "/devices?device="+d.ID, "", true)
	if !strings.Contains(w.Body.String(), `aria-pressed="true"`) || !strings.Contains(w.Body.String(), "Journey FM") {
		t.Fatal("device missing favourite", w.Body.String())
	}
	second, e := s.Seen("second-journey-radio", "pure", "8", "192.168.1.3")
	if e != nil {
		t.Fatal(e)
	}
	w = send("GET", "/devices?device="+second.ID, "", true)
	if !strings.Contains(w.Body.String(), `id="station-`+saved.ID+`"`) || strings.Contains(w.Body.String(), `aria-pressed="true"`) || strings.Contains(w.Body.String(), "Add from your library") {
		t.Fatal("shared library or isolated favourites incorrect", w.Body.String())
	}
	w = send("GET", "/devices", "", true)
	if strings.Count(w.Body.String(), "Manage favourites") != 2 || strings.Contains(w.Body.String(), "Stations on") {
		t.Fatal("list does not show all devices", w.Body.String())
	}

	radio := &frontierxml.Handler{Store: s, Base: "http://radio.local"}
	w = httptest.NewRecorder()
	radio.ServeHTTP(w, httptest.NewRequest("GET", "/setupapp/pure/asp/BrowseXML/FavXML.asp?empty=&mac=journey-radio", nil))
	if !strings.Contains(w.Body.String(), "Journey FM") {
		t.Fatal("radio favourites missing import")
	}
	w = httptest.NewRecorder()
	radio.ServeHTTP(w, httptest.NewRequest("GET", "/setupapp/pure/asp/BrowseXML/Search.asp?sSearchtype=3&Search="+saved.ID+"&mac=journey-radio", nil))
	if !strings.Contains(w.Body.String(), "/stream/"+saved.StreamID) || strings.Contains(w.Body.String(), "https:") {
		t.Fatal("radio did not receive compatibility URL")
	}
	w = httptest.NewRecorder()
	relay.ServeHTTP(w, httptest.NewRequest("GET", "/stream/"+saved.StreamID, nil))
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatal("relay failed")
	}
	w = send("POST", "/stations/check", "station="+saved.ID+"&device="+d.ID+"&context=/favourites", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Ready to play") {
		t.Fatal(w.Code, w.Body.String())
	}
	s.DB.Close()
	s, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	f, e = s.Favourites(d.ID)
	if e != nil || len(f) != 1 || f[0].StreamID != saved.StreamID {
		t.Fatal("restart lost favourite")
	}
	if h, e := s.Health(saved.ID); e != nil || !h.Working {
		t.Fatal("restart lost health")
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeviceHeartsAndSharedLibrary(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "radio.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	d, e := s.Seen("heart-radio", "pure", "8", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.Seen("other-heart-radio", "pure", "8", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	a := &App{Store: s, Relay: delivery.New(s), Base: "http://radio.local"}
	h := a.Handler()
	send := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://radio.local"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	initial := httptest.NewRecorder()
	h.ServeHTTP(initial, httptest.NewRequest("GET", "http://radio.local/devices?device="+d.ID, nil))
	if !strings.Contains(initial.Body.String(), `hx-trigger="load"`) || strings.Contains(initial.Body.String(), "Check again") || strings.Contains(initial.Body.String(), ">Remove from this radio<") {
		t.Fatal("manual controls remain", initial.Body.String())
	}
	a.Relay.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"audio/mpeg"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("audio", 300))), Request: r}, nil
	})}
	checked := send("/stations/check", "station=1001&device="+d.ID+"&context=/devices")
	if checked.Code != 200 || strings.Contains(checked.Body.String(), `hx-trigger="load"`) {
		t.Fatal("automatic check loops", checked.Body.String())
	}
	body := "station=1001&device=" + d.ID + "&context=/devices&action="
	w := send("/favourites", body+"add")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `aria-pressed="true"`) {
		t.Fatal(w.Body.String())
	}
	w = send("/favourites", body+"remove")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `aria-pressed="false"`) || !s.Assigned(d.ID, "1001") {
		t.Fatal(w.Body.String())
	}
	if lenMust, e := s.Favourites(other.ID); e != nil || len(lenMust) != 0 {
		t.Fatal("favourites crossed devices", e)
	}
	radio := &frontierxml.Handler{Store: s, Base: "http://radio.local"}
	w = httptest.NewRecorder()
	radio.ServeHTTP(w, httptest.NewRequest("GET", "/setupapp/pure/asp/BrowseXML/navXML.asp?gofile=Radio&mac=heart-radio", nil))
	if !strings.Contains(w.Body.String(), "Smooth Radio") {
		t.Fatal("unfavourited station unavailable", w.Body.String())
	}
	if _, e = s.Station("1001", false); e != nil {
		t.Fatal("library station deleted", e)
	}
}

func TestLibraryAdmissionWithoutDevices(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	c := model.Candidate{UUID: "44444444-4444-4444-4444-444444444444", Name: "Library Only FM", Resolved: "https://1.1.1.1/audio", Codec: "MP3", Bitrate: 128, Country: "Hungary"}
	if e = s.CachePut("fixture", []model.Candidate{c}); e != nil {
		t.Fatal(e)
	}
	relay := delivery.New(s)
	working := false
	relay.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		status, mime, body := 502, "text/html", "offline"
		if working {
			status, mime, body = 200, "audio/mpeg", strings.Repeat("audio", 300)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{mime}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	app := &App{Store: s, Relay: relay, Base: "http://radio.local"}
	h := app.Handler()
	send := func(method, path, body string, hx bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://radio.local"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	failed := send("POST", "/stations/select", "uuid="+c.UUID, true)
	if !strings.Contains(failed.Header().Get("HX-Trigger"), `"success":false`) {
		t.Fatal(failed.Body.String())
	}
	if _, e = s.ByUUID(c.UUID); e == nil {
		t.Fatal("failed stream saved to library")
	}
	var count int
	s.DB.QueryRow(`SELECT COUNT(*) FROM stream_health`).Scan(&count)
	if count != 0 {
		t.Fatal("unsaved probe wrote health")
	}
	custom := send("POST", "/custom", "name=Failed+custom&url=https%3A%2F%2F1.1.1.1%2Faudio&codec=MP3&bitrate=128", true)
	if !strings.Contains(custom.Header().Get("HX-Trigger"), `"success":false`) {
		t.Fatal(custom.Body.String())
	}
	stations, e := s.Stations("")
	if e != nil || len(stations) != 1 {
		t.Fatal("failed custom added", stations, e)
	}
	working = true
	success := send("POST", "/stations/select", "uuid="+c.UUID, true)
	if !strings.Contains(success.Header().Get("HX-Trigger"), `"success":true`) || strings.Contains(success.Body.String(), "Add to radio") || strings.Contains(success.Body.String(), `name="device" value="`) {
		t.Fatal(success.Body.String())
	}
	saved, e := s.ByUUID(c.UUID)
	if e != nil {
		t.Fatal(e)
	}
	health, e := s.Health(saved.ID)
	if e != nil || !health.Working {
		t.Fatal(health, e)
	}
	s.DB.QueryRow(`SELECT COUNT(*) FROM device_stations`).Scan(&count)
	if count != 0 {
		t.Fatal("library admission assigned station")
	}
	library := send("GET", "/stations?mode=library", "", false)
	if !strings.Contains(library.Body.String(), c.Name) || strings.Contains(library.Body.String(), `name="device"`) || strings.Contains(library.Body.String(), ">Favourites</a>") {
		t.Fatal("device controls in library", library.Body.String())
	}
	oldBookmark := send("GET", "/favourites", "", false)
	if oldBookmark.Code != 303 || oldBookmark.Header().Get("Location") != "/devices" {
		t.Fatal("old favourites bookmark did not redirect")
	}
}

func TestLibraryRemoveFormAndRoute(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "remove.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	d, e := s.Seen("remove-library-radio", "pure", "8", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	a := &App{Store: s, Relay: delivery.New(s), Base: "http://radio.local"}
	h := a.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/stations?mode=library", nil))
	if !strings.Contains(w.Body.String(), "Remove Smooth Radio from the library? This removes it from all radios and their favourites.") || strings.Contains(w.Body.String(), ">In library<") {
		t.Fatal(w.Body.String())
	}
	r := httptest.NewRequest("POST", "http://radio.local/stations/remove", strings.NewReader("station=1001"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("HX-Location") == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.Assigned(d.ID, "1001") {
		t.Fatal("removed station still on device")
	}
	if _, e = s.Station("1001", false); e == nil {
		t.Fatal("removed station remains in library")
	}
}

func TestResultHeaderPreservesUnicode(t *testing.T) {
	w := httptest.NewRecorder()
	actionResult(w, false, "Couldn’t add station", "Rádió 🇭🇺")
	header := w.Header().Get("HX-Trigger")
	for _, r := range header {
		if r > 127 {
			t.Fatal("non-ASCII response header")
		}
	}
	var event map[string]struct {
		Title   string
		Message string
		Success bool
	}
	if err := json.Unmarshal([]byte(header), &event); err != nil {
		t.Fatal(err)
	}
	result := event["station-result"]
	if result.Title != "Couldn’t add station" || result.Message != "Rádió 🇭🇺" || result.Success {
		t.Fatal(result)
	}
}

func TestDiscoveryChecksGateAdditionWithoutSaving(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "discovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	candidate := model.Candidate{UUID: "33333333-3333-3333-3333-333333333333", Name: "Discovery test", Resolved: "https://1.1.1.1/audio", Codec: "MP3"}
	if err := s.CachePut("discovery-test", []model.Candidate{candidate}); err != nil {
		t.Fatal(err)
	}
	relay := delivery.New(s)
	live := false
	relay.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		code, mime, body := 503, "text/plain", "unavailable"
		if live {
			code, mime, body = 200, "audio/mpeg", strings.Repeat("audio", 300)
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{mime}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	h := (&App{Store: s, Relay: relay}).Handler()
	for _, working := range []bool{false, true} {
		live = working
		r := httptest.NewRequest("POST", "/stations/candidate/check", strings.NewReader("uuid="+candidate.UUID))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		body := w.Body.String()
		if w.Code != 200 || strings.Contains(body, "hx-trigger=\"load\"") || strings.Contains(body, " disabled") == working {
			t.Fatal("incorrect discovery availability", working, w.Code, body)
		}
		stations, err := s.Stations("")
		if err != nil || len(stations) != 1 {
			t.Fatal("checking added a station", stations, err)
		}
	}
}

func TestDashboardKeepsTwelveMostPopularUsableStations(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "usable.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	entries := make([]model.Candidate, 36)
	for i := range entries {
		entries[i] = model.Candidate{UUID: fmt.Sprintf("11111111-1111-1111-1111-%012d", i), Name: fmt.Sprintf("Station %d", i), Resolved: fmt.Sprintf("https://1.1.1.1/audio?id=%d", i), Codec: "MP3"}
	}
	cat := &catalogue.Service{Store: s, Mirrors: []string{"https://directory.example"}, Client: &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		offset := 0
		fmt.Sscan(r.URL.Query().Get("offset"), &offset)
		end := offset + catalogue.PageSize
		if end > len(entries) {
			end = len(entries)
		}
		payload, _ := json.Marshal(entries[offset:end])
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(payload))), Request: r}, nil
	})}}
	relay := delivery.New(s)
	relay.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		index := 0
		fmt.Sscan(r.URL.Query().Get("id"), &index)
		status, mime, body := 200, "audio/mpeg", strings.Repeat("audio", 300)
		if index < 14 {
			status, mime, body = 503, "text/plain", "unavailable"
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{mime}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	app := &App{Store: s, Relay: relay, Catalogue: cat, Base: "http://radio.local"}
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/dashboard/discoveries?country=GB", nil))
	select {
	case <-app.discoveriesJobs["GB"].done:
	case <-time.After(5 * time.Second):
		t.Fatal("checks did not finish")
	}
	w = httptest.NewRecorder()
	app.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/dashboard/discoveries?country=GB", nil))
	body := w.Body.String()
	if w.Code != 200 || strings.Count(body, "<article") != 12 || !strings.Contains(body, "<h3>Station 14</h3>") || !strings.Contains(body, "<h3>Station 25</h3>") || strings.Contains(body, "<h3>Station 13</h3>") || strings.Contains(body, "<h3>Station 26</h3>") || strings.Contains(body, " disabled") || strings.Contains(body, "hx-trigger=\"load\"") {
		t.Fatal("incorrect usable directory selection", w.Code, body)
	}
	stations, err := s.Stations("")
	if err != nil || len(stations) != 1 {
		t.Fatal("discovery filtering modified library", stations, err)
	}
}

func TestDiscoveriesAppearBeforeSlowChecksFinish(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "progressive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	entries := []model.Candidate{
		{UUID: "11111111-1111-1111-1111-111111111111", Name: "Slow most popular", Resolved: "https://1.1.1.1/slow", Codec: "MP3"},
		{UUID: "22222222-2222-2222-2222-222222222222", Name: "Fast usable", Resolved: "https://1.1.1.1/fast", Codec: "MP3"},
	}
	payload, _ := json.Marshal(entries)
	cat := &catalogue.Service{Store: s, Mirrors: []string{"https://directory.example"}, Client: &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(payload))), Request: r}, nil
	})}}
	release := make(chan struct{})
	relay := delivery.New(s)
	relay.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/slow" {
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"audio/mpeg"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("audio", 300))), Request: r}, nil
	})}
	app := &App{Store: s, Catalogue: cat, Relay: relay, Base: "http://radio.local"}
	h := app.Handler()
	snapshot := func() string {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/dashboard/discoveries?country=GB", nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		return w.Body.String()
	}
	initial := snapshot()
	if !strings.Contains(initial, "hx-trigger=\"every 1s\"") {
		t.Fatal("no progress polling", initial)
	}
	deadline := time.Now().Add(2 * time.Second)
	for app.discoveriesJobs["GB"].count() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	progress := snapshot()
	close(release)
	select {
	case <-app.discoveriesJobs["GB"].done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not finish")
	}
	if !strings.Contains(progress, "<h3>Fast usable</h3>") || strings.Contains(progress, "<h3>Slow most popular</h3>") || !strings.Contains(progress, "hx-trigger=\"every 1s\"") {
		t.Fatal("fast station waited for slow station", progress)
	}
	final := snapshot()
	if strings.Contains(final, "hx-trigger=\"every 1s\"") || strings.Index(final, "<h3>Slow most popular</h3>") > strings.Index(final, "<h3>Fast usable</h3>") {
		t.Fatal("polling did not stop or popularity order lost", final)
	}
}

func TestSharedStationRendererContextActions(t *testing.T) {
	station := model.Station{ID: "1001", Name: "Shared station", Source: "radio-browser", Codec: "MP3", Bitrate: 128, Country: "United Kingdom", URL: "https://example.com/live", RBUUID: "directory-id"}
	for _, tc := range []struct {
		name, context, want, unwanted string
		candidate                     bool
	}{
		{"library", "/stations", "library-remove", "heart-form", false},
		{"device", "/devices", "heart-form", "library-remove", false},
		{"discovery", "/stations", "library-add", "library-remove", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := card{Station: station, Context: tc.context, Selected: "radio-id", AutoCheck: true}
			if tc.candidate {
				c = candidateStationCard(candidateCard{Candidate: model.Candidate{UUID: station.RBUUID, Name: station.Name, Codec: station.Codec, Bitrate: station.Bitrate, Country: station.Country, URL: station.URL}, AutoCheck: true})
			}
			w := httptest.NewRecorder()
			renderCard(w, c)
			body := w.Body.String()
			if w.Code != 200 || strings.Count(body, "<article ") != 1 || !strings.Contains(body, tc.want) || strings.Contains(body, tc.unwanted) || !strings.Contains(body, "class=\"attributes\"") || strings.Contains(body, "spinner") || !strings.Contains(body, "hx-trigger=\"load\"") {
				t.Fatal(w.Code, body)
			}
		})
	}
}

func TestStationDiscoveryLocationAndLibraryFilters(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "filters.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	for _, station := range []model.Station{
		{Name: "UK Jazz", Country: "United Kingdom", Tags: "jazz,live", URL: "https://example.com/uk", Codec: "MP3"},
		{Name: "US Jazz", Country: "United States", Tags: "jazz", URL: "https://example.com/us", Codec: "MP3"},
		{Name: "UK Rock", Country: "United Kingdom", Tags: "rock", URL: "https://example.com/rock", Codec: "MP3"},
	} {
		if _, err := s.SaveStation(station); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{Store: s, Relay: delivery.New(s)}
	h := a.Handler()
	for _, route := range []string{"/", "/stations", "/stations?mode=library&country=GB&genre=jazz&q=UK"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", route, nil))
		body := w.Body.String()
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		switch route {
		case "/":
			if strings.Contains(body, `id="directory-discoveries"`) {
				t.Fatal("discovery still on dashboard")
			}
		case "/stations":
			if !strings.Contains(body, `hx-get="/stations/discoveries"`) || !strings.Contains(body, `name="genre"`) {
				t.Fatal("missing station discovery or filters")
			}
		default:
			if !strings.Contains(body, ">UK Jazz</h3>") && !strings.Contains(body, `title="jazz,live">UK Jazz</h3>`) {
				t.Fatal("matching library station missing")
			}
			if strings.Contains(body, ">US Jazz</h3>") || strings.Contains(body, ">UK Rock</h3>") {
				t.Fatal("combined filters ignored")
			}
		}
	}
}
