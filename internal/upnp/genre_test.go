// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"retroradio.local/server/internal/agentfm"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/protocol/frontierxml"
	"retroradio.local/server/internal/store"
)

func genreFixture(t *testing.T, native bool, jazzTracks int, direct bool) *Manager {
	t.Helper()
	var server *httptest.Server
	metadata := map[string]string{
		"genre":  container("genre", "0", "Genre"),
		"jazz":   container("jazz", "genre", "Jazz"),
		"pop":    container("pop", "genre", "Pop"),
		"albums": container("albums", "jazz", "Albums"),
		"items":  container("items", "jazz", "Items"),
		"album":  container("album", "albums", "Jazz album"),
	}
	if native {
		metadata["genre"] = container("genre", "0", "Styles")
		metadata["jazz"] = strings.Replace(metadata["jazz"], "object.container", "object.container.genre.musicGenre", 1)
	}
	var jazz []string
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("track%d", i)
		metadata[id] = fmt.Sprintf(`<item id="%s" parentID="album"><title>Track %d</title><class>object.item.audioItem.musicTrack</class><res protocolInfo="http-get:*:audio/mpeg:*" bitrate="16000">PLACEHOLDER/audio</res></item>`, id, i)
		if i < jazzTracks {
			jazz = append(jazz, id)
		}
	}
	children := map[string][]string{"0": {"genre"}, "genre": {"jazz", "pop"}, "jazz": {"albums", "items"}, "albums": {"album"}, "album": jazz, "items": jazz, "pop": {"track2", "track3"}}
	if direct {
		children["jazz"] = jazz
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/description" {
			fmt.Fprint(w, `<root><device><friendlyName>MinimServer</friendlyName><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></root>`)
			return
		}
		var request browseRequest
		if err := xml.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		ids := children[request.Object]
		if request.Flag == "BrowseMetadata" {
			ids = []string{request.Object}
		}
		end := min(len(ids), request.Start+request.Count)
		var entries strings.Builder
		for i := request.Start; i < end; i++ {
			entries.WriteString(strings.ReplaceAll(metadata[ids[i]], "PLACEHOLDER", server.URL))
		}
		fmt.Fprint(w, soap(didlOpen+entries.String()+`</DIDL-Lite>`, max(0, end-request.Start), len(ids)))
	}))
	t.Cleanup(server.Close)
	p, err := New(server.URL + "/description")
	if err != nil {
		t.Fatal(err)
	}
	p.allowLoopback = true
	m, _ := NewManager("")
	m.servers["fixture"] = musicServer{key: "fixture", provider: p, manual: true, seen: time.Now()}
	m.agent = agentServiceFunc(func(context.Context, agentfm.Track, []agentfm.Track) (agentfm.Segment, error) {
		t.Error("API called while browsing")
		return agentfm.Segment{}, nil
	})
	m.agentFFmpeg = fakeAgentFFmpeg(t)
	return m
}

func TestGenreFMMenuScopeAndPagination(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			m := genreFixture(t, native, 2, false)
			db, err := store.Open(filepath.Join(t.TempDir(), "genre.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.DB.Close()
			h := &frontierxml.Handler{Store: db, Base: "http://radio.test", Music: m}
			type menu struct {
				Count int                `xml:"ItemCount"`
				Items []frontierxml.Item `xml:"Item"`
			}
			get := func(path string) menu {
				t.Helper()
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", h.Base+"/setupapp/pure/asp/BrowseXML/"+path+"&mac=0123456789abcdef", nil))
				var result menu
				if w.Code != 200 || xml.Unmarshal(w.Body.Bytes(), &result) != nil {
					t.Fatal(w.Code, w.Body)
				}
				return result
			}
			link := "navXML.asp?music=" + base64.RawURLEncoding.EncodeToString([]byte("fixture:jazz"))
			full := get(link)
			if full.Count != 3 || len(full.Items) != 4 || full.Items[1].Name != "[Jazz FM]" {
				t.Fatal(full)
			}
			first := get(link + "&startItems=1&endItems=1")
			tail := get(link + "&startItems=2&endItems=3")
			if first.Count != 3 || len(first.Items) != 2 || first.Items[1].Name != "[Jazz FM]" || len(tail.Items) != 3 || tail.Items[1].Title != "Albums" || tail.Items[2].Title != "Items" {
				t.Fatal(first, tail)
			}
			for _, folder := range []string{"fixture:genre", "fixture:album"} {
				ordinary := get("navXML.asp?music=" + base64.RawURLEncoding.EncodeToString([]byte(folder)))
				for _, item := range ordinary.Items {
					if strings.HasSuffix(item.Name, " FM]") {
						t.Fatal("FM offered outside a genre", ordinary)
					}
				}
			}
			selected := get("Search.asp?sSearchtype=3&Search=" + full.Items[1].ID).Items[1]
			if selected.Name != "Jazz FM" || selected.Logo == nil || *selected.Logo != h.Base+"/artwork/agent-fm.jpg" {
				t.Fatal(selected)
			}
			session := strings.Split(strings.Split(selected.URL, "/stream/upnp-queue/")[1], "?")[0]
			q := m.queues[session]
			if q.name != "Jazz FM" || len(q.tracks) != 2 {
				t.Fatal(q)
			}
			w := httptest.NewRecorder()
			m.ServeQueue(w, httptest.NewRequest("HEAD", selected.URL, nil))
			if w.Code != 200 || w.Header().Get("icy-name") != "Jazz FM" || q.next != 0 {
				t.Fatal("incorrect genre stream header or HEAD consumed playback", w.Code, w.Header())
			}
			for _, track := range q.tracks {
				if track.ID != "fixture:track0" && track.ID != "fixture:track1" {
					t.Fatal("escaped the genre", track)
				}
			}
			if _, _, err = m.StartGenreFM(context.Background(), "fixture:album", model.LegacyXML); err == nil {
				t.Fatal("accepted an album as a genre")
			}
		})
	}
}

func TestGenreFMDoesNotFallBackToOtherGenres(t *testing.T) {
	m := genreFixture(t, false, 1, false)
	if _, _, err := m.StartGenreFM(context.Background(), "fixture:jazz", model.LegacyXML); err == nil {
		t.Fatal("one-track genre silently used other genres")
	}
	tracks, err := m.agentLibrary(context.Background(), "0")
	if err != nil || len(tracks) != 3 {
		t.Fatal("global Agent FM lost other genres", len(tracks), err)
	}
}

func TestGenreFMAndPlayAllShareTrackMenuPagination(t *testing.T) {
	m := genreFixture(t, false, 2, true)
	db, err := store.Open(filepath.Join(t.TempDir(), "direct.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	h := &frontierxml.Handler{Store: db, Base: "http://radio.test", Music: m}
	for _, tc := range []struct {
		start, end int
		names      []string
	}{{1, 1, []string{"[Jazz FM]"}}, {2, 2, []string{"[Play All]"}}, {3, 4, []string{"Track 0", "Track 1"}}} {
		path := fmt.Sprintf("http://radio.test/setupapp/pure/asp/BrowseXML/navXML.asp?music=%s&startItems=%d&endItems=%d&mac=0123456789abcdef", base64.RawURLEncoding.EncodeToString([]byte("fixture:jazz")), tc.start, tc.end)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		var menu struct {
			Count int                `xml:"ItemCount"`
			Items []frontierxml.Item `xml:"Item"`
		}
		if w.Code != 200 || xml.Unmarshal(w.Body.Bytes(), &menu) != nil || menu.Count != 4 || len(menu.Items) != len(tc.names)+1 {
			t.Fatal(w.Code, w.Body)
		}
		for i, name := range tc.names {
			if menu.Items[i+1].Name != name {
				t.Fatal(menu)
			}
		}
	}
}
