// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"retroradio.local/server/internal/content"
	"sync"
	"time"
)

type convertedReader struct {
	io.Reader
	close func()
	once  sync.Once
}

func (r *convertedReader) Close() error { r.once.Do(r.close); return nil }

// FFmpeg receives only checked source bytes, never a URL, playlist, filesystem path
// or network access. Input is forced to FLAC; output is the existing MP3 profile.
func (p *Provider) openConverted(parent context.Context, resource content.Resource) (io.ReadCloser, error) {
	if p.scope == nil || p.scope.check(resource.URL) != nil {
		return nil, errors.New("FLAC source outside music server")
	}
	if p.ffmpegPath == "" {
		return nil, errors.New("FFmpeg required for FLAC playback")
	}
	select {
	case p.conversionSlots <- struct{}{}:
	default:
		return nil, errors.New("music conversion busy")
	}
	ctx, cancel := context.WithCancel(parent)
	release := func() { cancel(); <-p.conversionSlots }
	req, e := http.NewRequestWithContext(ctx, "GET", resource.URL, nil)
	if e != nil {
		release()
		return nil, e
	}
	res, e := p.client.Do(req)
	if e != nil {
		release()
		return nil, e
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		release()
		return nil, errors.New("FLAC source unavailable")
	}
	stopClose := context.AfterFunc(ctx, func() { res.Body.Close() })
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "pipe", "-f", "flac", "-i", "pipe:0", "-map", "0:a:0", "-vn", "-threads", "1", "-c:a", "libmp3lame", "-b:a", "128k", "-ar", "44100", "-ac", "2", "-f", "mp3", "-flush_packets", "1", "pipe:1"}
	cmd := exec.CommandContext(ctx, p.ffmpegPath, args...)
	cmd.WaitDelay = time.Second
	cmd.Env = []string{}
	cmd.Stdin = res.Body
	cmd.Stderr = io.Discard
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		res.Body.Close()
		stopClose()
		release()
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		stdout.Close()
		res.Body.Close()
		stopClose()
		release()
		return nil, e
	}
	cleanup := func() {
		cancel()
		res.Body.Close()
		stdout.Close()
		cmd.Wait()
		stopClose()
		<-p.conversionSlots
	}
	reader := bufio.NewReader(stdout)
	timer := time.AfterFunc(15*time.Second, cancel)
	// Delay successful HTTP headers until more than an ID3 header has been produced.
	_, e = reader.Peek(4096)
	timer.Stop()
	if e != nil {
		cleanup()
		return nil, errors.New("FLAC could not be converted to MP3")
	}
	return &convertedReader{Reader: reader, close: cleanup}, nil
}
func (p *Provider) serveConverted(w http.ResponseWriter, r *http.Request, play content.Playback) {
	// A converted byte stream has different offsets from the original file.
	// Accept browser opening probes (including bytes=0-1) as a full 200 stream,
	// matching Play All. Only nonzero/suffix/multiple ranges request real seeking.
	if !queueOpeningRange(r.Header.Get("Range")) {
		http.Error(w, "seeking is unavailable for converted tracks", 416)
		return
	}
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "HEAD" {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(200)
		return
	}
	audio, e := p.openConverted(r.Context(), play.Resource)
	if e != nil {
		http.Error(w, "music conversion unavailable", 502)
		return
	}
	defer audio.Close()
	w.Header().Set("Content-Type", "audio/mpeg")
	w.WriteHeader(200)
	rc := http.NewResponseController(w)
	defer rc.SetWriteDeadline(time.Time{})
	var finish func(bool)
	complete := false
	defer func() {
		if finish != nil {
			finish(complete)
		}
	}()
	buf := make([]byte, 32<<10)
	for {
		n, e := audio.Read(buf)
		if n > 0 {
			_ = rc.SetWriteDeadline(time.Now().Add(20 * time.Second))
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			if finish == nil && p.PlaybackObserver != nil {
				finish = p.PlaybackObserver(r, play)
			}
			_ = rc.Flush()
		}
		if e != nil {
			complete = errors.Is(e, io.EOF)
			return
		}
	}
}
