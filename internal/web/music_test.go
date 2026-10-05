// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"errors"
	"html"
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
)

type webMusicFixture struct {
	fail          bool
	offset, count int
}

func (p *webMusicFixture) Browse(ctx context.Context, id string, offset, count int) (content.Page, error) {
	p.offset = offset
	p.count = count
	if p.fail {
		return content.Page{}, errors.New("offline")
	}
	return content.Page{Title: "MinimServer [NAS]", Total: 100, Returned: 2, ParentID: "0", Items: []content.Item{{ID: "album", Title: "Album & more", Kind: content.Folder}, {ID: "track", Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Duration: "0:09:22", ArtURL: "http://music/art", PlaybackID: "upnp_fixture", PlaybackCodec: "MP3", Kind: content.PlayableItem, Resources: []content.Resource{{Protocol: "http-get", Codec: "MP3"}}}}}, nil
}
func (p *webMusicFixture) Resolve(context.Context, string, model.Capabilities) (content.Playback, error) {
	return content.Playback{}, nil
}
func TestMusicWebAuthBrowsePaginationAndOutage(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "music.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.DB.Close()
	music := &webMusicFixture{}
	a := &App{Store: db, Relay: delivery.New(db), Music: music, Base: "http://radio.local", User: "admin", Password: "test"}
	h := a.Handler()
	r := httptest.NewRequest("GET", "/music", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("GET", musicBrowseURL("album", 24), nil)
	r.SetBasicAuth("admin", "test")
	r.Header.Set("HX-Request", "true")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{"MinimServer [NAS]", "Album &amp; more", "So What", "Miles Davis", "Kind of Blue", "/stream/upnp/upnp_fixture", "/artwork/upnp/upnp_fixture.jpg", "offset=26"} {
		if !strings.Contains(body, want) {
			t.Fatal(want, body)
		}
	}
	for _, unwanted := range []string{"FFmpeg", "FLAC", "Find music servers", "Refresh music", "Music library available", "/stations/music"} {
		if strings.Contains(body, unwanted) {
			t.Fatal("unexpected music interface content", unwanted)
		}
	}
	if !strings.Contains(body, `href="`+html.EscapeString(musicPageURL("album", 26, "24"))+`"`) {
		t.Fatal("next page does not stay in music", body)
	}
	if w.Code != 200 || strings.Contains(body, "<!doctype") || music.offset != 24 || music.count != 24 {
		t.Fatal(w.Code, music)
	}
	r = httptest.NewRequest("GET", musicPageURL("album", 26, "24"), nil)
	r.SetBasicAuth("admin", "test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "offset=24") || !strings.Contains(w.Body.String(), "trail=24%2C26") {
		t.Fatal("partial-page cursor lost", w.Body)
	}
	music.fail = true
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Your music is temporarily unavailable") {
		t.Fatal(w.Code, w.Body)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.SetBasicAuth("admin", "test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("music outage broke dashboard", w.Code)
	}
}
