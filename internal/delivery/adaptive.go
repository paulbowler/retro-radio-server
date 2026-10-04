// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"retroradio.local/server/internal/model"
	"strings"
	"sync"
	"time"
)

func adaptiveKind(raw, content string, flagged bool) string {
	u, _ := url.Parse(raw)
	ext := ""
	if u != nil {
		ext = strings.ToLower(path.Ext(u.Path))
	}
	if ext == ".mpd" || strings.Contains(strings.ToLower(content), "dash+xml") {
		return "dash"
	}
	if flagged || ext == ".m3u8" || strings.Contains(strings.ToLower(content), "mpegurl") {
		return "hls"
	}
	return ""
}

// FFmpeg only sees loopback URLs. Every manifest, child playlist, initialisation
// fragment and segment is fetched through the checked Go client, including DNS
// pinning and redirect validation. No file/crypto/UDP protocols are enabled.
type mediaGateway struct {
	fetch         func(*http.Request) (*http.Response, error)
	client        *http.Client
	origin, token string
}

func (g *mediaGateway) local(raw string, base *url.URL) (string, error) {
	ref, e := url.Parse(raw)
	if e != nil {
		return "", e
	}
	target := base.ResolveReference(ref)

	if e = ValidateURL(target.String()); e != nil {
		return "", e
	}
	// Preserve DASH $Number$/$Time$ templates and encoded path characters.
	return g.origin + "/" + g.token + "/" + target.Scheme + "/" + target.Host + target.EscapedPath() + func() string {
		if target.RawQuery != "" {
			return "?" + target.RawQuery
		}
		return ""
	}(), nil
}

var uriAttribute = regexp.MustCompile(`URI="([^"]*)"`)

func (g *mediaGateway) hls(data []byte, base *url.URL) ([]byte, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("#EXTM3U")) {
		return nil, errors.New("invalid HLS playlist")
	}
	var out strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXT-X-KEY:") && !strings.Contains(line, "METHOD=NONE") {
			return nil, errors.New("encrypted HLS is not supported")
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			v, e := g.local(line, base)
			if e != nil {
				return nil, e
			}
			line = v
		} else {
			var rewriteErr error
			line = uriAttribute.ReplaceAllStringFunc(line, func(match string) string {
				v, e := g.local(match[5:len(match)-1], base)
				if e != nil {
					rewriteErr = e
					return match
				}
				return `URI="` + v + `"`
			})
			if rewriteErr != nil {
				return nil, rewriteErr
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return []byte(out.String()), nil
}

// DASH BaseURL inheritance is retained on the local gateway; explicit HTTP(S)
// references are rewritten, while relative segment templates stay relative.
func (g *mediaGateway) dash(data []byte, base *url.URL) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var out bytes.Buffer
	encoder := xml.NewEncoder(&out)
	var stack []string
	root := false
	for {
		token, e := decoder.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		switch t := token.(type) {
		case xml.StartElement:
			// Encoder emits namespaces from Name.Space; remove explicit xmlns
			// declarations to avoid duplicate attributes after token decoding.
			attrs := t.Attr[:0]
			for _, a := range t.Attr {
				if a.Name.Local != "xmlns" && a.Name.Space != "xmlns" {
					attrs = append(attrs, a)
				}
			}
			t.Attr = attrs
			if !root {
				if t.Name.Local != "MPD" {
					return nil, errors.New("invalid DASH manifest")
				}
				root = true
			}
			if t.Name.Local == "ContentProtection" {
				return nil, errors.New("protected DASH is not supported")
			}
			stack = append(stack, t.Name.Local)
			for i, a := range t.Attr {
				if a.Name.Local == "media" || a.Name.Local == "initialization" || a.Name.Local == "sourceURL" || a.Name.Local == "href" || (t.Name.Local == "UTCTiming" && a.Name.Local == "value") {
					ref, e := url.Parse(a.Value)
					if e != nil {
						return nil, e
					}
					if ref.IsAbs() || strings.HasPrefix(a.Value, "//") || strings.HasPrefix(a.Value, "/") {
						v, e := g.local(a.Value, base)
						if e != nil {
							return nil, e
						}
						t.Attr[i].Value = v
					}
				}
			}
			token = t
		case xml.CharData:
			if len(stack) > 0 && (stack[len(stack)-1] == "BaseURL" || stack[len(stack)-1] == "Location") {
				value := strings.TrimSpace(string(t))
				if value != "" {
					ref, e := url.Parse(value)
					if e != nil {
						return nil, e
					}
					if ref.IsAbs() || strings.HasPrefix(value, "//") || strings.HasPrefix(value, "/") {
						v, e := g.local(value, base)
						if e != nil {
							return nil, e
						}
						token = xml.CharData(v)
					}
				}
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.Directive:
			return nil, errors.New("DASH directives are not supported")
		}
		if e = encoder.EncodeToken(token); e != nil {
			return nil, e
		}
	}
	if e := encoder.Flush(); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func (g *mediaGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "method", 405)
		return
	}
	prefix := "/" + g.token + "/"
	if !strings.HasPrefix(r.URL.EscapedPath(), prefix) {
		http.NotFound(w, r)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.EscapedPath(), prefix), "/", 3)
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	raw := parts[0] + "://" + parts[1] + "/"
	if len(parts) == 3 {
		raw += parts[2]
	}
	if r.URL.RawQuery != "" {
		raw += "?" + r.URL.RawQuery
	}
	if ValidateURL(raw) != nil {
		http.Error(w, "blocked media URL", 502)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, r.Method, raw, nil)
	if e != nil {
		http.Error(w, "invalid media URL", 502)
		return
	}
	req.Header.Set("User-Agent", "RetroRadio/0.3 BBC compatible relay")
	if v := r.Header.Get("Range"); v != "" {
		req.Header.Set("Range", v)
	}
	fetch := g.fetch
	if fetch == nil {
		fetch = g.client.Do
	}
	res, e := fetch(req)
	if e != nil {
		http.Error(w, "media fetch failed", 502)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 206 {
		http.Error(w, "media unavailable", res.StatusCode)
		return
	}
	kind := adaptiveKind(res.Request.URL.String(), res.Header.Get("Content-Type"), false)
	body := bufio.NewReader(res.Body)
	prefixBytes, _ := body.Peek(64)
	prefixText := strings.TrimSpace(string(prefixBytes))
	if strings.HasPrefix(prefixText, "#EXTM3U") {
		kind = "hls"
	}
	if strings.HasPrefix(prefixText, "<?xml") || strings.HasPrefix(prefixText, "<MPD") {
		kind = "dash"
	}
	if kind != "" {
		data, e := io.ReadAll(io.LimitReader(body, (2<<20)+1))
		if e != nil || len(data) > 2<<20 {
			http.Error(w, "manifest too large", 502)
			return
		}
		if kind == "hls" {
			data, e = g.hls(data, res.Request.URL)
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		} else {
			data, e = g.dash(data, res.Request.URL)
			w.Header().Set("Content-Type", "application/dash+xml")
		}
		if e != nil {
			http.Error(w, "unsupported or unsafe manifest", 502)
			return
		}
		w.Write(data)
		return
	}
	for _, key := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := res.Header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	if r.Method != "HEAD" {
		io.Copy(w, io.LimitReader(body, 32<<20))
	}
}

type boundedDiagnostic struct{ bytes.Buffer }

func (b *boundedDiagnostic) Write(data []byte) (int, error) {
	n := len(data)
	remaining := 4096 - b.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		b.Buffer.Write(data)
	}
	return n, nil
}

type adaptiveReader struct {
	io.Reader
	close func()
	once  sync.Once
}

func (r *adaptiveReader) Close() error { r.once.Do(r.close); return nil }
func (p *Relay) openAdaptive(parent context.Context, raw, kind string) (io.ReadCloser, error) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		return nil, errors.New("FFmpeg is required for BBC HLS/DASH playback")
	}
	ctx, cancel := context.WithCancel(parent)
	select {
	case p.adaptiveSlots <- struct{}{}:
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
	release := func() { cancel(); <-p.adaptiveSlots }
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		release()
		return nil, e
	}
	token := make([]byte, 24)
	if _, e = rand.Read(token); e != nil {
		listener.Close()
		release()
		return nil, e
	}
	gateway := &mediaGateway{client: p.Client, fetch: p.request, origin: "http://" + listener.Addr().String(), token: hex.EncodeToString(token)}
	srv := &http.Server{Handler: gateway, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 25 * time.Second}
	go srv.Serve(listener)
	base, _ := url.Parse(raw)
	input, e := gateway.local(raw, base)
	if e != nil {
		srv.Close()
		release()
		return nil, e
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "http,tcp", "-rw_timeout", "15000000", "-f", kind}
	if kind == "hls" {
		args = append(args, "-allowed_extensions", "ALL")
	}
	args = append(args, "-i", input, "-map", "0:a:0", "-vn", "-threads", "1", "-c:a", "libmp3lame", "-b:a", "128k", "-ar", "44100", "-ac", "2", "-f", "mp3", "-flush_packets", "1", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.WaitDelay = time.Second
	cmd.Env = []string{}
	var diagnostic boundedDiagnostic
	cmd.Stderr = &diagnostic
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		srv.Close()
		release()
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		stdout.Close()
		srv.Close()
		release()
		return nil, e
	}
	reader := bufio.NewReader(stdout)
	timer := time.AfterFunc(25*time.Second, cancel)
	// Do not send a successful response for a manifest alone: require MP3 output.
	_, e = reader.Peek(4096)
	timer.Stop()
	cleanup := func() { cancel(); stdout.Close(); cmd.Wait(); srv.Close(); <-p.adaptiveSlots }
	if e != nil {
		cleanup()
		return nil, fmt.Errorf("adaptive audio could not be decoded: %w (%s)", e, diagnostic.String())
	}
	return &adaptiveReader{Reader: reader, close: cleanup}, nil
}

func (p *Relay) checkAdaptive(ctx context.Context, s model.Station, h model.Health, kind string, save func(model.Health) error) (model.Health, error) {
	h.Adaptive = kind
	audio, e := p.openAdaptive(ctx, s.URL, kind)
	if e != nil {
		h.Message = "Adaptive stream unavailable or could not be decoded"
		if _, lookErr := exec.LookPath("ffmpeg"); lookErr != nil {
			h.Message = "FFmpeg is required for HLS/DASH playback"
		}
		return h, save(h)
	}
	defer audio.Close()
	buf := make([]byte, 4096)
	if _, e = io.ReadFull(audio, buf); e != nil {
		h.Message = "No converted audio received"
		return h, save(h)
	}
	h.Working = true
	h.Codec = "MP3"
	h.Bitrate = 128
	h.Status = 200
	h.ContentType = "audio/mpeg"
	h.FinalURL = s.URL
	h.HTTPS = strings.HasPrefix(s.URL, "https://")
	h.LastSuccess = h.Checked
	h.Message = "Audio received via " + strings.ToUpper(kind) + " relay"
	return h, save(h)
}
func (p *Relay) serveAdaptive(w http.ResponseWriter, r *http.Request, s model.Station, kind string) {
	// Range/ICY requests do not apply to generated live MP3 audio.
	if r.Method == "HEAD" {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		return
	}
	audio, e := p.openAdaptive(r.Context(), s.URL, kind)
	if e != nil {
		p.Store.Log("", "Stream failed", s.Name+": adaptive relay could not start")
		http.Error(w, "adaptive stream unavailable", 502)
		return
	}
	defer audio.Close()
	playing := s
	playing.Codec = "MP3"
	key := p.beginPlayback(r, playing, http.Header{"Icy-Br": {"128"}})
	p.Store.Log("", "Stream connected", s.Name+": "+strings.ToUpper(kind)+" converted to MP3")
	defer func() {
		p.mu.Lock()
		delete(p.active, key)
		p.mu.Unlock()
		p.Store.Log("", "Stream disconnected", s.Name)
	}()
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Icy-Name", s.Name)
	w.Header().Set("Icy-Br", "128")
	w.WriteHeader(200)
	rc := http.NewResponseController(w)
	if rc.Flush() != nil {
		return
	}
	buf := make([]byte, 16<<10)
	for {
		n, readErr := audio.Read(buf)
		if n > 0 {
			rc.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, e = w.Write(buf[:n]); e != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}
