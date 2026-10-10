// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
)

type liveChunk struct {
	audio []byte
	title string
}
type liveOpen func(context.Context) (io.ReadCloser, http.Header, error)

// A bounded producer queue holds audio ahead of a paced consumer. Increasing a
// copy buffer alone would not provide any protection against an upstream outage.
func liveChunks(ctx context.Context, initial io.ReadCloser, headers http.Header, settings store.Settings, bitrate int, reopen liveOpen) <-chan liveChunk {
	blockSize := min(4096, max(256, bitrate*125/4))
	capacity := max(2, min(512, settings.BufferSeconds*bitrate*125/blockSize+1))
	chunks := make(chan liveChunk, capacity)
	go func() {
		defer close(chunks)
		body, h := initial, headers
		title := ""
		for {
			currentBody := body
			stop := context.AfterFunc(ctx, func() { _ = currentBody.Close() })
			observer := newICYObserver(h.Get("Icy-MetaInt"), func(value string) { title = cleanMetadata(value) })
			if h.Get("Icy-MetaInt") != "" && observer == nil {
				stop()
				body.Close()
				return
			}
			var readErr error
			for ctx.Err() == nil {
				buf := make([]byte, blockSize)
				// A stalled connection must be noticed before a short buffer runs dry.
				var timer *time.Timer
				if settings.AutoReconnect {
					current := body
					stall := 5 * time.Second
					if h.Get("X-Retro-Adaptive") == "1" {
						stall = 20 * time.Second
					}
					timer = time.AfterFunc(stall, func() { _ = current.Close() })
				}
				n, err := io.ReadFull(body, buf)
				if timer != nil {
					timer.Stop()
				}
				if n > 0 {
					audio := observer.Process(buf[:n], true)
					if len(audio) > 0 {
						select {
						case chunks <- liveChunk{audio: audio, title: title}:
						case <-ctx.Done():
							stop()
							body.Close()
							return
						}
					}
				}
				if err != nil {
					readErr = err
					break
				}
			}
			stop()
			body.Close()
			if ctx.Err() != nil || !settings.AutoReconnect || reopen == nil || errors.Is(readErr, ErrUnsafeTarget) {
				return
			}
			// Retry for a bounded period; keep the listener connection open meanwhile.
			deadline := time.Now().Add(time.Minute)
			recovered := false
			for time.Now().Before(deadline) && ctx.Err() == nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				attempt, cancel := context.WithCancel(ctx)
				timeout := time.AfterFunc(15*time.Second, cancel)
				next, nextHeaders, err := reopen(attempt)
				timeout.Stop()
				if err == nil {
					body = &cancelBody{ReadCloser: next, cancel: cancel}
					h = nextHeaders
					recovered = true
					break
				}
				cancel()
				if errors.Is(err, ErrUnsafeTarget) {
					return
				}
			}
			if !recovered {
				return
			}
		}
	}()
	return chunks
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error { b.cancel(); return b.ReadCloser.Close() }

// Emit consistent ICY framing even if a reopened source uses another interval.
type icyOutput struct {
	writer    io.Writer
	remaining int
}

func (o *icyOutput) write(chunk liveChunk) error {
	data := chunk.audio
	for len(data) > 0 {
		n := min(len(data), o.remaining)
		if _, err := o.writer.Write(data[:n]); err != nil {
			return err
		}
		data = data[n:]
		o.remaining -= n
		if o.remaining == 0 {
			text := ""
			if chunk.title != "" {
				text = "StreamTitle='" + strings.ReplaceAll(chunk.title, "'", "") + "';"
			}
			blocks := (len(text) + 15) / 16
			meta := make([]byte, 1+blocks*16)
			meta[0] = byte(blocks)
			copy(meta[1:], text)
			if _, err := o.writer.Write(meta); err != nil {
				return err
			}
			o.remaining = 8192
		}
	}
	return nil
}

func (p *Relay) serveBufferedLive(w http.ResponseWriter, r *http.Request, s model.Station, body io.ReadCloser, h http.Header, settings store.Settings, reopen liveOpen) {
	if h.Get("Icy-MetaInt") != "" && newICYObserver(h.Get("Icy-MetaInt"), func(string) {}) == nil {
		body.Close()
		http.Error(w, "invalid stream metadata", http.StatusBadGateway)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	bitrate := s.Bitrate
	if n, err := strconv.Atoi(h.Get("Icy-Br")); err == nil && n > 0 {
		bitrate = n
	}
	if bitrate <= 0 {
		bitrate = 128
	}
	bitrate = min(1024, max(8, bitrate))
	chunks := liveChunks(ctx, body, h, settings, bitrate, reopen)
	// Drain after cancellation so the reader and upstream are always released.
	defer func() {
		cancel()
		for range chunks {
		}
	}()
	for _, name := range []string{"Content-Type", "Icy-Br", "Icy-Name", "Icy-Genre", "Icy-Description", "Icy-Url"} {
		if value := h.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	metadata := r.Header.Get("Icy-MetaData") == "1"
	if metadata {
		w.Header().Set("Icy-MetaInt", "8192")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if rc.Flush() != nil {
		return
	}
	key := p.beginPlayback(r, s, h)
	p.Store.Log("", "Stream connected", s.Name+": buffered live relay")
	defer func() {
		p.mu.Lock()
		delete(p.active, key)
		p.mu.Unlock()
		p.Store.Log("", "Stream disconnected", s.Name)
	}()
	target := settings.BufferSeconds * bitrate * 125
	var pending []liveChunk
	buffered := 0
	// Headers are already sent; a modest tuning delay fills the actual reservoir.
	startup := time.NewTimer(time.Duration(settings.BufferSeconds+5) * time.Second)
	defer startup.Stop()
	priming := true
	for buffered < target && priming {
		select {
		case <-ctx.Done():
			return
		case <-startup.C:
			priming = false
		case chunk, ok := <-chunks:
			if !ok {
				priming = false
				break
			}
			pending = append(pending, chunk)
			buffered += len(chunk.audio)
		}
	}
	output := icyOutput{writer: w, remaining: 8192}
	emit := func(chunk liveChunk) bool {
		p.observeMetadata(key, chunk.title)
		_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Second))
		var err error
		if metadata {
			err = output.write(chunk)
		} else {
			_, err = w.Write(chunk.audio)
		}
		if err != nil || rc.Flush() != nil {
			return false
		}
		if target > 0 {
			delay := time.Duration(int64(len(chunk.audio)) * int64(time.Second) / int64(bitrate*125))
			select {
			case <-ctx.Done():
				return false
			case <-time.After(delay):
			}
		}
		return true
	}
	for _, chunk := range pending {
		if !emit(chunk) {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-chunks:
			if !ok || !emit(chunk) {
				return
			}
		}
	}
}

func (p *Relay) reopenLive(s model.Station) liveOpen {
	return func(ctx context.Context) (io.ReadCloser, http.Header, error) {
		if err := p.validateStation(s); err != nil {
			return nil, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("User-Agent", "RetroRadio/0.2")
		req.Header.Set("Icy-MetaData", "1")
		res, err := p.stationRequest(s, req)
		if err != nil {
			return nil, nil, err
		}
		content := strings.ToLower(strings.Split(res.Header.Get("Content-Type"), ";")[0])
		compatible := s.Codec == "MP3" && (content == "audio/mpeg" || content == "audio/mp3") || s.Codec == "AAC" && (content == "audio/aac" || content == "audio/aacp")
		if res.StatusCode != 200 || !compatible || res.Header.Get("Content-Length") != "" {
			res.Body.Close()
			return nil, nil, errors.New("live stream format changed")
		}
		if res.Header.Get("Icy-MetaInt") != "" && newICYObserver(res.Header.Get("Icy-MetaInt"), func(string) {}) == nil {
			res.Body.Close()
			return nil, nil, errors.New("invalid stream metadata")
		}
		prefix := make([]byte, 1024)
		n, readErr := res.Body.Read(prefix)
		if n == 0 || (readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF) {
			res.Body.Close()
			return nil, nil, errors.New("no live audio received")
		}
		return &prefixedBody{Reader: io.MultiReader(strings.NewReader(string(prefix[:n])), res.Body), Closer: res.Body}, res.Header, nil
	}
}
