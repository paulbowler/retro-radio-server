// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"retroradio.local/server/internal/agentfm"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/protocol/frontierxml"
	"retroradio.local/server/internal/store"
)

type agentServiceFunc func(context.Context, agentfm.Track, []agentfm.Track) (agentfm.Segment, error)

func (f agentServiceFunc) Prepare(ctx context.Context, t agentfm.Track, c []agentfm.Track) (agentfm.Segment, error) {
	return f(ctx, t, c)
}
func fakeAgentFFmpeg(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if e := os.WriteFile(path, []byte("#!/bin/sh\nexec /bin/cat\n"), 0700); e != nil {
		t.Fatal(e)
	}
	return path
}
func TestAgentFMTopLevelMenuAndGlobalLibrary(t *testing.T) {
	m, folder := queueFixture(t, 3, "tracks")
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
	get := func(path string) menu {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", base+path+"&mac=0123456789abcdef", nil))
		var result menu
		if w.Code != 200 || xml.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return result
	}
	disabled := get("loginXML.asp?gofile=")
	for _, item := range disabled.Items {
		if item.Name == "Agent FM" {
			t.Fatal("shown without configuration")
		}
	}
	m.agent = agentServiceFunc(func(context.Context, agentfm.Track, []agentfm.Track) (agentfm.Segment, error) {
		t.Error("API called by navigation or HEAD")
		return agentfm.Segment{}, nil
	})
	m.agentFFmpeg = fakeAgentFFmpeg(t)
	root := get("loginXML.asp?gofile=")
	if root.Count != 4 || len(root.Items) != 5 || root.Items[4].Name != "Agent FM" || root.Items[4].Type != "Station" {
		t.Fatal(root)
	}
	album := get("navXML.asp?music=" + base64.RawURLEncoding.EncodeToString([]byte(folder)))
	if album.Count != 4 || album.Items[1].Name != "[Play All]" {
		t.Fatal(album)
	}
	for _, i := range album.Items {
		if i.Name == "Agent FM" {
			t.Fatal("Agent FM inside album")
		}
	}
	lookup := get("Search.asp?sSearchtype=3&Search=" + root.Items[4].ID)
	item := lookup.Items[1]
	if item.Name != "Agent FM" || item.Mime != "MP3" || item.Bitrate != 128 || !strings.Contains(item.URL, "/stream/upnp-queue/") || !strings.Contains(item.URL, "?radio=") {
		t.Fatal(item)
	}
	if item.Logo == nil || *item.Logo != h.Base+"/artwork/agent-fm.jpg" {
		t.Fatal("Agent FM did not use the fixed station image", item)
	}
	id := strings.Split(strings.Split(item.URL, "/stream/upnp-queue/")[1], "?")[0]
	if len(m.queues[id].tracks) != 3 {
		t.Fatal("did not gather global tracks")
	}
	w := httptest.NewRecorder()
	m.ServeQueue(w, httptest.NewRequest("HEAD", item.URL, nil))
	if w.Code != 200 || w.Body.Len() != 0 || m.queues[id].next != 0 {
		t.Fatal("HEAD consumed playback")
	}
}
func TestAgentFMStreamChoiceSpeechMetadataAndStop(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "speech", true: "API failure"}[fail], func(t *testing.T) {
			m, folder := queueFixture(t, 3, "tracks")
			tracks, e := m.TrackList(context.Background(), folder)
			if e != nil {
				t.Fatal(e)
			}
			m.agentFFmpeg = fakeAgentFFmpeg(t)
			m.agent = agentServiceFunc(func(ctx context.Context, current agentfm.Track, c []agentfm.Track) (agentfm.Segment, error) {
				if fail {
					return agentfm.Segment{}, errors.New("simulated unavailable API")
				}
				index := 0
				want := "Track 3"
				if current.Title == "Track 3" {
					want = "Track 2"
				}
				for i, v := range c {
					if v.Title == want {
						index = i
					}
				}
				return agentfm.Segment{Index: index, Audio: bytes.Repeat([]byte{'S'}, 5000)}, nil
			})
			_, id, e := m.createQueue(context.Background(), tracks, model.LegacyXML, true)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var observed []string
			m.queueWait = waitQueue
			m.PlaybackObserver = func(r *http.Request, p content.Playback) func(bool) {
				if r.URL.Query().Get("radio") != "test" || p.Codec() != "MP3" || p.Bitrate() != 128 {
					t.Error("attribution/output profile lost")
				}
				observed = append(observed, p.Item.Title)
				return func(complete bool) {
					if len(observed) == 3 {
						cancel()
					}
				}
			}
			r := httptest.NewRequest("GET", "/stream/upnp-queue/"+id+"?radio=test", nil).WithContext(ctx)
			r.Header.Set("Icy-MetaData", "1")
			w := httptest.NewRecorder()
			m.ServeQueue(w, r)
			if w.Code != 200 || len(observed) != 3 {
				t.Fatal(w.Code, observed)
			}
			audio, titles := splitQueueICY(t, w.Body.Bytes())
			if fail {
				if bytes.Contains(audio, []byte("SSSS")) {
					t.Fatal("speech on failed API")
				}
			} else {
				expected := append(bytes.Repeat([]byte{'A'}, 5000), bytes.Repeat([]byte{'S'}, 5000)...)
				expected = append(expected, bytes.Repeat([]byte{'C'}, 5000)...)
				expected = append(expected, bytes.Repeat([]byte{'S'}, 5000)...)
				expected = append(expected, bytes.Repeat([]byte{'B'}, 5000)...)
				if !bytes.Equal(audio, expected) || strings.Join(observed, ",") != "Track 1,Track 3,Track 2" {
					t.Fatal("choice or speech order wrong", observed, len(audio))
				}
				joined := strings.Join(titles, " ")
				if !strings.Contains(joined, "Agent FM - Up next:") || !strings.Contains(joined, "Track 3") || !strings.Contains(joined, "Track 2") {
					t.Fatal(titles)
				}
			}
			if m.queues[id].active || len(m.queueSlots) != 0 {
				t.Fatal("stopped session leaked")
			}
		})
	}
}
func TestAgentFMLateLinkCancellationAndFileCleanup(t *testing.T) {
	entered := make(chan struct{})
	q := &musicQueue{ffmpeg: fakeAgentFFmpeg(t), agent: agentServiceFunc(func(ctx context.Context, _ agentfm.Track, _ []agentfm.Track) (agentfm.Segment, error) {
		close(entered)
		<-ctx.Done()
		return agentfm.Segment{}, ctx.Err()
	})}
	job := prepareAgentLink(context.Background(), q, content.Item{}, []content.Item{{}})
	<-entered
	link := job.ready()
	if link.path != "" {
		t.Fatal("late speech used")
	}
	job.close()
	q.agent = agentServiceFunc(func(context.Context, agentfm.Track, []agentfm.Track) (agentfm.Segment, error) {
		return agentfm.Segment{Audio: []byte("speech")}, nil
	})
	job = prepareAgentLink(context.Background(), q, content.Item{}, []content.Item{{}})
	<-job.done
	link = job.ready()
	if link.path == "" {
		t.Fatal("no downloaded file")
	}
	b, e := os.ReadFile(link.path)
	if e != nil || string(b) != "speech" {
		t.Fatal(e)
	}
	job.close()
	if _, e := os.Stat(link.path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("speech cache leaked", e)
	}
}
func TestGeneratedAgentMusicSpeechMusicDecodes(t *testing.T) {
	ffmpeg, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("FFmpeg integration runs in CI and the Docker runtime")
	}
	var joined bytes.Buffer
	for _, rate := range []string{"44100", "24000", "48000"} {
		input, e := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate="+rate, "-t", "0.4", "-ac", "1", "-c:a", "libmp3lame", "-f", "mp3", "pipe:1").Output()
		if e != nil {
			t.Fatal(e)
		}
		audio, e := normalizeAgentAudio(context.Background(), ffmpeg, "mp3", io.NopCloser(bytes.NewReader(input)))
		if e != nil {
			t.Fatal(e)
		}
		_, e = io.Copy(&joined, audio)
		audio.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	cmd := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-f", "mp3", "-i", "pipe:0", "-f", "s16le", "-ar", "44100", "-ac", "2", "pipe:1")
	cmd.Stdin = &joined
	pcm, e := cmd.Output()
	if e != nil || len(pcm) < int(1.2*44100*4) {
		t.Fatal("music/speech/music did not decode", len(pcm), e)
	}
}

func TestAgentLibraryPaginationCyclesAndMinimumTracks(t *testing.T) {
	for _, tc := range []struct {
		n    int
		kind string
		want int
	}{{103, "tracks", 103}, {3, "folder", 2}, {1, "tracks", 0}} {
		m, _ := queueFixture(t, tc.n, tc.kind)
		tracks, e := m.agentLibrary(context.Background(), "0")
		if tc.want == 0 {
			if e == nil {
				t.Fatal("started with one track")
			}
			continue
		}
		if e != nil || len(tracks) != tc.want {
			t.Fatal(len(tracks), tc.want, e)
		}
		seen := map[string]bool{}
		for _, v := range tracks {
			if seen[v.PlaybackID] {
				t.Fatal("duplicate track")
			}
			seen[v.PlaybackID] = true
		}
	}
}
func TestAgentCandidatesAvoidCurrentAndRecentTracks(t *testing.T) {
	q := &musicQueue{tracks: []content.Item{{PlaybackID: "current"}, {PlaybackID: "recent"}, {PlaybackID: "available"}}, history: []string{"recent"}}
	c := agentCandidates(q, q.tracks[0])
	if len(c) != 1 || c[0].PlaybackID != "available" {
		t.Fatal(c)
	}
	q.history = append(q.history, "available")
	c = agentCandidates(q, q.tracks[0])
	if len(c) != 2 {
		t.Fatal("small-library fallback failed", c)
	}
	for _, v := range c {
		if v.PlaybackID == "current" {
			t.Fatal("immediate repeat")
		}
	}
}
func TestAgentSpeechFailureKeepsValidatedChoice(t *testing.T) {
	q := &musicQueue{agent: agentServiceFunc(func(context.Context, agentfm.Track, []agentfm.Track) (agentfm.Segment, error) {
		return agentfm.Segment{Index: 1}, errors.New("speech unavailable")
	})}
	job := prepareAgentLink(context.Background(), q, content.Item{}, []content.Item{{}, {}})
	<-job.done
	defer job.close()
	link := job.ready()
	if link.index != 1 || link.path != "" {
		t.Fatal(link)
	}
}
