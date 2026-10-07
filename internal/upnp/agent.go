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
	return m.startAgentFM(ctx, "0", "Agent FM", caps)
}

func (m *Manager) StartGenreFM(ctx context.Context, folder string, caps model.Capabilities) (content.Playback, string, error) {
	page, err := m.Browse(ctx, folder, 0, 1)
	if err != nil {
		return content.Playback{}, "", err
	}
	if !page.Genre {
		return content.Playback{}, "", errors.New("select a genre folder for genre FM")
	}
	return m.startAgentFM(ctx, folder, page.Title+" FM", caps)
}

func (m *Manager) startAgentFM(ctx context.Context, folder, name string, caps model.Capabilities) (content.Playback, string, error) {
	if !m.AgentFMAvailable() {
		return content.Playback{}, "", errors.New("Agent FM requires an API key and FFmpeg")
	}
	if !caps.MP3 {
		return content.Playback{}, "", errors.New("Agent FM requires MP3 playback")
	}
	tracks, e := m.agentLibrary(ctx, folder)
	if e != nil {
		return content.Playback{}, "", e
	}
	rand.Shuffle(len(tracks), func(i, j int) { tracks[i], tracks[j] = tracks[j], tracks[i] })
	play, session, err := m.createQueue(ctx, tracks, caps, true)
	if err == nil {
		m.mu.Lock()
		if q := m.queues[session]; q != nil {
			q.name = queueTitle(content.Item{Title: name})
		}
		m.mu.Unlock()
	}
	return play, session, err
}
func agentTrack(t content.Item) agentfm.Track {
	return agentfm.Track{Title: t.Title, Artist: t.Artist, Album: t.Album, Composer: t.Composer, Genre: t.GenreName, Date: t.Date, Duration: t.Duration}
}

// FFmpeg sees checked bytes only and cannot fetch URLs or open files.
// Decoders and the session encoder share cancellation and bounded pipes.
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

// Every input is decoded to one PCM profile; only the final programme is encoded.
func convertAgentAudio(parent context.Context, path, format string, source io.ReadCloser, encode bool) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(parent)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "pipe", "-probesize", "32768", "-analyzeduration", "0", "-f", format}
	if format == "f32le" {
		args = append(args, "-ar", "44100", "-ac", "2")
	}
	args = append(args, "-i", "pipe:0", "-map", "0:a:0", "-vn", "-threads", "1", "-map_metadata", "-1", "-ar", "44100", "-ac", "2")
	if encode {
		args = append(args, "-c:a", "libmp3lame", "-b:a", "128k", "-id3v2_version", "0", "-write_xing", "0", "-f", "mp3")
	} else {
		args = append(args, "-c:a", "pcm_f32le", "-f", "f32le")
	}
	args = append(args, "-flush_packets", "1", "pipe:1")
	cmd := exec.CommandContext(ctx, path, args...)

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
	index        int
	path         string
	expires      time.Time
	localStarted func()
	localAllowed func() bool
}

func (a agentLink) playable(now time.Time) bool {
	return a.path != "" && (a.expires.IsZero() || now.Before(a.expires)) && (a.localAllowed == nil || a.localAllowed())
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
	history := append([]string(nil), q.history[max(0, len(q.history)-5):]...)
	go func() {
		defer close(job.done)
		defer cancel()
		candidates := make([]agentfm.Track, len(remaining))
		for i, t := range remaining {
			candidates[i] = agentTrack(t)
		}
		present := agentTrack(current)
		// Snapshot the short listening history before the main loop advances it.
		for _, id := range history {
			for _, track := range q.tracks {
				if track.PlaybackID == id {
					present.Recent = append(present.Recent, agentTrack(track))
					break
				}
			}
		}
		segment, e := q.agent.Prepare(ctx, present, candidates)
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
		// Cache decoded speech so handover needs neither an API call nor a decoder.
		audio, e := convertAgentAudio(ctx, q.ffmpeg, "mp3", io.NopCloser(bytes.NewReader(segment.Audio)), false)
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
		n, e := io.Copy(file, io.LimitReader(audio, maxAgentSpeechPCM+1))
		if e != nil || n == 0 || n > maxAgentSpeechPCM || ctx.Err() != nil {
			return
		}
		if e = file.Close(); e != nil {
			return
		}
		job.link.path = file.Name()
		job.link.expires = segment.Expires
		job.link.localStarted, job.link.localAllowed = segment.LocalStarted, segment.LocalAllowed
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

func (m *Manager) openAgentTrack(ctx context.Context, q *musicQueue, item content.Item) (content.Playback, io.ReadCloser, error) {
	play, source, _, err := m.openQueueTrack(ctx, item.PlaybackID, q.caps)
	if err != nil {
		return play, nil, err
	}
	format := "mp3"
	if play.Codec() == "AAC" {
		format = "aac"
	}
	audio, err := convertAgentAudio(ctx, q.ffmpeg, format, source, false)
	return play, audio, err
}

type primedAgentTrack struct {
	id    string
	play  content.Playback
	audio io.ReadCloser
}

type agentTrackJob struct {
	done   chan struct{}
	cancel context.CancelFunc
	track  *primedAgentTrack
	taken  bool
}

type cancelAgentAudio struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (a *cancelAgentAudio) Close() error { a.cancel(); return a.ReadCloser.Close() }

// Wait for the choice, then open and prime exactly one future NAS track while
// the current track plays. Its bounded decoder pipe supplies backpressure.
func (m *Manager) primeAgentTrack(parent context.Context, q *musicQueue, link *agentJob, candidates []content.Item) *agentTrackJob {
	ctx, cancel := context.WithCancel(parent)
	job := &agentTrackJob{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(job.done)
		select {
		case <-link.done:
		case <-ctx.Done():
			return
		}
		if ctx.Err() != nil {
			return
		}
		item := candidates[link.link.index]
		play, audio, err := m.openAgentTrack(ctx, q, item)
		if err != nil {
			return
		}
		job.track = &primedAgentTrack{id: item.PlaybackID, play: play, audio: &cancelAgentAudio{ReadCloser: audio, cancel: cancel}}
	}()
	return job
}
func (j *agentTrackJob) take(id string) *primedAgentTrack {
	select {
	case <-j.done:
		if j.track != nil && j.track.id == id {
			j.taken = true
			return j.track
		}
	default:
	}
	return nil
}
func (j *agentTrackJob) close() {
	if j.taken {
		return
	}
	j.cancel()
	<-j.done
	if j.track != nil {
		j.track.audio.Close()
	}
}

var _ content.AgentProvider = (*Manager)(nil)
var _ content.GenreAgentProvider = (*Manager)(nil)

// Build a bounded fresh pool using the existing pinned UPnP browser. Cycles and
// alternate views of the same object cannot grow the pool or repeat that track.
func (m *Manager) agentLibrary(parent context.Context, root string) ([]content.Item, error) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	folders := []string{root}
	seenFolders := map[string]bool{root: true}
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
		return nil, errors.New("Agent FM needs at least two playable tracks in the selected music view")
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
