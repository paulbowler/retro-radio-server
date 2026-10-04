// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"testing"
)

func TestEpisodeRelayHTTPSRangeAndHead(t *testing.T) {
	db, e := store.Open(filepath.Join(t.TempDir(), "episodes.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.DB.Close()
	if e = db.SaveSettings(store.Settings{BufferSeconds: 10, AutoReconnect: true, Quality: "low"}); e != nil {
		t.Fatal(e)
	}
	p, e := db.SavePodcast(model.Podcast{Title: "Show", Feed: "https://feeds.example.org"}, []model.Episode{{GUID: "first", Title: "Episode", URL: "http://audio.example.org/one.mp3", Codec: "MP3"}})
	if e != nil {
		t.Fatal(e)
	}
	eps, _ := db.Episodes(p.ID)
	relay := New(db)
	for _, method := range []string{"GET", "HEAD"} {
		t.Run(method, func(t *testing.T) {
			calls := 0
			relay.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Scheme != "https" || r.Header.Get("Range") != "bytes=3-5" {
					t.Fatal(r.URL, r.Header)
				}
				return &http.Response{StatusCode: 206, Request: r, Header: http.Header{"Content-Type": {"audio/mpeg"}, "Content-Length": {"3"}, "Content-Range": {"bytes 3-5/10"}, "Accept-Ranges": {"bytes"}}, Body: io.NopCloser(strings.NewReader("345"))}, nil
			})}
			r := httptest.NewRequest(method, "/episode/"+eps[0].ID, nil)
			r.Header.Set("Range", "bytes=3-5")
			w := httptest.NewRecorder()
			relay.ServeEpisode(w, r)
			if w.Code != 206 || w.Header().Get("Content-Range") != "bytes 3-5/10" || calls != 1 {
				t.Fatal(w.Code, w.Header(), calls)
			}
			if method == "GET" && w.Body.String() != "345" || method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal(w.Body.String())
			}
		})
	}
	if e = db.DeletePodcast(p.ID); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	relay.ServeEpisode(w, httptest.NewRequest("GET", "/episode/"+eps[0].ID, nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
