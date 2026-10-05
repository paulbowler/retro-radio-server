// SPDX-License-Identifier: GPL-3.0-only
package frontierxml

import (
	"encoding/json"
	"encoding/xml"
	"net/http/httptest"
	"os"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strconv"
	"strings"
	"testing"
)

func newHandler(t *testing.T) *Handler {
	t.Helper()
	s, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Close() })
	return &Handler{Store: s, Base: "http://192.168.1.20"}
}
func TestReferenceTransactions(t *testing.T) {
	h := newHandler(t)
	data, e := os.ReadFile("testdata/requests.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixtures []struct {
		Name, Path string
		Count      int
	}
	if e = json.Unmarshal(data, &fixtures); e != nil {
		t.Fatal(e)
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", f.Path, nil))
			if w.Code != 200 {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) {
				t.Fatal("incorrect length")
			}
			if strings.Contains(w.Body.String(), "https:") {
				t.Fatal("legacy radio received HTTPS")
			}
			if f.Count == -2 {
				if w.Body.String() != Challenge {
					t.Fatal(w.Body.String())
				}
				return
			}
			var l list
			if e := xml.Unmarshal(w.Body.Bytes(), &l); e != nil {
				t.Fatal(e)
			}
			if l.Count != f.Count {
				t.Fatalf("count %d", l.Count)
			}
			if l.Items[0].Type != "Previous" {
				t.Fatal("no Previous")
			}
			if f.Name == "root" {
				for _, i := range l.Items {
					if i.Type == "Dir" && !strings.Contains(i.Dir, "?") {
						t.Fatal("missing query delimiter for radio appended parameters")
					}
				}
			}
			if f.Name == "station_lookup" {
				i := l.Items[1]
				if i.Mime != "MP3" || i.Bitrate != 128 || !strings.HasPrefix(i.URL, h.Base+"/stream/") {
					t.Fatalf("bad station %#v", i)
				}
				if strings.Contains(i.URL, "SmoothUK") {
					t.Fatal("upstream leaked")
				}
			}
		})
	}
	ds, e := h.Store.Devices()
	if e != nil || len(ds) != 1 {
		t.Fatalf("devices %v %v", ds, e)
	}
	events, e := h.Store.Events()
	if e != nil {
		t.Fatal(e)
	}
	for _, e := range events {
		if strings.Contains(e.Detail, "0123456789") {
			t.Fatal("token logged")
		}
	}
}
func TestFollowAdvertisedURLs(t *testing.T) {
	h := newHandler(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/setupapp/pure/asp/BrowseXML/loginXML.asp?gofile=&mac=0123456789abcdef", nil))
	var root list
	xml.Unmarshal(w.Body.Bytes(), &root)
	u := root.Items[1].Dir + "&mac=0123456789abcdef"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", u, nil))
	var menu list
	if e := xml.Unmarshal(w.Body.Bytes(), &menu); e != nil {
		t.Fatal(e)
	}
	if menu.Count != 5 || menu.Items[1].Title != "All stations" || !strings.Contains(menu.Items[0].Previous, "loginXML.asp") {
		t.Fatal("invalid Radio menu", menu)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", menu.Items[1].Dir+"&mac=0123456789abcdef", nil))
	var stations list
	if e := xml.Unmarshal(w.Body.Bytes(), &stations); e != nil {
		t.Fatal(e)
	}
	if len(stations.Items) != 2 || stations.Items[1].ID != "1001" {
		t.Fatalf("%s", w.Body.String())
	}
}
func TestFavouritesAndPagination(t *testing.T) {
	h := newHandler(t)
	token := "0123456789abcdef"
	d, e := h.Store.Seen(token, "pure", "8", "192.168.1.99")
	if e != nil {
		t.Fatal(e)
	}
	if e = h.Store.Favourite(d.ID, "1001", true); e != nil {
		t.Fatal(e)
	}
	for _, ep := range []string{"FavXML.asp?empty=", "navXML.asp?gofile=Radio&startItems=2&endItems=100"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/setupapp/pure/asp/BrowseXML/"+ep+"&mac="+token, nil))
		var l list
		if e = xml.Unmarshal(w.Body.Bytes(), &l); e != nil {
			t.Fatal(e)
		}
		if l.Count != 1 {
			t.Fatal("count should describe full list")
		}
		if strings.Contains(ep, "startItems=2") && len(l.Items) != 1 {
			t.Fatal("pagination ignored")
		}
	}
}
func TestRejectUnknownAndUnauthenticated(t *testing.T) {
	h := newHandler(t)
	for _, p := range []string{"/setupapp/pure/asp/BrowseXML/navXML.asp?mac=x", "/setupapp/pure/asp/BrowseXML/nope.asp?mac=0123456789abcdef"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code < 400 {
			t.Fatal("accepted invalid request")
		}
	}
}
func TestBaseURL(t *testing.T) {
	for _, bad := range []string{"https://radio.local", "http://radio.local/path", "http://radio.local?x=y", "http://admin:password@radio.local"} {
		if ValidateBase(bad) == nil {
			t.Fatal(bad)
		}
	}
}

func TestExpectedResponseFixtures(t *testing.T) {
	h := newHandler(t)
	station, e := h.Store.Station("1001", false)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct{ fixture, path string }{{"root.xml", "loginXML.asp?gofile=&mac=synthetic-fixture"}, {"station.xml", "Search.asp?sSearchtype=3&Search=1001&mac=synthetic-fixture"}} {
		expected, e := os.ReadFile("testdata/" + c.fixture)
		if e != nil {
			t.Fatal(e)
		}
		want := strings.ReplaceAll(strings.TrimSpace(string(expected)), "{STREAM_ID}", station.StreamID)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/setupapp/pure/asp/BrowseXML/"+c.path, nil))
		device, _ := h.Store.Seen("synthetic-fixture", "pure", "", "192.0.2.1")
		want = strings.ReplaceAll(want, "{RADIO_ID}", device.ID)
		if w.Body.String() != want {
			t.Fatalf("response differs from %s\n%s", c.fixture, w.Body.String())
		}
	}
}

func TestSharedLibraryMetadataMenus(t *testing.T) {
	h := newHandler(t)
	first, _ := h.Store.Seen("first-radio", "pure", "8", "127.0.0.1")
	a, e := h.Store.SaveStation(model.Station{Name: "Jazz & Café", URL: "https://1.1.1.1/audio", Codec: "MP3", Country: "France", Tags: "Jazz, Soul, jazz"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := h.Store.SaveStation(model.Station{Name: "Soul FM", URL: "https://1.1.1.1/audio", Codec: "MP3", Country: "Hungary", Tags: "soul"})
	if e != nil {
		t.Fatal(e)
	}
	if e = h.Store.Favourite(first.ID, a.ID, true); e != nil {
		t.Fatal(e)
	}
	read := func(path, token string) list {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path+"&mac="+token, nil))
		var l list
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if e := xml.Unmarshal(w.Body.Bytes(), &l); e != nil {
			t.Fatal(e)
		}
		return l
	}
	base := "/setupapp/pure/asp/BrowseXML/"
	for _, token := range []string{"first-radio", "future-radio"} {
		l := read(base+"navXML.asp?gofile=Radio", token)
		if l.Count != 3 {
			t.Fatal("shared library missing stations", l)
		}
		f := read(base+"FavXML.asp?empty=", token)
		want := 0
		if token == "first-radio" {
			want = 1
		}
		if f.Count != want {
			t.Fatal("favourites leaked", f)
		}
	}
	countries := read(base+"navXML.asp?group=country", "first-radio")
	if countries.Count != 3 {
		t.Fatal(countries)
	}
	var france string
	for _, item := range countries.Items {
		if item.Title == "France" {
			france = item.Dir
		}
	}
	french := read(france, "future-radio")
	if french.Count != 1 || french.Items[1].ID != a.ID {
		t.Fatal(french)
	}
	if !strings.Contains(french.Items[0].Previous, "group=country") {
		t.Fatal("wrong parent", french)
	}
	genres := read(base+"navXML.asp?group=genre", "first-radio")
	if genres.Count != 3 {
		t.Fatal("duplicate or missing genre", genres)
	}
	soul := read(base+"navXML.asp?group=genre&value=soul&startItems=2&endItems=2", "future-radio")
	if soul.Count != 2 || len(soul.Items) != 2 || soul.Items[1].ID != b.ID {
		t.Fatal("genre pagination", soul)
	}
	if e = h.Store.DeleteStation(a.ID); e != nil {
		t.Fatal(e)
	}
	jazz := read(base+"navXML.asp?group=genre&value=jazz", "first-radio")
	if jazz.Count != 0 {
		t.Fatal("deleted metadata persisted", jazz)
	}
}

func TestAACTrialAdvertisedAndPlayable(t *testing.T) {
	h := newHandler(t)
	s, e := h.Store.SaveStation(model.Station{Name: "AAC trial", URL: "https://1.1.1.1/audio", Codec: "AAC"})
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"navXML.asp?gofile=Radio", "Search.asp?sSearchtype=3&Search=" + s.ID} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/setupapp/pure/asp/BrowseXML/"+path+"&mac=aac-trial", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), s.Name) {
			t.Fatal("AAC hidden", w.Code, w.Body.String())
		}
		if strings.HasPrefix(path, "Search") && (!strings.Contains(w.Body.String(), "/stream/"+s.StreamID) || !strings.Contains(w.Body.String(), "<StationMime>AAC</StationMime>")) {
			t.Fatal("AAC lookup failed", w.Body.String())
		}
	}
}
