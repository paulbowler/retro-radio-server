// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
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
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg integration runs in CI")
	}
	music := generatedAgentTone(t, ffmpeg, "440", "16")
	speech := generatedAgentTone(t, ffmpeg, "880", "1")
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "speech", true: "API failure"}[fail], func(t *testing.T) {
			m, folder := queueFixtureAudio(t, 3, "tracks", [][]byte{music, music, music})
			tracks, err := m.TrackList(context.Background(), folder)
			if err != nil {
				t.Fatal(err)
			}
			m.agentFFmpeg = ffmpeg
			// Keep the virtual listener bounded but paced: an unpaced sink can
			// consume a short song before even a local speech decoder starts.
			m.queueWait = func(ctx context.Context, _ time.Duration) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(10 * time.Millisecond):
					return nil
				}
			}
			calls := 0
			m.agent = agentServiceFunc(func(_ context.Context, current agentfm.Track, c []agentfm.Track) (agentfm.Segment, error) {
				calls++
				if fail && len(current.RecentLinks) != 0 {
					t.Error("failed speech entered memory")
				}
				if !fail && calls == 2 && (len(current.RecentLinks) != 1 || current.RecentLinks[0] != "First spoken link") {
					t.Error("previous link missing from next preparation", current.RecentLinks)
				}
				if fail {
					return agentfm.Segment{}, errors.New("simulated API unavailable")
				}
				want := "Track 3"
				if current.Title == want {
					want = "Track 2"
				}
				for i, v := range c {
					if v.Title == want {
						return agentfm.Segment{Index: i, Text: "First spoken link", Audio: speech}, nil
					}
				}
				return agentfm.Segment{Text: "First spoken link", Audio: speech}, nil
			})
			_, id, err := m.createQueue(context.Background(), tracks, model.LegacyXML, true)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var observed []string
			m.PlaybackObserver = func(r *http.Request, p content.Playback) func(bool) {
				if p.Codec() != "MP3" || p.Bitrate() != 128 {
					t.Error("output profile lost")
				}
				observed = append(observed, p.Item.Title)
				return func(bool) {
					if len(observed) == 3 {
						cancel()
					}
				}
			}
			r := httptest.NewRequest("GET", "/stream/upnp-queue/"+id+"?radio=test", nil).WithContext(ctx)
			r.Header.Set("Icy-MetaData", "1")
			r.Header.Set("Range", "bytes=0-1")
			w := httptest.NewRecorder()
			m.ServeQueue(w, r)
			if w.Code != 200 || len(observed) != 3 || w.Header().Get("Accept-Ranges") != "none" {
				t.Fatal(w.Code, observed)
			}
			audio, titles := splitQueueICY(t, w.Body.Bytes())
			if !fail && strings.Join(observed, ",") != "Track 1,Track 3,Track 2" {
				t.Fatal(observed)
			}
			if !fail && !strings.Contains(strings.Join(titles, " "), "Agent FM - Up next:") {
				t.Fatal(titles)
			}
			decoded := decodeAgentTestPCM(t, ffmpeg, audio)
			// The same MP3 decoder must consume all three songs and overlays.
			if len(decoded) < 15*agentPCMSecond {
				t.Fatal("continuous programme truncated", len(decoded))
			}
			if m.queues[id].active || len(m.queueSlots) != 0 {
				t.Fatal("stopped session leaked")
			}
		})
	}
}
func generatedAgentTone(t *testing.T, ffmpeg, frequency, duration string) []byte {
	t.Helper()
	data, err := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency="+frequency+":sample_rate=44100", "-t", duration, "-c:a", "libmp3lame", "-f", "mp3", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func decodeAgentTestPCM(t *testing.T, ffmpeg string, data []byte) []byte {
	t.Helper()
	audio, err := convertAgentAudio(context.Background(), ffmpeg, "mp3", io.NopCloser(bytes.NewReader(data)), false)
	if err != nil {
		t.Fatal(err)
	}
	defer audio.Close()
	pcm, err := io.ReadAll(audio)
	if err != nil {
		t.Fatal(err)
	}
	return pcm
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
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg integration runs in CI")
	}
	var programme bytes.Buffer
	for _, rate := range []string{"44100", "24000", "48000"} {
		data, err := exec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate="+rate, "-t", "0.4", "-c:a", "libmp3lame", "-f", "mp3", "pipe:1").Output()
		if err != nil {
			t.Fatal(err)
		}
		programme.Write(decodeAgentTestPCM(t, ffmpeg, data))
	}
	encoded, err := convertAgentAudio(context.Background(), ffmpeg, "f32le", io.NopCloser(bytes.NewReader(programme.Bytes())), true)
	if err != nil {
		t.Fatal(err)
	}
	mp3, err := io.ReadAll(encoded)
	encoded.Close()
	if err != nil {
		t.Fatal(err)
	}
	pcm := decodeAgentTestPCM(t, ffmpeg, mp3)
	if len(pcm) < int(1.2*agentPCMSecond) {
		t.Fatal("continuous encoder truncated programme", len(pcm))
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

func TestGeneratedAgentSpeechVolumeAndUnchangedMusic(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg integration runs in CI")
	}
	normal := decodeAgentTestPCM(t, ffmpeg, generatedAgentTone(t, ffmpeg, "440", "1"))
	encode := func(pcm []byte) []byte {
		audio, err := convertAgentAudio(context.Background(), ffmpeg, "f32le", io.NopCloser(bytes.NewReader(pcm)), true)
		if err != nil {
			t.Fatal(err)
		}
		mp3, err := io.ReadAll(audio)
		audio.Close()
		if err != nil {
			t.Fatal(err)
		}
		return decodeAgentTestPCM(t, ffmpeg, mp3)
	}
	base := agentTestRMS(encode(normal))
	for _, volume := range []int{25, 100, 400} {
		level := volume
		q := &musicQueue{volume: func() int { return level }}
		var output bytes.Buffer
		p := &agentProgram{sink: &output, timeline: &agentTimeline{}}
		if err := p.transition(q, nil, normal, content.Item{Title: "Next"}); err != nil {
			t.Fatal(err)
		}
		ratio := agentTestRMS(encode(output.Bytes())) / base
		want := float64(volume) / 100
		if math.Abs(ratio/want-1) > .12 {
			t.Fatal("encoded gain incorrect", volume, ratio)
		}
	}
	// Music without an announcement is copied byte for byte, at every voice gain.
	var output bytes.Buffer
	p := &agentProgram{sink: &output, timeline: &agentTimeline{}}
	q := &musicQueue{volume: func() int { return 400 }}
	if err := p.transition(q, normal, nil, content.Item{}); err != nil || !bytes.Equal(output.Bytes(), normal) {
		t.Fatal("voice gain altered music", err)
	}
}

func TestAgentPrimesChosenTrackBeforeHandover(t *testing.T) {
	m, folder := queueFixture(t, 3, "tracks")
	tracks, err := m.TrackList(context.Background(), folder)
	if err != nil {
		t.Fatal(err)
	}
	p := m.servers["fixture"].provider
	original := p.client.Transport
	var requests []string
	p.client.Transport = roundTripMusic(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/audio/") {
			requests = append(requests, r.URL.Path)
		}
		return original.RoundTrip(r)
	})
	q := &musicQueue{ffmpeg: fakeAgentFFmpeg(t), caps: model.LegacyXML}
	choice := &agentJob{done: make(chan struct{}), link: agentLink{index: 1}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job := m.primeAgentTrack(ctx, q, choice, tracks[1:])
	close(choice.done)
	select {
	case <-job.done:
	case <-time.After(3 * time.Second):
		job.close()
		t.Fatal("next music did not become ready")
	}
	if strings.Join(requests, ",") != "/audio/2" || q.next != 0 {
		t.Fatal("prefetch fetched wrong tracks or advanced playback", requests, q.next)
	}
	primed := job.take(tracks[2].PlaybackID)
	if primed == nil || primed.play.Item.Title != "Track 3" {
		t.Fatal("selected track was not primed")
	}
	job.close() // Transferred ownership must preserve the ready audio.
	data, err := io.ReadAll(primed.audio)
	primed.audio.Close()
	if err != nil || !bytes.Equal(data, bytes.Repeat([]byte{'C'}, 5000)) {
		t.Fatal("primed track could not play", len(data), err)
	}
	if ctx.Err() != nil {
		t.Fatal("closing prefetched audio cancelled the playing session")
	}
}

func TestAgentPrefetchCancelledBeforeChoice(t *testing.T) {
	m, _ := queueFixture(t, 2, "tracks")
	choice := &agentJob{done: make(chan struct{})}
	job := m.primeAgentTrack(context.Background(), &musicQueue{}, choice, nil)
	done := make(chan struct{})
	go func() { job.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("prefetch waited for a late AI choice during stop")
	}
	if job.track != nil {
		t.Fatal("unselected audio was opened")
	}
}

func TestAgentSuppliesEditorialMetadataAndRecentHistory(t *testing.T) {
	tracks := make([]content.Item, 8)
	var history []string
	for i := range tracks {
		id := string(rune('a' + i))
		tracks[i] = content.Item{PlaybackID: id, Title: id, Artist: "Performer", Album: "Album", Composer: "Writer", GenreName: "Rock", Date: "1983", Duration: "0:04:30"}
		if i < 7 {
			history = append(history, id)
		}
	}
	var received agentfm.Track
	var offered []agentfm.Track
	q := &musicQueue{tracks: tracks, history: history, agent: agentServiceFunc(func(_ context.Context, current agentfm.Track, candidates []agentfm.Track) (agentfm.Segment, error) {
		received, offered = current, candidates
		return agentfm.Segment{}, errors.New("no speech needed")
	})}
	q.rememberLink("Earlier commentary")
	job := prepareAgentLink(context.Background(), q, tracks[7], tracks[:2])
	defer job.close()
	<-job.done
	if len(received.Recent) != 5 || received.Recent[0].Title != "c" || received.Recent[4].Title != "g" {
		t.Fatal(received)
	}
	if len(received.RecentLinks) != 1 || received.RecentLinks[0] != "Earlier commentary" {
		t.Fatal(received)
	}
	for _, track := range append(offered, received) {
		if track.Artist != "Performer" || track.Composer != "Writer" || track.Genre != "Rock" || track.Date != "1983" || track.Duration != "0:04:30" {
			t.Fatal(track)
		}
	}
}

func TestCachedLocalLinkExpiresOrStopsWhenSettingsChange(t *testing.T) {
	now := time.Now()
	link := agentLink{path: "cached-speech", expires: now.Add(time.Minute)}
	if !link.playable(now) || link.playable(now.Add(time.Minute)) {
		t.Fatal("expired bulletin eligible for speech")
	}
	allowed := false
	link.localAllowed = func() bool { return allowed }
	if link.playable(now) {
		t.Fatal("changed/disabled locality played stale cached bulletin")
	}
	allowed = true
	if !link.playable(now) {
		t.Fatal("valid cached bulletin rejected")
	}
}

func TestAgentProgrammeMemoryBoundedAndIsolated(t *testing.T) {
	q := &musicQueue{}
	for i := 0; i < agentfm.MaxRecentLinks+2; i++ {
		q.rememberLink(fmt.Sprintf("Link %d", i))
	}
	q.rememberLink(" ")
	links := q.recentLinks()
	if len(links) != agentfm.MaxRecentLinks || links[0] != "Link 2" {
		t.Fatal(links)
	}
	links[0] = "changed snapshot"
	if q.recentLinks()[0] != "Link 2" {
		t.Fatal("snapshot mutated memory")
	}
	if len((&musicQueue{}).recentLinks()) != 0 {
		t.Fatal("station memory leaked")
	}
	q.rememberLink(strings.Repeat("é", agentfm.MaxLinkChars+1))
	if len([]rune(q.recentLinks()[agentfm.MaxRecentLinks-1])) != agentfm.MaxLinkChars {
		t.Fatal("unbounded script")
	}
}

// A steady sine proves multiplication, but misses soft syllables and loud
// transients. Exercise that dynamic range through the real speech cache filter,
// saved settings callback, programme encoder and MP3 decoder.
func TestAgentSpeechLevellingAndSavedGainThroughMP3(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg integration runs in CI")
	}
	const seconds = 12
	pcm := make([]byte, seconds*agentPCMSecond)
	for frame := 0; frame < seconds*agentPCMRate; frame++ {
		phase := float64(frame%agentPCMRate) / agentPCMRate
		level := .006
		switch {
		case phase < .10 || phase > .90:
			level = 0
		case phase > .55 && phase < .70:
			level = .15
		case phase > .70:
			level = .012
		}
		time := float64(frame) / agentPCMRate
		v := float32(level * (.7*math.Sin(2*math.Pi*240*time) + .3*math.Sin(2*math.Pi*1100*time)))
		for channel := 0; channel < 2; channel++ {
			binary.LittleEndian.PutUint32(pcm[frame*agentPCMFrame+4*channel:], math.Float32bits(v))
		}
	}
	encode := func(source []byte) []byte {
		t.Helper()
		r, err := convertAgentAudio(context.Background(), ffmpeg, "f32le", io.NopCloser(bytes.NewReader(source)), true)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		result, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	var prepared [][]byte
	for _, sourceGain := range []float64{.1, 1} {
		source := make([]byte, len(pcm))
		for i := 0; i < len(pcm); i += 4 {
			v := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:]))
			binary.LittleEndian.PutUint32(source[i:], math.Float32bits(float32(float64(v)*sourceGain)))
		}
		mp3 := encode(source)
		r, err := convertAgentSpeech(context.Background(), ffmpeg, io.NopCloser(bytes.NewReader(mp3)))
		if err != nil {
			t.Fatal(err)
		}
		levelled, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(levelled) < (seconds-1)*agentPCMSecond || len(levelled) > maxAgentSpeechPCM {
			t.Fatal("speech duration changed", len(levelled))
		}
		if ratio := agentTestRMS(levelled) / agentTestRMS(source); ratio < 2 {
			t.Fatal("quiet speech not brought up", sourceGain, ratio)
		}
		for i := 0; i < len(levelled); i += 4 {
			if math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(levelled[i:])))) > .82 {
				t.Fatal("speech levelling exceeded peak ceiling")
			}
		}
		prepared = append(prepared, levelled)
		t.Log("source gain", sourceGain, "levelled RMS", agentTestRMS(levelled))
	}
	// A 20 dB source-level difference must not remain a 20 dB voice difference.
	if ratio := agentTestRMS(prepared[1]) / agentTestRMS(prepared[0]); ratio < .5 || ratio > 2 {
		t.Fatal("speech baseline still depends on source volume", ratio)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "speech.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	q := &musicQueue{volume: func() int { return db.Settings().AgentVolume }}
	var rms []float64
	for _, volume := range []int{25, 100, 400} {
		settings := store.DefaultSettings()
		settings.AgentVolume = volume
		if err := db.SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		var mixed bytes.Buffer
		p := &agentProgram{sink: &mixed, timeline: &agentTimeline{}}
		if err := p.transition(q, nil, prepared[1], content.Item{}); err != nil {
			t.Fatal(err)
		}
		decoded := decodeAgentTestPCM(t, ffmpeg, encode(mixed.Bytes()))
		rms = append(rms, agentTestRMS(decoded))
	}
	if rms[1]/rms[0] < 3.5 || rms[2]/rms[1] < 1.5 {
		t.Fatal("saved slider ineffective after speech processing and MP3", rms)
	}
	t.Log("encoded RMS at 25%, 100%, 400%", rms)
}

type introAgentService struct {
	agentServiceFunc
	intro func(context.Context, string) (agentfm.Segment, error)
}

func (s introAgentService) Intro(ctx context.Context, name string) (agentfm.Segment, error) {
	return s.intro(ctx, name)
}

type cancelledAgentResponse struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w cancelledAgentResponse) Write(b []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(b)
	if w.Body.Len() >= 32000 {
		w.cancel()
	}
	return n, err
}

func TestStationWelcomeFiniteThenContinuousProgramme(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg integration runs in CI")
	}
	speech := generatedAgentTone(t, ffmpeg, "880", "2")
	music := generatedAgentTone(t, ffmpeg, "440", "16")
	m, folder := queueFixtureAudio(t, 2, "tracks", [][]byte{music, music})
	tracks, err := m.TrackList(context.Background(), folder)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	m.agentFFmpeg = ffmpeg
	m.AgentVolume = func() int { return 25 }
	m.agent = introAgentService{
		agentServiceFunc: func(context.Context, agentfm.Track, []agentfm.Track) (agentfm.Segment, error) {
			return agentfm.Segment{}, errors.New("mock link unavailable")
		},
		intro: func(context.Context, string) (agentfm.Segment, error) {
			calls++
			return agentfm.Segment{Audio: speech, Text: "You're listening to Retro Radio."}, nil
		},
	}
	m.queueWait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	_, id, err := m.createQueue(context.Background(), tracks, model.LegacyXML, true)
	if err != nil {
		t.Fatal(err)
	}
	q := m.queues[id]
	// HEAD must neither generate nor consume the welcome.
	head := httptest.NewRecorder()
	m.ServeQueue(head, httptest.NewRequest("HEAD", "/stream/upnp-queue/"+id, nil))
	if calls != 0 || q.introDone {
		t.Fatal("HEAD consumed welcome")
	}
	first := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/stream/upnp-queue/"+id, nil)
	request.Header.Set("Icy-MetaData", "1")
	m.ServeQueue(first, request)
	if first.Code != 200 || first.Header().Get("Content-Length") != fmt.Sprint(first.Body.Len()) || first.Header().Get("icy-metaint") != "" {
		t.Fatal("welcome was not a finite MP3", first.Code, first.Header())
	}
	if calls != 1 || !q.introDone || len(q.recentLinks()) != 1 {
		t.Fatal("welcome not completed", calls)
	}
	pcm, text, err := agentWelcome(context.Background(), &musicQueue{agent: m.agent, ffmpeg: ffmpeg, volume: func() int { return 25 }})
	if err != nil || text == "" || len(pcm) == 0 {
		t.Fatal(err)
	}
	r, err := convertAgentSpeech(context.Background(), ffmpeg, io.NopCloser(bytes.NewReader(speech)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(r)
	r.Close()
	if err != nil || !bytes.Equal(pcm, mixAgentPCM(nil, trimAgentSpeech(raw), .25)) {
		t.Fatal("welcome bypassed saved volume")
	}
	calls = 1
	// The Pure's next GET starts music, with no finite Content-Length or welcome.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	second := httptest.NewRecorder()
	m.ServeQueue(cancelledAgentResponse{second, cancel}, httptest.NewRequest("GET", "/stream/upnp-queue/"+id, nil).WithContext(ctx))
	if second.Code != 200 || second.Body.Len() < 32000 || second.Header().Get("Content-Length") != "" || calls != 1 {
		t.Fatal("music did not remain a separate continuous programme", second.Code, calls, second.Header())
	}
}

func TestStationWelcomeFailure(t *testing.T) {
	q := &musicQueue{agent: introAgentService{intro: func(context.Context, string) (agentfm.Segment, error) {
		return agentfm.Segment{}, errors.New("speech unavailable")
	}}}
	pcm, _, err := agentWelcome(context.Background(), q)
	if err == nil || len(pcm) != 0 || q.introDone {
		t.Fatal("failed welcome consumed session")
	}
}
