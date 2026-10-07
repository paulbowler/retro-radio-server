// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"strings"
	"sync"
	"testing"
	"time"
)

func soap(didl string, returned, total int) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(didl))
	return fmt.Sprintf(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><Result>%s</Result><NumberReturned>%d</NumberReturned><TotalMatches>%d</TotalMatches><UpdateID>9</UpdateID></u:BrowseResponse></s:Body></s:Envelope>`, b.String(), returned, total)
}
func container(id, parent, title string) string {
	return `<container id="` + id + `" parentID="` + parent + `"><dc:title xmlns:dc="http://purl.org/dc/elements/1.1/">` + title + `</dc:title><upnp:class xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">object.container</upnp:class></container>`
}
func track(base string) string {
	return `<item id="track&amp;1" parentID="album"><dc:title xmlns:dc="http://purl.org/dc/elements/1.1/">So What &amp; More</dc:title><upnp:artist xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">Miles Davis</upnp:artist><upnp:album xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">Kind of Blue</upnp:album><upnp:albumArtURI xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` + base + `/cover</upnp:albumArtURI><upnp:class xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">object.item.audioItem.musicTrack</upnp:class><res protocolInfo="http-get:*:audio/flac:*">` + base + `/flac</res><res protocolInfo="http-get:*:audio/mpeg:*" bitrate="16000" duration="0:09:22">` + base + `/audio</res><res protocolInfo="http-get:*:audio/aac:*">` + base + `/aac</res></item>`
}

const didlOpen = `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/">`

type browseRequest struct {
	Object string `xml:"Body>Browse>ObjectID"`
	Flag   string `xml:"Body>Browse>BrowseFlag"`
	Start  int    `xml:"Body>Browse>StartingIndex"`
	Count  int    `xml:"Body>Browse>RequestedCount"`
}

func fixture(t *testing.T) (*Provider, *httptest.Server, *[]browseRequest, *sync.Mutex) {
	t.Helper()
	requests := []browseRequest{}
	mu := &sync.Mutex{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/description.xml":
			fmt.Fprint(w, `<root><device><friendlyName>MinimServer [NAS]</friendlyName><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></root>`)
		case "/control":
			if r.Method != "POST" || r.Header.Get("SOAPAction") != `"urn:schemas-upnp-org:service:ContentDirectory:1#Browse"` {
				t.Error("bad SOAP request")
			}
			var req browseRequest
			if e := xml.NewDecoder(r.Body).Decode(&req); e != nil {
				t.Error(e)
			}
			mu.Lock()
			requests = append(requests, req)
			mu.Unlock()
			if req.Flag == "BrowseMetadata" {
				if req.Object == "track&1" {
					fmt.Fprint(w, soap(didlOpen+track(server.URL)+`</DIDL-Lite>`, 1, 1))
				} else {
					fmt.Fprint(w, soap(didlOpen+container(req.Object, "0", "Album")+`</DIDL-Lite>`, 1, 1))
				}
			} else if req.Count == 1 {
				fmt.Fprint(w, soap(didlOpen+container("album", "0", "Kind of Blue")+`</DIDL-Lite>`, 1, 1000))
			} else {
				fmt.Fprint(w, soap(didlOpen+container("album", "0", "Kind of Blue")+track(server.URL)+`</DIDL-Lite>`, 2, 1000))
			}
		case "/cover":
			w.Header().Set("Content-Type", "image/png")
			png.Encode(w, image.NewRGBA(image.Rect(0, 0, 320, 160)))
		case "/audio":
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Header().Set("Accept-Ranges", "bytes")
			if r.Header.Get("Range") == "bytes=3-5" {
				w.Header().Set("Content-Range", "bytes 3-5/10")
				w.Header().Set("Content-Length", "3")
				w.WriteHeader(206)
				if r.Method != "HEAD" {
					fmt.Fprint(w, "345")
				}
			} else {
				w.Header().Set("Content-Length", "10")
				if r.Method != "HEAD" {
					fmt.Fprint(w, "0123456789")
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p, e := New(server.URL + "/description.xml")
	if e != nil {
		t.Fatal(e)
	}
	p.allowLoopback = true
	return p, server, &requests, mu
}
func TestBrowseParsePaginationAndResolve(t *testing.T) {
	p, _, requests, mu := fixture(t)
	page, e := p.Browse(context.Background(), "0", 48, 24)
	if e != nil {
		t.Fatal(e)
	}
	if page.Total != 1000 || page.Returned != 2 || page.UpdateID != "9" || len(page.Items) != 2 || page.Items[0].Kind != content.Folder || page.Items[1].Title != "So What & More" || p.Name() != "MinimServer [NAS]" {
		t.Fatal(page)
	}
	mu.Lock()
	first := (*requests)[0]
	mu.Unlock()
	if first.Start != 48 || first.Count != 24 || first.Flag != "BrowseDirectChildren" {
		t.Fatal(first)
	}
	play, e := p.Resolve(context.Background(), page.Items[1].PlaybackID, model.LegacyXML)
	if e != nil {
		t.Fatal(e)
	}
	if play.Item.Artist != "Miles Davis" || play.Item.Album != "Kind of Blue" || play.Item.Duration != "0:09:22" || play.Resource.Codec != "MP3" || play.Resource.Bitrate != 128 || play.Direct {
		t.Fatal(play)
	}
	mu.Lock()
	last := (*requests)[len(*requests)-1]
	mu.Unlock()
	if last.Object != "track&1" || last.Flag != "BrowseMetadata" {
		t.Fatal(last)
	}
	if _, e = p.Resolve(context.Background(), "made-up", model.LegacyXML); e != ErrUnknownID {
		t.Fatal(e)
	}
	page, e = p.Browse(context.Background(), "album", 0, 24)
	if e != nil || page.ParentID != "0" {
		t.Fatal(page, e)
	}
}
func TestResourceSelection(t *testing.T) {
	p, _, _, _ := fixture(t)
	p.DisableTranscode = true
	if _, e := p.Browse(context.Background(), "0", 0, 24); e != nil {
		t.Fatal(e)
	}
	resources := []content.Resource{{URL: "http://evil.example/audio", Protocol: "http-get", Codec: "MP3"}, {URL: "http://127.0.0.1/audio", Protocol: "rtsp", Codec: "MP3"}, {URL: "http://127.0.0.1/a.flac", Protocol: "http-get", Codec: "FLAC"}, {URL: "https://127.0.0.1/audio", Protocol: "http-get", Codec: "AAC", MIME: "audio/aac"}}
	r, e := p.SelectResource(resources, model.LegacyXML)
	if e != nil || r.Codec != "AAC" {
		t.Fatal(r, e)
	}
	if _, e = p.SelectResource(resources[:3], model.LegacyXML); e == nil {
		t.Fatal("unsafe/unsupported resources selected")
	}
	if _, e = p.SelectResource(resources, model.Capabilities{HTTP: true}); e == nil {
		t.Fatal("unsupported profile selected")
	}
}
func TestProxyRangeHeadAndOpaqueIDs(t *testing.T) {
	p, _, _, _ := fixture(t)
	page, e := p.Browse(context.Background(), "0", 0, 24)
	if e != nil {
		t.Fatal(e)
	}
	id := page.Items[1].PlaybackID
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, "/stream/upnp/"+id, nil)
		r.Header.Set("Range", "bytes=3-5")
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != 206 || w.Header().Get("Content-Range") != "bytes 3-5/10" || w.Header().Get("Content-Type") != "audio/mpeg" {
			t.Fatal(w.Code, w.Header(), w.Body)
		}
		if method == "GET" && w.Body.String() != "345" {
			t.Fatal(w.Body)
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD body")
		}
	}
	for _, path := range []string{"/stream/upnp/missing?url=http://127.0.0.1/secret", "/stream/upnp/https://evil.example", "/stream/upnp/" + id + "/extra"} {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("POST", "/stream/upnp/"+id, nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	p.mu.Lock()
	p.tokens[id] = token{object: "track&1", at: time.Now().Add(-25 * time.Hour)}
	p.mu.Unlock()
	w = httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "/stream/upnp/"+id, nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestMalformedDIDLAndMixedOrder(t *testing.T) {
	for _, raw := range []string{`<bad/>`, `<DIDL-Lite>`, `<DIDL-Lite><item id="a"/></DIDL-Lite>`, `<DIDL-Lite></DIDL-Lite><evil/>`} {
		if _, e := parseDIDL(raw); e == nil {
			t.Fatal(raw)
		}
	}
	items, e := parseDIDL(didlOpen + track("http://music.home") + container("folder", "0", "Folder") + `</DIDL-Lite>`)
	if e != nil || len(items) != 2 || items[0].Kind != content.PlayableItem || items[1].Kind != content.Folder || len(items[0].Resources) != 3 {
		t.Fatal(items, e)
	}
}
func TestUnavailableAndMalformedSOAP(t *testing.T) {
	for _, body := range []string{`<bad/>`, `<Envelope><Body><Fault/></Body></Envelope>`, soap(`<wrong/>`, 0, 0), soap(didlOpen+`</DIDL-Lite>`, 1, 1)} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "GET" {
				fmt.Fprint(w, `<root><device><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></root>`)
			} else {
				fmt.Fprint(w, body)
			}
		}))
		p, _ := New(s.URL)
		p.allowLoopback = true
		_, e := p.Browse(context.Background(), "0", 0, 24)
		s.Close()
		if e == nil {
			t.Fatal(body)
		}
	}
	p, s, _, _ := fixture(t)
	s.Close()
	if _, e := p.Browse(context.Background(), "0", 0, 24); e == nil {
		t.Fatal("unavailable accepted")
	}
	p, _, _, _ = fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := p.Browse(ctx, "0", 0, 24); e == nil {
		t.Fatal("cancelled browse accepted")
	}
}
func TestLANScope(t *testing.T) {
	for _, raw := range []string{"file:///music", "http://user:pass@music.home/x", "http://music.home:22/x", "http://music.home/x#fragment"} {
		if _, e := New(raw); e == nil {
			t.Fatal(raw)
		}
	}
	u, _ := url.Parse("http://127.0.0.1:9790/description.xml")
	if _, e := newScope(context.Background(), u, false); e == nil {
		t.Fatal("loopback allowed in production")
	}
	p, _, _, _ := fixture(t)
	if _, e := p.Browse(context.Background(), "0", 0, 24); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"http://169.254.169.254/", "http://192.168.1.99/audio", "http://evil.example/audio", "http://127.0.0.1:22/audio"} {
		if e := p.scope.check(raw); e == nil {
			t.Fatal(raw)
		}
	}
	// Redirects are checked even when they originate from the trusted server.
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://evil.example/audio", 302) }))
	defer redirect.Close()
	res, e := p.client.Get(redirect.URL)
	if res != nil {
		res.Body.Close()
	}
	if e == nil {
		t.Fatal("cross-host redirect accepted")
	}
}
func TestTokenBoundsAndConcurrentBrowse(t *testing.T) {
	p, _, _, _ := fixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := p.Browse(context.Background(), "0", 0, 24); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	for i := 0; i < 4100; i++ {
		p.register(fmt.Sprint(i))
	}
	if len(p.tokens) > 4096 || len(p.objects) > 4096 {
		t.Fatal("unbounded tokens")
	}
}
func TestProxyFailureAndRedirect(t *testing.T) {
	p, s, _, _ := fixture(t)
	page, e := p.Browse(context.Background(), "0", 0, 24)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "/stream/upnp/"+page.Items[1].PlaybackID, nil))
	if w.Code != 502 {
		t.Fatal(w.Code)
	}
}

func TestArtworkOpaqueJPEGAndHEAD(t *testing.T) {
	p, _, _, _ := fixture(t)
	page, e := p.Browse(context.Background(), "0", 0, 24)
	if e != nil {
		t.Fatal(e)
	}
	path := "/artwork/upnp/" + page.Items[1].PlaybackID + ".jpg"
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		p.ServeArtwork(w, httptest.NewRequest(method, path, nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" {
			t.Fatal(w.Code, w.Body)
		}
		if method == "GET" {
			img, e := jpeg.Decode(bytes.NewReader(w.Body.Bytes()))
			if e != nil || img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 {
				t.Fatal(img, e)
			}
		} else if w.Body.Len() != 0 {
			t.Fatal("HEAD body")
		}
	}
	w := httptest.NewRecorder()
	p.ServeArtwork(w, httptest.NewRequest("GET", "/artwork/upnp/arbitrary.jpg?url=http://evil.example", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

// Some clients disconnect immediately after consuming the advertised length.
// A source read can observe that cancellation while also returning its final bytes.
type finalCancelledRead struct{ done bool }

func (r *finalCancelledRead) Read(b []byte) (int, error) {
	if r.done {
		return 0, context.Canceled
	}
	r.done = true
	return copy(b, "0123456789"), context.Canceled
}
func (r *finalCancelledRead) Close() error { return nil }
func TestMusicFinalBytesBeforeCancellationCountAsCompleted(t *testing.T) {
	p, _, _, _ := fixture(t)
	page, e := p.Browse(context.Background(), "0", 0, 24)
	if e != nil {
		t.Fatal(e)
	}
	original := p.client.Transport
	p.client.Transport = roundTripMusic(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/audio" {
			return original.RoundTrip(r)
		}
		return &http.Response{StatusCode: 200, ContentLength: 10, Header: http.Header{"Content-Type": {"audio/mpeg"}, "Content-Length": {"10"}}, Body: &finalCancelledRead{}, Request: r}, nil
	})
	started, completed := false, false
	p.PlaybackObserver = func(*http.Request, content.Playback) func(bool) {
		started = true
		return func(ok bool) { completed = ok }
	}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "/stream/upnp/"+page.Items[1].PlaybackID, nil))
	if w.Body.String() != "0123456789" || !started || !completed {
		t.Fatal(w.Body, started, completed)
	}
}

type roundTripMusic func(*http.Request) (*http.Response, error)

func (f roundTripMusic) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDIDLPerformerCredits(t *testing.T) {
	for _, tc := range []struct{ name, tags, artist, composer string }{
		{"performer after composer", `<artist role="Composer">Jim Steinman</artist><artist role="Performer">Bonnie Tyler</artist>`, "Bonnie Tyler", "Jim Steinman"},
		{"composer after performer", `<artist role="Performer">Bonnie Tyler</artist><artist role="Composer">Jim Steinman</artist>`, "Bonnie Tyler", "Jim Steinman"},
		{"unqualified performer", `<artist>Bonnie Tyler</artist><artist role="Songwriter">Jim Steinman</artist>`, "Bonnie Tyler", "Jim Steinman"},
		{"explicit performer wins", `<artist role="AlbumArtist">Various Artists</artist><artist>Jim Steinman</artist><artist role="Performer">Bonnie Tyler</artist>`, "Bonnie Tyler", ""},
		{"multiple performers", `<artist role="Performer">Singer A</artist><artist role="Performer">Singer B</artist><artist role="Performer">Singer A</artist>`, "Singer A, Singer B", ""},
		{"cover retained", `<artist role="Composer">Jim Steinman</artist><artist role="Performer">Cover Band</artist>`, "Cover Band", "Jim Steinman"},
		{"composer only", `<creator>Jim Steinman</creator><artist role="Composer">Jim Steinman</artist>`, "", "Jim Steinman"},
		{"track artist over compilation", `<artist role="AlbumArtist">Various Artists</artist><artist>Bonnie Tyler</artist>`, "Bonnie Tyler", ""},
		{"author composer", `<author role="Composer">Jim Steinman</author><artist>Bonnie Tyler</artist>`, "Bonnie Tyler", "Jim Steinman"},
		{"legacy creator", `<creator>Legacy Artist</creator>`, "Legacy Artist", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := didlOpen + `<item id="song"><title>Total Eclipse of the Heart</title><class>object.item.audioItem.musicTrack</class>` + tc.tags + `<genre>Pop</genre><genre>Rock</genre><date>1983-01-01</date></item></DIDL-Lite>`
			items, err := parseDIDL(raw)
			if err != nil || len(items) != 1 {
				t.Fatal(items, err)
			}
			item := items[0]
			if item.Artist != tc.artist || item.Composer != tc.composer || item.GenreName != "Pop, Rock" || item.Date != "1983-01-01" {
				t.Fatal(item)
			}
			if tc.artist != "" && !strings.HasPrefix(queueTitle(item), tc.artist+" - ") {
				t.Fatal("display did not use performer", queueTitle(item))
			}
		})
	}
}
