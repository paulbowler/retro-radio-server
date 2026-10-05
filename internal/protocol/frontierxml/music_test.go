// SPDX-License-Identifier: GPL-3.0-only
package frontierxml

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"net/http/httptest"
	"net/url"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"strconv"
	"strings"
	"testing"
	"time"
)

type musicFixture struct {
	fail          bool
	offset, count int
	direct        bool
}

func (p *musicFixture) Browse(ctx context.Context, id string, offset, count int) (content.Page, error) {
	if p.fail {
		return content.Page{}, errors.New("offline")
	}
	p.offset = offset
	p.count = count
	page := content.Page{ParentID: "0", Title: "Album", Total: 31, Returned: 2}
	switch id {
	case "0":
		page.ParentID = "-1"
		page.Items = []content.Item{{ID: "server", Title: "MinimServer", Kind: content.Folder}}
	case "server":
		page.ParentID = "0"
		page.Items = []content.Item{{ID: "Album&1", Title: "Album", Kind: content.Folder}}
	case "Album&1":
		page.Items = []content.Item{{ID: "Kind of Blue", Title: "Kind of Blue", Kind: content.Folder}}
	case "Kind of Blue":
		page.ParentID = "Album&1"
		page.Items = []content.Item{{ID: "track", ParentID: id, Title: "So What", Kind: content.PlayableItem, PlaybackID: "upnp_fixture"}}
	default:
		return content.Page{}, errors.New("missing")
	}
	return page, nil
}
func (p *musicFixture) Resolve(ctx context.Context, id string, caps model.Capabilities) (content.Playback, error) {
	if p.fail || id != "upnp_fixture" {
		return content.Playback{}, errors.New("unavailable")
	}
	return content.Playback{Direct: p.direct && caps.HTTPS, Item: content.Item{ID: "track", ParentID: "Kind of Blue", Title: `Midnight Sun 'live' "mix"`, Artist: "Miles Davis", Album: "Kind of Blue", Duration: "0:09:22", ArtURL: "https://music.home/art.jpg", PlaybackID: id}, Resource: content.Resource{URL: "https://music.home/audio", Codec: "MP3", Bitrate: 128}}, nil
}
func TestMusicMenuHierarchyPaginationMetadataAndLookup(t *testing.T) {
	h := newHandler(t)
	p := &musicFixture{}
	h.Music = p
	get := func(link string) list {
		t.Helper()
		r := httptest.NewRequest("GET", link+"&mac=0123456789abcdef", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		var result list
		if e := xml.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		return result
	}
	base := h.Base + "/setupapp/pure/asp/BrowseXML/"
	root := get(base + "loginXML.asp?gofile=")
	if root.Count != 7 {
		t.Fatal(root)
	}
	var music Item
	for _, item := range root.Items {
		if item.Title == "My Music" {
			music = item
		}
	}
	servers := get(music.Dir)
	if servers.Items[1].Title != "MinimServer" {
		t.Fatal(servers)
	}
	categories := get(servers.Items[1].Dir)
	albums := get(categories.Items[1].Dir)
	tracks := get(albums.Items[1].Dir + "&startItems=25&endItems=31")
	if tracks.Count != 31 || tracks.Items[1].Name != "So What" || p.offset != 24 || p.count != 7 {
		t.Fatal(tracks, p)
	}
	if !strings.Contains(tracks.Items[0].Previous, base64.RawURLEncoding.EncodeToString([]byte("Album&1"))) {
		t.Fatal("parent lost", tracks)
	}
	stationID := tracks.Items[1].ID
	if n, e := strconv.Atoi(stationID); e != nil || n < musicIDBase || n >= musicIDLimit {
		t.Fatal("music station ID does not match numeric catalogue", stationID)
	}
	lookup := get(base + "Search.asp?sSearchtype=3&Search=" + url.QueryEscape(stationID))
	item := lookup.Items[1]
	if item.ID != stationID || item.Format != "Radio" || item.Name != `Midnight Sun 'live' "mix"` || item.URL != h.Base+"/stream/upnp/upnp_fixture" || item.Logo == nil || *item.Logo != h.Base+"/artwork/upnp/upnp_fixture.jpg" || !strings.Contains(item.Desc, "Miles Davis") || !strings.Contains(item.Desc, "Kind of Blue") || item.Mime != "MP3" {
		t.Fatal(item)
	}
	h.SupportsHTTPS = true
	p.direct = true
	lookup = get(base + "Search.asp?sSearchtype=3&Search=upnp_fixture")
	if lookup.Items[1].URL != "https://music.home/audio" {
		t.Fatal(lookup)
	}
	p.fail = true
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", servers.Items[1].Dir+"&mac=0123456789abcdef", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	root = get(base + "loginXML.asp?gofile=")
	if root.Count != 7 {
		t.Fatal("music outage broke root")
	}
	radio := get(base + "navXML.asp?gofile=Radio")
	if radio.Count < 1 {
		t.Fatal("music outage broke radio")
	}
}
func TestMusicInvalidPagination(t *testing.T) {
	h := newHandler(t)
	h.Music = &musicFixture{}
	for _, suffix := range []string{"&startItems=no", "&startItems=0", "&endItems=500", "&startItems=4&endItems=2"} {
		_, _, e := h.musicItems(context.Background(), "http://radio/", url.Values{"music": {base64.RawURLEncoding.EncodeToString([]byte("0"))}}, model.LegacyXML)
		if e != nil {
			t.Fatal(e)
		}
		q, _ := url.ParseQuery("music=MA" + suffix)
		if _, _, e = h.musicItems(context.Background(), "http://radio/", q, model.LegacyXML); e == nil {
			t.Fatal(suffix)
		}
	}
}

func TestMusicStationAliasesBoundedExpiredAndStable(t *testing.T) {
	h := newHandler(t)
	id, e := h.musicStationID("upnp_example")
	if e != nil {
		t.Fatal(e)
	}
	again, e := h.musicStationID("upnp_example")
	if e != nil || again != id {
		t.Fatal("alias changed", again, e)
	}
	token, e := h.musicPlaybackID(id)
	if e != nil || token != "upnp_example" {
		t.Fatal(token, e)
	}
	h.musicIDs[id] = musicStationID{playback: token, seen: time.Now().Add(-25 * time.Hour)}
	if _, e = h.musicPlaybackID(id); e == nil {
		t.Fatal("expired alias accepted")
	}
	if _, e = h.musicPlaybackID("1999999999"); e == nil {
		t.Fatal("unissued alias accepted")
	}
	for i := 0; i < 4100; i++ {
		if _, e = h.musicStationID("upnp_" + strconv.Itoa(i)); e != nil {
			t.Fatal(e)
		}
	}
	if len(h.musicIDs) != 4096 {
		t.Fatal("unbounded aliases", len(h.musicIDs))
	}
	if h.isMusicStation("1001") {
		t.Fatal("radio station captured as music")
	}
}

func TestLiteralQuotesInMusicXML(t *testing.T) {
	h := newHandler(t)
	h.Music = &musicFixture{}
	r := httptest.NewRequest("GET", h.Base+"/setupapp/pure/asp/BrowseXML/Search.asp?sSearchtype=3&Search=upnp_fixture&mac=0123456789abcdef", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<StationName>Midnight Sun 'live' "mix"</StationName>`) || strings.Contains(body, "&#39;") || strings.Contains(body, "&#34;") {
		t.Fatal(body)
	}
	var response list
	if e := xml.Unmarshal(w.Body.Bytes(), &response); e != nil {
		t.Fatal("invalid XML", e)
	}
	if response.Items[1].Name != `Midnight Sun 'live' "mix"` {
		t.Fatal(response)
	}
}
