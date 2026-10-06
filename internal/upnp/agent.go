// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"retroradio.local/server/internal/agentfm"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
)

// SetAgentFM is called at startup, before background or HTTP work.
func (m *Manager) SetAgentFM(service agentfm.Service) {
	m.agent = service
	m.agentFFmpeg, _ = exec.LookPath("ffmpeg")
}
func (m *Manager) AgentFMAvailable() bool { return m.agent != nil && m.agentFFmpeg != "" }
func (m *Manager) StartAgentFM(ctx context.Context, caps model.Capabilities) (content.Playback, string, error) {
	if !m.AgentFMAvailable() {
		return content.Playback{}, "", errors.New("Agent FM requires an API key and FFmpeg")
	}
	if !caps.MP3 {
		return content.Playback{}, "", errors.New("Agent FM requires MP3 playback")
	}
	tracks, e := m.agentLibrary(ctx)
	if e != nil {
		return content.Playback{}, "", e
	}
	rand.Shuffle(len(tracks), func(i, j int) { tracks[i], tracks[j] = tracks[j], tracks[i] })
	return m.createQueue(ctx, tracks, caps, true)
}
func agentTrack(t content.Item) agentfm.Track {
	return agentfm.Track{Title: t.Title, Artist: t.Artist, Album: t.Album}
}

// Every Agent FM source is normalized to the same 128k/44.1k stereo MP3
// profile. FFmpeg sees checked bytes only and cannot fetch URLs or open files.
type agentAudio struct {
	io.Reader
	cmd    *exec.Cmd
	source io.ReadCloser
	stdout io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
	err    error
}

func (a *agentAudio) wait() { a.once.Do(func() { a.err = a.cmd.Wait() }) }
func (a *agentAudio) Read(b []byte) (int, error) {
	n, e := a.Reader.Read(b)
	if errors.Is(e, io.EOF) {
		a.wait()
		if a.err != nil {
			e = errors.New("Agent FM audio conversion failed")
		}
	}
	return n, e
}
func (a *agentAudio) Close() error {
	a.cancel()
	a.source.Close()
	a.stdout.Close()
	a.wait()
	return nil
}
func normalizeAgentAudio(parent context.Context, path, format string, source io.ReadCloser) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, path, "-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "pipe", "-f", format, "-i", "pipe:0", "-map", "0:a:0", "-vn", "-threads", "1", "-map_metadata", "-1", "-c:a", "libmp3lame", "-b:a", "128k", "-ar", "44100", "-ac", "2", "-id3v2_version", "0", "-write_xing", "0", "-f", "mp3", "-flush_packets", "1", "pipe:1")
	cmd.Env = []string{}
	cmd.WaitDelay = time.Second
	cmd.Stdin = source
	cmd.Stderr = io.Discard
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		source.Close()
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		cancel()
		source.Close()
		stdout.Close()
		return nil, e
	}
	stop := context.AfterFunc(ctx, func() { source.Close() })
	reader := bufio.NewReader(stdout)
	a := &agentAudio{Reader: reader, cmd: cmd, source: source, stdout: stdout, cancel: func() { cancel(); stop() }}
	timer := time.AfterFunc(15*time.Second, cancel)
	_, e = reader.Peek(1)
	timer.Stop()
	if e != nil {
		a.Close()
		return nil, errors.New("Agent FM audio could not be converted")
	}
	return a, nil
}

type agentLink struct {
	index int
	path  string
}

func (a agentLink) remove() {
	if a.path != "" {
		os.Remove(a.path)
	}
}

type agentJob struct {
	done   chan struct{}
	cancel context.CancelFunc
	link   agentLink
}

func prepareAgentLink(parent context.Context, q *musicQueue, current content.Item, remaining []content.Item) *agentJob {
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	job := &agentJob{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(job.done)
		defer cancel()
		candidates := make([]agentfm.Track, len(remaining))
		for i, t := range remaining {
			candidates[i] = agentTrack(t)
		}
		segment, e := q.agent.Prepare(ctx, agentTrack(current), candidates)
		if segment.Index >= 0 && segment.Index < len(remaining) {
			job.link.index = segment.Index
		}
		if e != nil {
			if ctx.Err() == nil {
				log.Printf("Agent FM: %v; continuing music", e)
			}
			return
		}
		if len(segment.Audio) == 0 || len(segment.Audio) > agentfm.MaxAudio {
			return
		}
		audio, e := normalizeAgentAudio(ctx, q.ffmpeg, "mp3", io.NopCloser(bytes.NewReader(segment.Audio)))
		if e != nil {
			log.Print("Agent FM: speech conversion failed; continuing music")
			return
		}
		defer audio.Close()
		file, e := os.CreateTemp("", "retro-agent-fm-*.mp3")
		if e != nil {
			log.Print("Agent FM: speech cache unavailable; continuing music")
			return
		}
		keep := false
		defer func() {
			file.Close()
			if !keep {
				os.Remove(file.Name())
			}
		}()
		n, e := io.Copy(file, io.LimitReader(audio, agentfm.MaxAudio+1))
		if e != nil || n == 0 || n > agentfm.MaxAudio || ctx.Err() != nil {
			return
		}
		if e = file.Close(); e != nil {
			return
		}
		job.link.path = file.Name()
		keep = true
	}()
	return job
}
func (j *agentJob) close() { j.cancel(); <-j.done; j.link.remove() }

// Never wait for online generation at the transition. Late links are discarded.
func (j *agentJob) ready() agentLink {
	select {
	case <-j.done:
		return j.link
	default:
		j.cancel()
		return agentLink{}
	}
}

func (m *Manager) serveAgentQueue(w http.ResponseWriter, r *http.Request, q *musicQueue, index int) {
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("icy-name", "Agent FM")
	icy := r.Header.Get("Icy-MetaData") == "1"
	if icy {
		w.Header().Set("icy-metaint", "4096")
	}
	if r.Method == "HEAD" {
		w.WriteHeader(200)
		return
	}
	rc := http.NewResponseController(w)
	defer rc.SetWriteDeadline(time.Time{})
	writer := queueICY{w: w, remaining: 4096, enabled: icy}
	started := false
	for {
		item := q.tracks[index]
		play, source, _, e := m.openQueueTrack(r.Context(), item.PlaybackID, q.caps)
		if e != nil {
			if !started {
				http.Error(w, "Agent FM track unavailable", 502)
			}
			return
		}
		format := "mp3"
		if play.Codec() == "AAC" {
			format = "aac"
		}
		audio, e := normalizeAgentAudio(r.Context(), q.ffmpeg, format, source)
		if e != nil {
			if !started {
				http.Error(w, "Agent FM audio conversion unavailable", 502)
			}
			return
		}
		// Start online preparation before sending this track, one transition ahead.
		candidates := agentCandidates(q, item)
		job := prepareAgentLink(r.Context(), q, item, candidates)
		writer.title = queueTitle(play.Item)
		writer.artwork = m.queueArtwork(play.Item)
		if !started {
			w.WriteHeader(200)
			started = true
		}
		// Normalized output is also reflected in the existing playback observer.
		play.Transcode = true
		complete := m.writeAgentAudio(r, w, rc, &writer, audio, &play)
		audio.Close()
		if !complete {
			if job != nil {
				job.close()
			}
			return
		}
		link := agentLink{}
		if job != nil {
			link = job.ready()
		}
		next := candidates[link.index]
		m.mu.Lock()
		q.history = append(q.history, item.PlaybackID)
		if len(q.history) > 20 {
			q.history = q.history[len(q.history)-20:]
		}
		for i, t := range q.tracks {
			if t.PlaybackID == next.PlaybackID {
				index = i
				break
			}
		}
		q.next = index
		q.seen = time.Now()
		m.mu.Unlock()
		if link.path != "" {
			speech, e := os.Open(link.path)
			if e == nil {
				writer.title = "Agent FM - Up next: " + queueTitle(next)
				writer.artwork = m.queueArtwork(next)
				complete = m.writeAgentAudio(r, w, rc, &writer, speech, nil)
				speech.Close()
			}
		}
		if job != nil {
			job.close()
		}
		if !complete {
			return
		}
	}
}

// A constant output profile gives predictable pacing for music and speech.
func (m *Manager) writeAgentAudio(r *http.Request, w http.ResponseWriter, rc *http.ResponseController, writer *queueICY, audio io.Reader, play *content.Playback) bool {
	wait := m.queueWait
	if wait == nil {
		wait = waitQueue
	}
	start := time.Now()
	sent := int64(0)
	var finish func(bool)
	complete := false
	defer func() {
		if finish != nil {
			finish(complete)
		}
	}()
	buffer := make([]byte, 4096)
	for {
		n, e := audio.Read(buffer)
		if n > 0 {
			due := start.Add(time.Duration(float64(sent+int64(n)) / 16000 * float64(time.Second))).Add(-250 * time.Millisecond)
			if wait(r.Context(), time.Until(due)) != nil {
				return false
			}
			_ = rc.SetWriteDeadline(time.Now().Add(20 * time.Second))
			written, err := writer.Write(buffer[:n])
			if err != nil || written != n {
				return false
			}
			sent += int64(n)
			if finish == nil && play != nil && m.PlaybackObserver != nil {
				finish = m.PlaybackObserver(r, *play)
			}
			_ = rc.Flush()
		}
		if e != nil {
			if !errors.Is(e, io.EOF) {
				return false
			}
			break
		}
	}
	complete = wait(r.Context(), time.Until(start.Add(time.Duration(float64(sent)/16000*float64(time.Second))))) == nil
	return complete
}

var _ content.AgentProvider = (*Manager)(nil)

// Build a bounded fresh pool using the existing pinned UPnP browser. Cycles and
// alternate views of the same object cannot grow the pool or repeat that track.
func (m *Manager) agentLibrary(parent context.Context) ([]content.Item, error) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	folders := []string{"0"}
	seenFolders := map[string]bool{"0": true}
	seenTracks := map[string]bool{}
	var tracks []content.Item
	pages := 0
	for len(folders) > 0 && pages < 200 && len(tracks) < maxQueueTracks && ctx.Err() == nil {
		folder := folders[0]
		folders = folders[1:]
		for offset := 0; pages < 200 && len(tracks) < maxQueueTracks && ctx.Err() == nil; {
			pages++
			page, e := m.Browse(ctx, folder, offset, MaxPage)
			if e != nil || len(page.Items) == 0 {
				break
			}
			// Shuffle navigation branches, so repeated sessions can sample other views.
			rand.Shuffle(len(page.Items), func(i, j int) { page.Items[i], page.Items[j] = page.Items[j], page.Items[i] })
			for _, t := range page.Items {
				if t.Kind == content.Folder && !seenFolders[t.ID] && len(seenFolders) < 1000 {
					seenFolders[t.ID] = true
					folders = append(folders, t.ID)
				} else if t.Kind == content.PlayableItem && t.PlaybackCodec != "" && !seenTracks[t.PlaybackID] && len(tracks) < maxQueueTracks {
					seenTracks[t.PlaybackID] = true
					tracks = append(tracks, t)
				}
			}
			offset += len(page.Items)
			if offset >= page.Total {
				break
			}
		}
	}
	if parent.Err() != nil {
		return nil, parent.Err()
	}
	if len(tracks) < 2 {
		return nil, errors.New("Agent FM needs at least two playable tracks on the UPnP music servers")
	}
	log.Printf("Agent FM: sampled %d tracks from the music servers", len(tracks))
	return tracks, nil
}

func agentCandidates(q *musicQueue, current content.Item) []content.Item {
	recent := map[string]bool{}
	for _, id := range q.history {
		recent[id] = true
	}
	collect := func(avoidRecent bool) []content.Item {
		var list []content.Item
		for _, t := range q.tracks {
			if t.PlaybackID != current.PlaybackID && (!avoidRecent || !recent[t.PlaybackID]) {
				list = append(list, t)
			}
		}
		return list
	}
	list := collect(true)
	if len(list) == 0 {
		list = collect(false)
	}
	rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
	if len(list) > 100 {
		list = list[:100]
	}
	return list
}
