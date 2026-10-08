package upnp

import (
	"context"
	"encoding/xml"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"retroradio.local/server/internal/model"
	"strings"
	"testing"
	"time"
)

func TestAlbumContainerRelativeArtworkAndTrackInheritance(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/description":
			fmt.Fprint(w, `<root><device><friendlyName>Album server</friendlyName><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></root>`)
		case "/control":
			var req browseRequest
			if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
				return
			}
			album := strings.Replace(container("album", "0", "Album"), "</container>", `<albumArtURI>/cover</albumArtURI></container>`, 1)
			song := fmt.Sprintf(`<item id="song" parentID="album"><title>Song</title><class>object.item.audioItem.musicTrack</class><res protocolInfo="http-get:*:audio/mpeg:*">%s/audio</res></item>`, server.URL)
			entry := album
			if req.Object == "song" || req.Flag == "BrowseDirectChildren" && req.Object == "album" {
				entry = song
			}
			fmt.Fprint(w, soap(didlOpen+entry+`</DIDL-Lite>`, 1, 1))
		case "/cover":
			png.Encode(w, image.NewRGBA(image.Rect(0, 0, 100, 100)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p, _ := New(server.URL + "/description")
	p.allowLoopback = true
	root, err := p.Browse(context.Background(), "0", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	album := root.Items[0]
	if album.ArtURL != server.URL+"/cover" || album.PlaybackID == "" {
		t.Fatal(album)
	}
	if _, err = p.Resolve(context.Background(), album.PlaybackID, model.LegacyXML); err == nil {
		t.Fatal("album became playable")
	}
	page, err := p.Browse(context.Background(), "album", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].ArtURL != album.ArtURL {
		t.Fatal("album artwork not inherited", page)
	}
	for _, item := range []string{album.PlaybackID, page.Items[0].PlaybackID} {
		w := httptest.NewRecorder()
		p.ServeArtwork(w, httptest.NewRequest("GET", "/artwork/upnp/"+item+".jpg", nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if _, err := jpeg.Decode(w.Body); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRebuildInvalidatesTokensQueuesAndRecreatesManualSources(t *testing.T) {
	m, folder := queueFixture(t, 2, "tracks")
	_, id, err := m.StartSequence(context.Background(), folder, "", model.LegacyXML)
	if err != nil {
		t.Fatal(err)
	}
	old := m.snapshot()[0].provider
	token := m.queues[id].tracks[0].PlaybackID
	m.agentLastPlay = map[string]time.Time{"old": time.Now()}
	m.discover = func(context.Context) ([]DiscoveredServer, error) { return nil, nil }
	m.newProvider = func(raw string) (*Provider, error) {
		p, e := New(raw)
		if p != nil {
			p.allowLoopback = true
		}
		return p, e
	}
	if warnings := m.Rebuild(context.Background()); len(warnings) != 0 {
		t.Fatal(warnings)
	}
	if len(m.queues) != 0 || len(m.agentLastPlay) != 0 {
		t.Fatal("queue/history retained")
	}
	if m.snapshot()[0].provider == old {
		t.Fatal("old provider retained")
	}
	if _, ok := m.providerFor(token); ok {
		t.Fatal("old token retained")
	}
	if _, err := m.Browse(context.Background(), folder, 0, 1); err != nil {
		t.Fatal("normal browsing unavailable", err)
	}
}
