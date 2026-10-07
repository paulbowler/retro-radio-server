// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
)

type webAgentFixture struct {
	enabled, fail bool
	starts        int
	genre         string
}

func (p *webAgentFixture) Browse(_ context.Context, id string, _, _ int) (content.Page, error) {
	return content.Page{Title: "Jazz", ParentID: "0", Genre: id == "jazz", Total: 1, Returned: 1, Items: []content.Item{{ID: "jazz", Title: "Jazz", Kind: content.Folder}}}, nil
}
func (p *webAgentFixture) Resolve(context.Context, string, model.Capabilities) (content.Playback, error) {
	return content.Playback{}, nil
}
func (p *webAgentFixture) AgentFMAvailable() bool { return p.enabled }
func (p *webAgentFixture) StartAgentFM(context.Context, model.Capabilities) (content.Playback, string, error) {
	p.starts++
	return content.Playback{}, "global-session", nil
}
func (p *webAgentFixture) StartGenreFM(_ context.Context, folder string, _ model.Capabilities) (content.Playback, string, error) {
	p.starts++
	p.genre = folder
	if p.fail || folder != "jazz" {
		return content.Playback{}, "", errors.New("not enough genre music")
	}
	return content.Playback{}, "genre-session", nil
}
func (p *webAgentFixture) ServeQueue(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Write([]byte(r.URL.Path + "?" + r.URL.RawQuery))
}

func TestWebAgentCardsLazyPlaybackAuthAndGenreScope(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	p := &webAgentFixture{enabled: true}
	app := &App{Store: db, Relay: delivery.New(db), Music: p, User: "admin", Password: "secret"}
	h := app.Handler()
	get := func(method, path string, auth bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if auth {
			r.SetBasicAuth("admin", "secret")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	root := get("GET", "/music", true)
	if root.Code != 200 || !strings.Contains(root.Body.String(), "Agent FM") || !strings.Contains(root.Body.String(), `data-agent-stream="/music/agent"`) || !strings.Contains(root.Body.String(), "/artwork/agent-fm.jpg") {
		t.Fatal(root.Code, root.Body)
	}
	if strings.Contains(root.Body.String(), `src="/music/agent`) || p.starts != 0 {
		t.Fatal("navigation started DJ playback")
	}
	genre := get("GET", musicBrowseURL("jazz", 0), true)
	if !strings.Contains(genre.Body.String(), "[Jazz FM]") || !strings.Contains(genre.Body.String(), "genre="+base64.RawURLEncoding.EncodeToString([]byte("jazz"))) {
		t.Fatal(genre.Body)
	}
	album := get("GET", musicBrowseURL("album", 0), true)
	if strings.Contains(album.Body.String(), "data-agent-stream") {
		t.Fatal("agent card outside a genre")
	}
	if get("GET", "/music/agent", false).Code != 401 || p.starts != 0 {
		t.Fatal("playback bypassed authentication")
	}
	if w := get("HEAD", "/music/agent", true); w.Code != 200 || w.Body.Len() != 0 || p.starts != 0 {
		t.Fatal("HEAD started playback", w.Code, p.starts)
	}
	w := get("GET", "/music/agent", true)
	if w.Code != 200 || w.Body.String() != "/stream/upnp-queue/global-session?listener=web" || p.starts != 1 {
		t.Fatal(w.Code, w.Body, p.starts)
	}
	w = get("GET", "/music/agent?genre="+base64.RawURLEncoding.EncodeToString([]byte("jazz")), true)
	if w.Code != 200 || p.genre != "jazz" || w.Body.String() != "/stream/upnp-queue/genre-session?listener=web" {
		t.Fatal(w.Code, w.Body, p.genre)
	}
	starts := p.starts
	if get("GET", "/music/agent?genre=%%%", true).Code != 400 || p.starts != starts {
		t.Fatal("invalid folder accepted")
	}
	p.fail = true
	if get("GET", "/music/agent?genre="+base64.RawURLEncoding.EncodeToString([]byte("jazz")), true).Code != 503 {
		t.Fatal("genre failure did not surface")
	}
	p.enabled = false
	if strings.Contains(get("GET", "/music", true).Body.String(), "data-agent-stream") || get("GET", "/music/agent", true).Code != 503 {
		t.Fatal("disabled agent exposed")
	}
}
