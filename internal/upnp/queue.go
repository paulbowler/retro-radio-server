// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
)

const maxQueueTracks = 1000
const queueLifetime = 24 * time.Hour

type musicQueue struct {
	tracks []content.Item
	caps   model.Capabilities
	next   int
	active bool
	seen   time.Time
}

// TrackList inspects only direct children, in server order, across menu pages.
// Any subfolder/non-audio entry means this is not a lowest-level track menu.
func (m *Manager) TrackList(parent context.Context, folder string) ([]content.Item, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	var tracks []content.Item
	total := -1
	update := ""
	for offset := 0; ; {
		page, e := m.Browse(ctx, folder, offset, MaxPage)
		if e != nil {
			return nil, e
		}
		if total < 0 {
			total, update = page.Total, page.UpdateID
			if total == 0 || total > maxQueueTracks {
				return nil, nil
			}
		}
		if page.Total != total || page.UpdateID != update || len(page.Items) == 0 || offset+len(page.Items) > total {
			return nil, errors.New("music folder changed or incomplete; reopen it")
		}
		for _, item := range page.Items {
			if item.Kind != content.PlayableItem || len(item.Resources) == 0 {
				return nil, nil
			}
			tracks = append(tracks, item)
		}
		offset += len(page.Items)
		if offset == total {
			return tracks, nil
		}
	}
}

func (m *Manager) StartSequence(ctx context.Context, folder, start string, caps model.Capabilities) (content.Playback, string, error) {
	tracks, e := m.TrackList(ctx, folder)
	if e != nil {
		return content.Playback{}, "", e
	}
	if len(tracks) == 0 {
		return content.Playback{}, "", errors.New("this folder does not contain only tracks")
	}
	if start != "" {
		index := -1
		for i, item := range tracks {
			if item.ID == start {
				index = i
				break
			}
		}
		if index < 0 {
			return content.Playback{}, "", errors.New("selected track disappeared; reopen the folder")
		}
		tracks = tracks[index:]
	}
	// Reject unsupported/mixed output codecs before starting an HTTP stream.
	// The existing FLAC fallback advertises MP3, so FLAC-only CDs still work.
	codec := ""
	for _, item := range tracks {
		if item.PlaybackCodec == "" {
			return content.Playback{}, "", errors.New("a track has no compatible audio resource")
		}
		if codec == "" {
			codec = item.PlaybackCodec
		}
		if item.PlaybackCodec != codec {
			return content.Playback{}, "", errors.New("mixed output codecs are not supported by Play All")
		}
	}
	play, e := m.Resolve(ctx, tracks[0].PlaybackID, caps)
	if e == nil && play.Codec() != codec {
		e = errors.New("selected codec is incompatible with this radio")
	}
	if e != nil {
		return content.Playback{}, "", e
	}
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		return content.Playback{}, "", e
	}
	id := hex.EncodeToString(random[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.queues == nil {
		m.queues = map[string]*musicQueue{}
		m.queueSlots = make(chan struct{}, 4)
	}
	now := time.Now()
	oldest := ""
	for key, q := range m.queues {
		if q.active {
			continue
		}
		if now.Sub(q.seen) > queueLifetime {
			delete(m.queues, key)
			continue
		}
		if oldest == "" || q.seen.Before(m.queues[oldest].seen) {
			oldest = key
		}
	}
	if len(m.queues) >= 128 {
		if oldest == "" {
			return content.Playback{}, "", errors.New("music playback busy")
		}
		delete(m.queues, oldest)
	}
	m.queues[id] = &musicQueue{tracks: tracks, caps: caps, seen: now}
	return play, id, nil
}

// openQueueTrack reuses the provider's pinned transport and FLAC converter.
// Native audio is not transcoded. Leading MP3 file tags are excluded from the
// ongoing stream; a repeated file header must not replace the ICY track text.
func (m *Manager) openQueueTrack(ctx context.Context, id string, caps model.Capabilities) (content.Playback, io.ReadCloser, float64, error) {
	s, ok := m.providerFor(id)
	if !ok {
		return content.Playback{}, nil, 0, ErrUnknownID
	}
	p := s.provider
	play, e := p.Resolve(ctx, id, caps)
	if e != nil {
		return play, nil, 0, e
	}
	var audio io.ReadCloser
	size := int64(-1)
	if play.Transcode {
		audio, e = p.openConverted(ctx, play.Resource)
	} else {
		req, _ := http.NewRequestWithContext(ctx, "GET", play.Resource.URL, nil)
		var res *http.Response
		res, e = p.client.Do(req)
		if e == nil {
			mime := strings.ToLower(strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0]))
			if res.StatusCode != 200 || (mime != "" && mime != play.Resource.MIME && mime != "application/octet-stream") {
				res.Body.Close()
				return play, nil, 0, errors.New("music resource unavailable or changed format")
			}
			audio, size = res.Body, res.ContentLength
		}
	}
	if e != nil {
		return play, nil, 0, e
	}
	reader := bufio.NewReader(audio)
	if play.Codec() == "MP3" {
		head, _ := reader.Peek(10)
		if len(head) == 10 && string(head[:3]) == "ID3" {
			for _, b := range head[6:10] {
				if b&128 != 0 {
					audio.Close()
					return play, nil, 0, errors.New("invalid MP3 file tag")
				}
			}
			tag := int(head[6])<<21 | int(head[7])<<14 | int(head[8])<<7 | int(head[9])
			tag += 10
			if head[3] == 4 && head[5]&16 != 0 {
				tag += 10
			}
			if tag > 1<<20 {
				audio.Close()
				return play, nil, 0, errors.New("MP3 file tag exceeds limit")
			}
			if _, e = reader.Discard(tag); e != nil {
				audio.Close()
				return play, nil, 0, e
			}
			if size >= 0 {
				size -= int64(tag)
			}
		}
	}
	rate := float64(play.Bitrate() * 125)
	if !play.Transcode && size > 0 {
		if duration := delivery.PlaybackDuration(play.Item.Duration); duration > 0 {
			rate = float64(size) / duration.Seconds()
		}
	}
	if rate <= 0 {
		rate = 16000
	} // Legacy MP3 profile when the server supplies no timing.
	return play, &queueAudio{Reader: reader, closer: audio}, rate, nil
}

type queueAudio struct {
	io.Reader
	closer io.Closer
}

func (a *queueAudio) Close() error { return a.closer.Close() }

func waitQueue(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ServeQueue holds one HTTP connection through every track. A completed session
// returns 410 on reconnect rather than looping; a new selection creates a session.
func (m *Manager) ServeQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/stream/upnp-queue/")
	m.mu.Lock()
	q, ok := m.queues[id]
	if !ok || time.Since(q.seen) > queueLifetime && !q.active {
		m.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if q.next == len(q.tracks) {
		m.mu.Unlock()
		http.Error(w, "Play All finished; select it again to restart", 410)
		return
	}
	if q.active && r.Method == "GET" {
		m.mu.Unlock()
		http.Error(w, "music session is already playing", 409)
		return
	}
	if raw := r.Header.Get("Range"); raw != "" && raw != "bytes=0-" {
		m.mu.Unlock()
		http.Error(w, "seeking is unavailable during Play All", 416)
		return
	}
	index := q.next
	if r.Method == "GET" {
		q.active = true
	}
	q.seen = time.Now()
	m.mu.Unlock()
	if r.Method == "GET" {
		defer func() { m.mu.Lock(); q.active = false; q.seen = time.Now(); m.mu.Unlock() }()
		select {
		case m.queueSlots <- struct{}{}:
			defer func() { <-m.queueSlots }()
		default:
			http.Error(w, "music playback busy", 503)
			return
		}
	}
	codec := q.tracks[index].PlaybackCodec
	mime := "audio/mpeg"
	if codec == "AAC" {
		mime = "audio/aac"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	metadata := r.Header.Get("Icy-MetaData") == "1"
	if metadata {
		w.Header().Set("icy-metaint", "4096")
	}
	if r.Method == "HEAD" {
		w.WriteHeader(200)
		return
	}
	rc := http.NewResponseController(w)
	defer rc.SetWriteDeadline(time.Time{})
	writer := queueICY{w: w, remaining: 4096, enabled: metadata}
	started := false
	wait := m.queueWait
	if wait == nil {
		wait = waitQueue
	}
	for ; index < len(q.tracks); index++ {
		play, audio, rate, e := m.openQueueTrack(r.Context(), q.tracks[index].PlaybackID, q.caps)
		if e == nil && play.Codec() != codec {
			audio.Close()
			e = errors.New("track output codec changed")
		}
		if e != nil {
			if !started {
				http.Error(w, "music track unavailable", 502)
			}
			return
		}
		writer.title = queueTitle(play.Item)
		if !started {
			w.Header().Set("icy-name", writer.title)
			w.WriteHeader(200)
			started = true
		}
		trackStart := time.Now()
		sent := int64(0)
		var finish func(bool)
		complete := false
		buffer := make([]byte, 4096)
		for {
			n, readErr := audio.Read(buffer)
			if n > 0 {
				// Keep a small lead instead of flooding the radio with entire NAS files.
				due := trackStart.Add(time.Duration(float64(sent+int64(n)) / rate * float64(time.Second))).Add(-250 * time.Millisecond)
				if e = wait(r.Context(), time.Until(due)); e != nil {
					break
				}
				_ = rc.SetWriteDeadline(time.Now().Add(20 * time.Second))
				written, err := writer.Write(buffer[:n])
				e = err
				if e == nil && written != n {
					e = io.ErrShortWrite
				}
				if e != nil {
					break
				}
				sent += int64(n)
				if finish == nil && m.PlaybackObserver != nil {
					finish = m.PlaybackObserver(r, play)
				}
				_ = rc.Flush()
			}
			if readErr != nil {
				complete = errors.Is(readErr, io.EOF)
				break
			}
		}
		audio.Close()
		if complete {
			e = wait(r.Context(), time.Until(trackStart.Add(time.Duration(float64(sent)/rate*float64(time.Second)))))
			complete = e == nil
		}
		if finish != nil {
			finish(complete)
		}
		if !complete {
			return
		}
		m.mu.Lock()
		q.next = index + 1
		q.seen = time.Now()
		m.mu.Unlock()
	}
}

func queueTitle(item content.Item) string {
	title := item.Title
	if item.Artist != "" {
		title = item.Artist + " - " + title
	}
	if item.Album != "" {
		title += " (" + item.Album + ")"
	}
	return strings.NewReplacer("\r", " ", "\n", " ", "\x00", " ", "'", "’", ";", ",").Replace(title)
}

type queueICY struct {
	w         io.Writer
	remaining int
	enabled   bool
	title     string
}

func (w *queueICY) Write(data []byte) (int, error) {
	if !w.enabled {
		return w.w.Write(data)
	}
	total := 0
	for len(data) > 0 {
		n := min(len(data), w.remaining)
		sent, e := w.w.Write(data[:n])
		total += sent
		w.remaining -= sent
		if e != nil {
			return total, e
		}
		if sent != n {
			return total, io.ErrShortWrite
		}
		data = data[n:]
		if w.remaining == 0 {
			title := []rune(w.title)
			for len([]byte("StreamTitle='"+string(title)+"';")) > 4080 {
				title = title[:len(title)-1]
			}
			text := []byte("StreamTitle='" + string(title) + "';")
			units := (len(text) + 15) / 16
			block := make([]byte, 1+units*16)
			block[0] = byte(units)
			copy(block[1:], text)
			sent, e = w.w.Write(block)
			if e != nil {
				return total, e
			}
			if sent != len(block) {
				return total, io.ErrShortWrite
			}
			w.remaining = 4096
		}
	}
	return total, nil
}

// Keep wire helpers independent of the stream source or user-supplied URLs.
var _ content.SequentialProvider = (*Manager)(nil)
