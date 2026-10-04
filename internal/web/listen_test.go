// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
)

func TestStationBrowserListening(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "listen.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	candidate := model.Candidate{UUID: "preview", Name: "Preview Radio", URL: "https://audio.example.org/live.mp3", Codec: "MP3"}
	unsafe := model.Candidate{UUID: "unsafe", Name: "Private", URL: "http://127.0.0.1/private.mp3", Codec: "MP3"}
	if e = s.CachePut("listen", []model.Candidate{candidate, unsafe}); e != nil {
		t.Fatal(e)
	}
	relay := delivery.New(s)
	calls := 0
	relay.Client = &http.Client{Transport: podcastTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Request: r, Header: http.Header{"Content-Type": {"audio/mpeg"}}, Body: io.NopCloser(strings.NewReader("radio audio"))}, nil
	})}
	a := &App{Store: s, Relay: relay, Base: "http://radio.local", User: "admin", Password: "secret"}
	h := a.Handler()
	r := httptest.NewRequest("GET", "/stations/listen?uuid=preview", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || calls != 0 {
		t.Fatal("preview bypassed management login")
	}
	r.SetBasicAuth("admin", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "radio audio" || calls != 1 {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
	all, _ := s.Stations("")
	if len(all) != 1 {
		t.Fatal("preview saved station")
	}
	for _, query := range []string{"uuid=missing", "url=https://audio.example.org/live.mp3", "uuid=unsafe"} {
		r = httptest.NewRequest("GET", "/stations/listen?"+query, nil)
		r.SetBasicAuth("admin", "secret")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 && w.Code != 502 {
			t.Fatal(query, w.Code)
		}
		if calls != 1 {
			t.Fatal("unsafe or arbitrary target requested")
		}
	}
	saved := card{Station: model.Station{ID: "1001", StreamID: "opaque"}, Context: "/devices", Selected: "kitchen"}
	if stationListenURL(saved) != "/stream/opaque?device=kitchen" {
		t.Fatal("radio audio preference lost")
	}
	w = httptest.NewRecorder()
	renderCard(w, candidateStationCard(candidateCard{Candidate: candidate}))
	if !strings.Contains(w.Body.String(), `preload="none"`) || !strings.Contains(w.Body.String(), `src="/stations/listen?uuid=preview"`) || strings.Contains(w.Body.String(), "audio.example.org") {
		t.Fatal(w.Body.String())
	}
}
