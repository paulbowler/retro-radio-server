// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/protocol/frontierxml"
	"retroradio.local/server/internal/store"
)

func queueFixture(t *testing.T, n int, kind string) (*Manager, string) {
	return queueFixtureAudio(t, n, kind, nil)
}
func queueFixtureAudio(t *testing.T, n int, kind string, input [][]byte) (*Manager, string) {
	t.Helper()
	var server *httptest.Server
	entry := func(i int) string {
		if kind == "folder" && i == n-1 {
			return container("nested", "album", "Subfolder")
		}
		album := "Album"
		if i%2 == 1 {
			album = "Different album"
		}
		if i%3 == 0 {
			album = ""
		}
		codec := "audio/mpeg"
		if kind == "flac" {
			codec = "audio/flac"
		}
		if kind == "mixed-codec" && i == n-1 {
			codec = "audio/aac"
		}
		return fmt.Sprintf(`<item id="track%d" parentID="album"><title>Track %d</title><artist>Artist</artist><album>%s</album><class>object.item.audioItem.musicTrack</class><res protocolInfo="http-get:*:%s:*" bitrate="16000" duration="0:00:01">%s/audio/%d</res></item>`, i, i+1, album, codec, server.URL, i)
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/description":
			fmt.Fprint(w, `<root><device><friendlyName>Music server</friendlyName><serviceList><service><serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType><controlURL>/control</controlURL></service></serviceList></device></root>`)
		case "/control":
			var request browseRequest
			if e := xml.NewDecoder(r.Body).Decode(&request); e != nil {
				t.Error(e)
				return
			}
			if request.Flag == "BrowseMetadata" {
				if strings.HasPrefix(request.Object, "track") {
					i, _ := strconv.Atoi(strings.TrimPrefix(request.Object, "track"))
					fmt.Fprint(w, soap(didlOpen+entry(i)+`</DIDL-Lite>`, 1, 1))
				} else {
					fmt.Fprint(w, soap(didlOpen+container("album", "0", "Any folder name")+`</DIDL-Lite>`, 1, 1))
				}
				return
			}
			var entries strings.Builder
			end := min(request.Start+request.Count, n)
			for i := request.Start; i < end; i++ {
				entries.WriteString(entry(i))
			}
			fmt.Fprint(w, soap(didlOpen+entries.String()+`</DIDL-Lite>`, end-request.Start, n))
		default:
			if !strings.HasPrefix(r.URL.Path, "/audio/") {
				http.NotFound(w, r)
				return
			}
			i, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/audio/"))
			// A leading ID3 file tag must be stripped at every track boundary.
			audio := append([]byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 1, 'x'}, bytes.Repeat([]byte{byte('A' + i)}, 5000)...)
			if input != nil {
				audio = input[i]
			}
			mime := "audio/mpeg"
			if kind == "flac" {
				mime = "audio/flac"
			}
			w.Header().Set("Content-Type", mime)
			w.Header().Set("Content-Length", strconv.Itoa(len(audio)))
			w.Write(audio)
		}
	}))
	t.Cleanup(server.Close)
	p, e := New(server.URL + "/description")
	if e != nil {
		t.Fatal(e)
	}
	p.allowLoopback = true
	m, _ := NewManager("")
	m.servers["fixture"] = musicServer{key: "fixture", provider: p, manual: true, seen: time.Now()}
	m.queueWait = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	return m, "fixture:album"
}

func splitQueueICY(t *testing.T, wire []byte) ([]byte, []string) {
	t.Helper()
	var audio bytes.Buffer
	var titles []string
	for len(wire) >= 4096 {
		audio.Write(wire[:4096])
		wire = wire[4096:]
		if len(wire) == 0 {
			t.Fatal("missing ICY length byte")
		}
		size := int(wire[0]) * 16
		if len(wire) < size+1 {
			t.Fatal("truncated ICY metadata")
		}
		titles = append(titles, strings.TrimRight(string(wire[1:size+1]), "\x00"))
		wire = wire[size+1:]
	}
	audio.Write(wire)
	return audio.Bytes(), titles
}

func TestContinuousQueueChangesMetadataAndDoesNotReplay(t *testing.T) {
	for _, icy := range []bool{false, true} {
		t.Run(fmt.Sprint(icy), func(t *testing.T) {
			m, folder := queueFixture(t, 2, "tracks")
			play, id, e := m.StartSequence(context.Background(), folder, "", model.LegacyXML)
			if e != nil || play.Item.Title != "Track 1" {
				t.Fatal(play, id, e)
			}
			var observed []string
			m.PlaybackObserver = func(r *http.Request, play content.Playback) func(bool) {
				if r.URL.Query().Get("radio") != "test-radio" {
					t.Error("radio attribution lost")
				}
				observed = append(observed, play.Item.Title)
				return func(complete bool) {
					if !complete {
						t.Error("incomplete track")
					}
				}
			}
			link := "/stream/upnp-queue/" + id + "?radio=test-radio"
			head := httptest.NewRecorder()
			m.ServeQueue(head, httptest.NewRequest("HEAD", link, nil))
			if head.Code != 200 || head.Body.Len() != 0 {
				t.Fatal(head)
			}
			r := httptest.NewRequest("GET", link, nil)
			if icy {
				r.Header.Set("Icy-MetaData", "1")
			}
			r.Header.Set("Range", "bytes=0-")
			w := httptest.NewRecorder()
			m.ServeQueue(w, r)
			if w.Code != 200 || w.Header().Get("Content-Length") != "" || w.Header().Get("Accept-Ranges") != "none" {
				t.Fatal(w.Code, w.Header())
			}
			audio := w.Body.Bytes()
			if icy {
				if w.Header().Get("icy-metaint") != "4096" {
					t.Fatal(w.Header())
				}
				var titles []string
				audio, titles = splitQueueICY(t, audio)
				if len(titles) != 2 || !strings.Contains(titles[0], "Artist - Track 1") || !strings.Contains(titles[1], "Artist - Track 2 (Different album)") {
					t.Fatal(titles)
				}
			}
			expected := append(bytes.Repeat([]byte{'A'}, 5000), bytes.Repeat([]byte{'B'}, 5000)...)
			if !bytes.Equal(audio, expected) || strings.Join(observed, ",") != "Track 1,Track 2" {
				t.Fatal("track order/tag stripping/tracking failed", len(audio), observed)
			}
			replay := httptest.NewRecorder()
			m.ServeQueue(replay, httptest.NewRequest("GET", link, nil))
			if replay.Code != 410 || strings.Contains(replay.Body.String(), "AAAA") {
				t.Fatal("completed queue replayed", replay.Code)
			}
			_, fresh, e := m.StartSequence(context.Background(), folder, "", model.LegacyXML)
			if e != nil || fresh == id {
				t.Fatal("new selection did not restart", fresh, e)
			}
		})
	}
}

func TestQueueTrackListPagesAndStartsAtSelection(t *testing.T) {
	m, folder := queueFixture(t, 103, "tracks")
	tracks, e := m.TrackList(context.Background(), folder)
	if e != nil || len(tracks) != 103 || tracks[100].Title != "Track 101" {
		t.Fatal(len(tracks), e)
	}
	play, id, e := m.StartSequence(context.Background(), folder, "fixture:track100", model.LegacyXML)
	if e != nil || play.Item.Title != "Track 101" || len(m.queues[id].tracks) != 3 {
		t.Fatal(play, e)
	}
	w := httptest.NewRecorder()
	m.ServeQueue(w, httptest.NewRequest("GET", "/stream/upnp-queue/"+id, nil))
	if w.Code != 200 || w.Body.Len() != 15000 {
		t.Fatal(w.Code, w.Body.Len())
	}
	for _, kind := range []string{"folder", "mixed-codec"} {
		bad, folder := queueFixture(t, 103, kind)
		_, _, e = bad.StartSequence(context.Background(), folder, "", model.LegacyXML)
		if e == nil {
			t.Fatal("unsupported folder accepted", kind)
		}
	}
	if _, _, e = m.StartSequence(context.Background(), folder, "gone", model.LegacyXML); e == nil {
		t.Fatal("missing start accepted")
	}
}

func TestPurePlayAllMenuAndContinuousStreamJourney(t *testing.T) {
	m, folder := queueFixture(t, 31, "tracks")
	db, e := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.DB.Close()
	h := &frontierxml.Handler{Store: db, Base: "http://radio.test", Music: m}
	base := h.Base + "/setupapp/pure/asp/BrowseXML/"
	type menu struct {
		Count int                `xml:"ItemCount"`
		Items []frontierxml.Item `xml:"Item"`
	}
	get := func(link string) menu {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", link+"&mac=0123456789abcdef", nil))
		var result menu
		if w.Code != 200 || xml.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return result
	}
	link := base + "navXML.asp?music=" + base64.RawURLEncoding.EncodeToString([]byte(folder))
	first := get(link + "&startItems=1&endItems=24")
	if first.Count != 32 || len(first.Items) != 25 || first.Items[1].Name != "[Play All]" || first.Items[24].Name != "Track 23" {
		t.Fatal(first)
	}
	second := get(link + "&startItems=25&endItems=32")
	if len(second.Items) != 9 || second.Items[1].Name != "Track 24" || second.Items[8].Name != "Track 31" {
		t.Fatal(second)
	}
	if only := get(link + "&startItems=1&endItems=1"); len(only.Items) != 2 {
		t.Fatal(only)
	}
	for _, selection := range []struct {
		id   string
		size int
	}{{first.Items[1].ID, 31 * 5000}, {first.Items[3].ID, 30 * 5000}} {
		lookup := get(base + "Search.asp?sSearchtype=3&Search=" + selection.id)
		if !strings.Contains(lookup.Items[1].URL, "/stream/upnp-queue/") || !strings.Contains(lookup.Items[1].URL, "?radio=") {
			t.Fatal(lookup)
		}
		w := httptest.NewRecorder()
		m.ServeQueue(w, httptest.NewRequest("GET", lookup.Items[1].URL, nil))
		if w.Code != 200 || w.Body.Len() != selection.size {
			t.Fatal(w.Code, w.Body.Len(), selection.size)
		}
	}
}

func TestQueueCancellationReleasesSessionAndRejectsConcurrentGET(t *testing.T) {
	m, folder := queueFixture(t, 2, "tracks")
	_, id, e := m.StartSequence(context.Background(), folder, "", model.LegacyXML)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	var once sync.Once
	m.queueWait = func(ctx context.Context, _ time.Duration) error {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.ServeQueue(httptest.NewRecorder(), httptest.NewRequest("GET", "/stream/upnp-queue/"+id, nil).WithContext(ctx))
	}()
	<-entered
	w := httptest.NewRecorder()
	m.ServeQueue(w, httptest.NewRequest("GET", "/stream/upnp-queue/"+id, nil))
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	cancel()
	<-done
	if m.queues[id].active || m.queues[id].next != 0 || len(m.queueSlots) != 0 {
		t.Fatal("cancelled session lost current track or leaked slot")
	}
	m.queueWait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	w = httptest.NewRecorder()
	m.ServeQueue(w, httptest.NewRequest("GET", "/stream/upnp-queue/"+id, nil))
	if w.Code != 200 || w.Body.Len() != 10000 {
		t.Fatal(w.Code, w.Body.Len())
	}
	if e := waitQueue(ctx, time.Hour); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestQueueMethodRangeExpiryAndUnknownID(t *testing.T) {
	m, folder := queueFixture(t, 1, "tracks")
	_, id, e := m.StartSequence(context.Background(), folder, "", model.LegacyXML)
	if e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct {
		method, path, rangeHeader string
		status                    int
	}{
		{"POST", id, "", 405}, {"GET", "unknown", "", 404}, {"GET", id, "bytes=3-", 416},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(test.method, "/stream/upnp-queue/"+test.path, nil)
		r.Header.Set("Range", test.rangeHeader)
		m.ServeQueue(w, r)
		if w.Code != test.status {
			t.Fatal(w.Code, test)
		}
	}
	m.queues[id].seen = time.Now().Add(-queueLifetime - time.Second)
	w := httptest.NewRecorder()
	m.ServeQueue(w, httptest.NewRequest("GET", "/stream/upnp-queue/"+id, nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestGeneratedContinuousMP3AndFLACQueuesDecode(t *testing.T) {
	ffmpeg, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("FFmpeg required for generated audio queue test")
	}
	for _, kind := range []string{"mp3", "flac"} {
		t.Run(kind, func(t *testing.T) {
			var inputs [][]byte
			for _, frequency := range []string{"440", "880"} {
				encoder := []string{"-c:a", "flac", "-f", "flac"}
				if kind == "mp3" {
					encoder = []string{"-c:a", "libmp3lame", "-b:a", "128k", "-f", "mp3"}
				}
				args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=" + frequency + ":sample_rate=44100", "-t", "1", "-ac", "2"}
				args = append(args, encoder...)
				args = append(args, "pipe:1")
				input, e := exec.Command(ffmpeg, args...).Output()
				if e != nil {
					t.Fatal(e)
				}
				inputs = append(inputs, input)
			}
			m, folder := queueFixtureAudio(t, 2, kind, inputs)
			_, id, e := m.StartSequence(context.Background(), folder, "", model.LegacyXML)
			if e != nil {
				t.Fatal(e)
			}
			r := httptest.NewRequest("GET", "/stream/upnp-queue/"+id, nil)
			r.Header.Set("Icy-MetaData", "1")
			w := httptest.NewRecorder()
			m.ServeQueue(w, r)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			audio, titles := splitQueueICY(t, w.Body.Bytes())
			hasSecond := false
			for _, title := range titles {
				if strings.Contains(title, "Track 2") {
					hasSecond = true
				}
			}
			if !hasSecond {
				t.Fatal("metadata did not advance", titles)
			}
			decode := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "mp3", "-i", "pipe:0", "-f", "s16le", "pipe:1")
			decode.Stdin = bytes.NewReader(audio)
			pcm, e := decode.Output()
			if e != nil || len(pcm) < 2*44100*2*2 {
				t.Fatal("continuous MP3 did not decode both tracks", len(pcm), e)
			}
			if len(m.servers["fixture"].provider.conversionSlots) != 0 {
				t.Fatal("conversion slot leaked")
			}
		})
	}
}

func TestQueueICYArtworkChangesClearsAndKeepsAudio(t *testing.T) {
	var wire bytes.Buffer
	writer := queueICY{w: &wire, remaining: 4096, enabled: true, title: "First", artwork: "http://radio.test/artwork/upnp/first.jpg"}
	writer.Write(bytes.Repeat([]byte{'A'}, 4096))
	writer.title = "Second"
	writer.artwork = "http://radio.test/artwork/upnp/second.jpg"
	writer.Write(bytes.Repeat([]byte{'B'}, 4096))
	writer.title = "No artwork"
	writer.artwork = ""
	writer.Write(bytes.Repeat([]byte{'C'}, 4096))
	audio, meta := splitQueueICY(t, wire.Bytes())
	if len(audio) != 3*4096 || len(meta) != 3 || !strings.Contains(meta[0], "StreamUrl='http://radio.test/artwork/upnp/first.jpg';") || !strings.Contains(meta[1], "StreamUrl='http://radio.test/artwork/upnp/second.jpg';") || !strings.Contains(meta[2], "StreamUrl='';") {
		t.Fatal(meta)
	}
	m := &Manager{ArtworkBase: "http://radio.test/"}
	if got := m.queueArtwork(content.Item{PlaybackID: "safe", ArtURL: "http://nas/cover"}); got != "http://radio.test/artwork/upnp/safe.jpg" {
		t.Fatal(got)
	}
	if m.queueArtwork(content.Item{PlaybackID: "safe"}) != "" {
		t.Fatal("missing artwork not cleared")
	}
}
func TestQueueICYArtworkMetadataBoundedAndEscaped(t *testing.T) {
	var wire bytes.Buffer
	writer := queueICY{w: &wire, remaining: 4096, enabled: true, title: strings.Repeat("é", 4000), artwork: "http://radio.test/x';StreamTitle='bad\r\n.jpg"}
	writer.Write(bytes.Repeat([]byte{'A'}, 4096))
	_, meta := splitQueueICY(t, wire.Bytes())
	if len(meta[0]) > 4080 || strings.Contains(meta[0], "StreamTitle='bad") || strings.ContainsAny(meta[0], "\r\n") || !strings.Contains(meta[0], "%27%3B") {
		t.Fatal("invalid artwork metadata")
	}
}
