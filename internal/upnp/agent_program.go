// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sync"
	"time"

	"retroradio.local/server/internal/agentfm"
	"retroradio.local/server/internal/content"
)

const (
	agentPCMRate      = 44100
	agentPCMFrame     = 8 // Stereo float32.
	agentPCMSecond    = agentPCMRate * agentPCMFrame
	agentTailBytes    = 5 * agentPCMSecond
	agentOverlapBytes = 3 * agentPCMSecond
	maxAgentSpeechPCM = 60 * agentPCMSecond
)

// A bounded audio timeline lets metadata follow the encoded audio, rather than
// the producer which can be several packets ahead of the listener.
type agentCue struct {
	offset  int64
	title   string
	play    *content.Playback
	end     bool
	started func()
}
type agentTimeline struct {
	mu   sync.Mutex
	cues []agentCue
}

func (t *agentTimeline) add(c agentCue) { t.mu.Lock(); t.cues = append(t.cues, c); t.mu.Unlock() }
func (t *agentTimeline) ready(offset int64) []agentCue {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for n < len(t.cues) && t.cues[n].offset <= offset {
		n++
	}
	out := append([]agentCue(nil), t.cues[:n]...)
	t.cues = t.cues[n:]
	return out
}

// Strip at most two seconds of actual silence, never quiet musical passages.
// A stereo frame is silent only when both channels are below -60 dBFS.
func silentAgentFrame(b []byte) bool {
	for i := 0; i < agentPCMFrame; i += 4 {
		v := math.Float32frombits(binary.LittleEndian.Uint32(b[i : i+4]))
		if math.IsNaN(float64(v)) || math.Abs(float64(v)) >= .001 {
			return false
		}
	}
	return true
}
func trimAgentTail(b []byte) []byte {
	limit := max(0, len(b)-2*agentPCMSecond)
	for len(b)-agentPCMFrame >= limit && silentAgentFrame(b[len(b)-agentPCMFrame:]) {
		b = b[:len(b)-agentPCMFrame]
	}
	return b
}
func trimAgentSpeech(b []byte) []byte {
	b = b[:len(b)/agentPCMFrame*agentPCMFrame]
	limit := min(len(b), 2*agentPCMSecond)
	skipped := 0
	for skipped < limit && silentAgentFrame(b[skipped:skipped+agentPCMFrame]) {
		skipped += agentPCMFrame
	}
	return trimAgentTail(b[skipped:])
}
func agentVoiceGain(q *musicQueue) float64 {
	level := 100
	if q.volume != nil {
		level = q.volume()
	}
	if level < 25 || level > 400 {
		level = 100
	}
	return float64(level) / 100
}

// Apply voice gain in PCM, before the single MP3 encoder. There is no subsequent
// automatic loudness normalization that could undo the user's adjustment.
func mixAgentPCM(music, speech []byte, gain float64) []byte {
	n := max(len(music), len(speech))
	out := make([]byte, n)
	for i := 0; i < n; i += 4 {
		var value float64
		if i < len(music) {
			value = float64(math.Float32frombits(binary.LittleEndian.Uint32(music[i : i+4])))
			if i < len(speech) {
				value *= .25
			}
		}
		if i < len(speech) {
			value += gain * float64(math.Float32frombits(binary.LittleEndian.Uint32(speech[i:i+4])))
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			value = 0
		}
		value = max(-.95, min(.95, value))
		binary.LittleEndian.PutUint32(out[i:i+4], math.Float32bits(float32(value)))
	}
	return out
}

type agentProgram struct {
	sink     io.Writer
	timeline *agentTimeline
	written  int64
}

func (p *agentProgram) cue(title string, play *content.Playback, end bool) {
	p.timeline.add(agentCue{offset: p.written, title: title, play: play, end: end})
}
func (p *agentProgram) write(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	n, err := p.sink.Write(b)
	p.written += int64(n)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}

// Retain a small tail while decoding ahead. At EOF we can remove genuine trailing
// silence and talk over the last three seconds, without guessing from NAS tags.
func (p *agentProgram) music(audio io.Reader) ([]byte, error) {
	tail := make([]byte, 0, agentTailBytes+32768)
	buffer := make([]byte, 32768)
	leading := true
	skipped := 0
	for {
		n, err := audio.Read(buffer)
		tail = append(tail, buffer[:n]...)
		if leading {
			count := 0
			for count+agentPCMFrame <= len(tail) && skipped < 2*agentPCMSecond && silentAgentFrame(tail[count:count+agentPCMFrame]) {
				count += agentPCMFrame
				skipped += agentPCMFrame
			}
			tail = tail[count:]
			if len(tail) >= agentPCMFrame || skipped >= 2*agentPCMSecond {
				leading = false
			}
		}
		if len(tail) > agentTailBytes {
			count := (len(tail) - agentTailBytes) / agentPCMFrame * agentPCMFrame
			if e := p.write(tail[:count]); e != nil {
				return nil, e
			}
			copy(tail, tail[count:])
			tail = tail[:len(tail)-count]
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return nil, err
			}
			if len(tail)%agentPCMFrame != 0 {
				return nil, errors.New("incomplete Agent FM PCM frame")
			}
			return trimAgentTail(tail), nil
		}
	}
}
func (p *agentProgram) transition(q *musicQueue, tail, speech []byte, next content.Item, onStart ...func()) error {
	speech = trimAgentSpeech(speech)
	overlap := min(len(tail), len(speech), agentOverlapBytes)
	if err := p.write(tail[:len(tail)-overlap]); err != nil {
		return err
	}
	if len(speech) > 0 {
		log.Printf("Agent FM: voice gain %.0f%%; music overlap %.2fs", agentVoiceGain(q)*100, float64(overlap)/agentPCMSecond)
		var started func()
		if len(onStart) > 0 {
			started = onStart[0]
		}
		p.timeline.add(agentCue{offset: p.written, title: q.name + " - Up next: " + queueTitle(next), started: started})
	}
	music := tail[len(tail)-overlap:]
	for len(music) > 0 || len(speech) > 0 {
		count := min(8192, max(len(music), len(speech)))
		if len(music) > 0 {
			count = min(count, len(music))
		}
		musicCount, speechCount := min(count, len(music)), min(count, len(speech))
		mixed := mixAgentPCM(music[:musicCount], speech[:speechCount], agentVoiceGain(q))
		if err := p.write(mixed); err != nil {
			return err
		}
		music, speech = music[musicCount:], speech[speechCount:]
		if musicCount > 0 && len(music) == 0 {
			p.cue("", nil, true)
		}
	}
	if overlap == 0 {
		p.cue("", nil, true)
	}
	return nil
}

// Start music immediately so legacy radios can finish opening their decoder.
// Prepare the welcome in parallel, then duck the music under it after eight
// seconds of programme audio. No online call is awaited at a silent boundary.
func (p *agentProgram) intro(parent context.Context, q *musicQueue, first content.Item, music io.Reader) error {
	q.linkMu.Lock()
	played := q.introPlayed
	q.linkMu.Unlock()
	if played {
		return nil
	}
	presenter, ok := q.agent.(interface {
		Intro(context.Context, string) (agentfm.Segment, error)
	})
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	done := make(chan struct{})
	var speech []byte
	var text string
	var preparationErr error
	go func() {
		defer close(done)
		q.linkMu.Lock()
		cached := q.introAudio
		text = q.introText
		q.linkMu.Unlock()
		stage := "speech generation"
		if len(cached) == 0 {
			var segment agentfm.Segment
			segment, preparationErr = presenter.Intro(ctx, q.name)
			if preparationErr == nil && len(segment.Audio) > 0 && len(segment.Audio) <= agentfm.MaxAudio {
				cached, text = segment.Audio, segment.Text
				q.linkMu.Lock()
				q.introAudio, q.introText = cached, text
				q.linkMu.Unlock()
			} else if preparationErr == nil {
				preparationErr = errors.New("empty or oversized greeting")
			}
		}
		if preparationErr == nil {
			stage = "speech conversion"
			var audio io.ReadCloser
			audio, preparationErr = convertAgentSpeech(ctx, q.ffmpeg, io.NopCloser(bytes.NewReader(cached)))
			if preparationErr == nil {
				speech, preparationErr = io.ReadAll(io.LimitReader(audio, maxAgentSpeechPCM+1))
				audio.Close()
				if preparationErr == nil && (len(speech) > maxAgentSpeechPCM || len(trimAgentSpeech(speech)) == 0) {
					preparationErr = errors.New("silent or oversized greeting")
				}
				speech = trimAgentSpeech(speech)
			}
		}
		if ctx.Err() != nil {
			preparationErr = ctx.Err()
		}
		if preparationErr != nil && parent.Err() == nil {
			log.Printf("Agent FM: session introduction unavailable during %s: %v; continuing music", stage, preparationErr)
		}
	}()
	defer func() { cancel(); <-done }()
	buffer := make([]byte, 8192)
	const leadIn = 8 * agentPCMSecond
	for {
		select {
		case <-done:
			if preparationErr != nil {
				return parent.Err()
			}
			if p.written >= leadIn {
				goto welcome
			}
		default:
		}
		n, err := io.ReadFull(music, buffer)
		if n%agentPCMFrame != 0 {
			return errors.New("incomplete Agent FM opening PCM frame")
		}
		if e := p.write(buffer[:n]); e != nil {
			return e
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
	}
welcome:
	p.cue(q.name+" - Retro Radio", nil, false)
	for len(speech) > 0 {
		count := min(len(buffer), len(speech))
		n, err := io.ReadFull(music, buffer[:count])
		if n%agentPCMFrame != 0 {
			return errors.New("incomplete Agent FM opening PCM frame")
		}
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		if e := p.write(mixAgentPCM(buffer[:n], speech[:count], agentVoiceGain(q))); e != nil {
			return e
		}
		speech = speech[count:]
	}
	p.timeline.add(agentCue{offset: p.written, title: queueTitle(first), started: func() {
		q.linkMu.Lock()
		q.introPlayed = true
		q.introAudio = nil
		q.introText = ""
		q.linkMu.Unlock()
		q.rememberLink(text)
		log.Print("Agent FM: session introduction sent")
	}})
	return nil
}

func (m *Manager) agentProgram(ctx context.Context, q *musicQueue, index int, p *agentProgram) error {
	var primed *primedAgentTrack
	opening := true
	defer func() {
		if primed != nil {
			primed.audio.Close()
		}
	}()
	for ctx.Err() == nil {
		item := q.tracks[index]
		var play content.Playback
		var audio io.ReadCloser
		var err error
		if primed != nil {
			play, audio = primed.play, primed.audio
			primed = nil
		} else {
			play, audio, err = m.openAgentTrack(ctx, q, item)
		}
		if err != nil {
			return err
		}
		play.Transcode = true
		p.cue(queueTitle(play.Item), &play, false)
		if opening {
			opening = false
			if err = p.intro(ctx, q, item, audio); err != nil {
				audio.Close()
				return err
			}
		}
		candidates := agentCandidates(q, item)
		job := prepareAgentLink(ctx, q, item, candidates)
		future := m.primeAgentTrack(ctx, q, job, candidates)
		tail, err := p.music(audio)
		audio.Close()
		if err != nil {
			future.close()
			job.close()
			return err
		}
		link := job.ready()
		next := candidates[link.index]
		primed = future.take(next.PlaybackID)
		future.close()
		var speech []byte
		if link.playable(time.Now()) {
			speech, err = os.ReadFile(link.path)
			if err != nil {
				log.Print("Agent FM: cached speech unavailable; continuing music")
			}
		}
		err = p.transition(q, tail, speech, next, link.localStarted)
		job.close()
		if err != nil {
			return err
		}
		// Remember only a link successfully inserted into the programme, never a
		// prepared-but-skipped, expired or failed announcement. Record it before
		// preparing the following link, even if the encoder is slightly ahead.
		if len(trimAgentSpeech(speech)) > 0 {
			q.rememberLink(link.text)
		}
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
	}
	return ctx.Err()
}

func (m *Manager) serveAgentQueue(w http.ResponseWriter, r *http.Request, q *musicQueue, index int) {
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("icy-name", q.name)
	icy := r.Header.Get("Icy-MetaData") == "1"
	if icy {
		w.Header().Set("icy-metaint", "4096")
	}
	if r.Method == "HEAD" {
		w.WriteHeader(200)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	pcm, input := io.Pipe()
	timeline := &agentTimeline{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := m.agentProgram(ctx, q, index, &agentProgram{sink: input, timeline: timeline})
		input.CloseWithError(err)
	}()
	// One encoder for the whole session: no repeated MP3 padding or resets.
	encoded, err := convertAgentAudio(ctx, q.ffmpeg, "f32le", pcm, true)
	if err != nil {
		cancel()
		pcm.Close()
		<-done
		http.Error(w, "Agent FM audio unavailable", 502)
		return
	}
	defer func() { cancel(); encoded.Close(); pcm.Close(); <-done }()
	rc := http.NewResponseController(w)
	defer rc.SetWriteDeadline(time.Time{})
	writer := queueICY{w: w, remaining: 4096, enabled: icy}
	w.WriteHeader(200)
	wait := m.queueWait
	if wait == nil {
		wait = waitQueue
	}
	start := time.Now()
	var sent int64
	var finish func(bool)
	defer func() {
		if finish != nil {
			finish(false)
		}
	}()
	buffer := make([]byte, 2048)
	for {
		n, err := encoded.Read(buffer)
		if n > 0 {
			// Maintain one clock across music, overlays and spoken links.
			due := start.Add(time.Duration(float64(sent+int64(n)) / 16000 * float64(time.Second))).Add(-250 * time.Millisecond)
			if wait(ctx, time.Until(due)) != nil {
				return
			}
			for _, cue := range timeline.ready(sent * agentPCMSecond / 16000) {
				if cue.end && finish != nil {
					finish(true)
					finish = nil
				}
				if cue.started != nil {
					cue.started()
				}
				if cue.title != "" {
					writer.title = cue.title
				}
				if cue.play != nil && m.PlaybackObserver != nil {
					if finish != nil {
						finish(true)
					}
					finish = m.PlaybackObserver(r, *cue.play)
				}
				if ctx.Err() != nil {
					return
				}
			}
			_ = rc.SetWriteDeadline(time.Now().Add(20 * time.Second))
			count, e := writer.Write(buffer[:n])
			if e != nil || count != n {
				return
			}
			sent += int64(n)
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}
