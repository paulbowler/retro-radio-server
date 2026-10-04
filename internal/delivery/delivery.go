// SPDX-License-Identifier: GPL-3.0-only
// Package delivery translates transport, never audio codecs.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strconv"
	"strings"
	"sync"
	"time"
)

func PlayURL(base string, s model.Station, c model.Capabilities, health ...model.Health) (string, error) {
	if len(s.Variants) > 1 {
		if len(RankedStreams(s, c, "")) == 0 {
			return "", errors.New("no compatible audio stream")
		}
		return base + "/stream/" + s.StreamID, nil
	}
	if adaptiveKind(s.URL, "", s.HLS) != "" {
		if !c.HTTP || !c.MP3 {
			return "", errors.New("adaptive relay requires HTTP and MP3 playback")
		}
		return base + "/stream/" + s.StreamID, nil
	}
	if (s.Codec == "MP3" && !c.MP3) || (s.Codec == "AAC" && !c.AAC) || (s.Codec != "MP3" && s.Codec != "AAC") {
		return "", errors.New("unsupported codec; no transcoder installed")
	}
	if err := ValidateURL(s.URL); err != nil {
		return "", err
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "https" && c.HTTPS {
		return s.URL, nil
	}
	if u.Scheme == "http" && c.HTTP && len(health) > 0 {
		h := health[0]
		if h.Working && !h.HTTPS && h.FinalURL == s.URL && time.Since(h.Checked) < time.Hour {
			return s.URL, nil
		}
	}
	// HTTP may redirect to HTTPS. Without a validation result, keep it behind the relay.
	if !c.HTTP {
		return "", errors.New("device does not support HTTP")
	}
	return base + "/stream/" + s.StreamID, nil
}

var ErrUnsafeTarget = errors.New("upstream target rejected by address policy")

var denied = []netip.Prefix{
	netip.MustParsePrefix("::/96"), netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("64:ff9b::/96"),
}

func SafeIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range denied {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return errors.New("unsafe upstream URL")
	}
	if !safePort(u.Port()) {
		return errors.New("upstream port must be 80, 443 or 1024–65535")
	}
	return literalIP(u.Hostname())
}

func safePort(p string) bool {
	if p == "" || p == "80" || p == "443" {
		return true
	}
	n, e := strconv.Atoi(p)
	return e == nil && n >= 1024 && n <= 65535
}

type idleConn struct{ net.Conn }

func (c idleConn) Read(p []byte) (int, error) {
	if err := c.SetReadDeadline(time.Now().Add(45 * time.Second)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}
func NewClient() *http.Client {
	tr := &http.Transport{Proxy: nil, DisableCompression: true, ResponseHeaderTimeout: 15 * time.Second, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 16, MaxResponseHeaderBytes: 64 << 10,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if !safePort(port) {
				return nil, fmt.Errorf("%w: unsafe port", ErrUnsafeTarget)
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("no DNS addresses")
			}
			// Reject mixed answers too. Dial the checked address, never resolve it again.
			for _, ip := range ips {
				if !SafeIP(ip) {
					return nil, fmt.Errorf("%w: non-public DNS address", ErrUnsafeTarget)
				}
			}
			var last error
			for _, ip := range ips {
				c, err := (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return idleConn{c}, nil
				}
				last = err
			}
			return nil, last
		}}
	return &http.Client{Transport: tr, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("too many redirects")
		}
		return ValidateURL(r.URL.String())
	}}
}

// HTTP catalogue links are often stale: prefer verified HTTPS on the same
// origin, then use the original HTTP URL when that service is unavailable.
// Explicit HTTPS URLs retain TLS validation; redirects use the checked client.
func (p *Relay) request(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "http" && (req.URL.Port() == "" || req.URL.Port() == "80") {
		secure := req.Clone(req.Context())
		u := *req.URL
		u.Scheme = "https"
		if u.Port() == "80" {
			u.Host = u.Hostname()
		}
		secure.URL = &u
		res, e := p.Client.Do(secure)
		if e == nil && (res.StatusCode == 200 || res.StatusCode == 206 || res.StatusCode == 416) {
			return res, nil
		}
		if res != nil {
			res.Body.Close()
		}
		if errors.Is(e, ErrUnsafeTarget) {
			return nil, e
		}
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
	}
	return p.Client.Do(req)
}

type Active struct {
	Station string    `json:"station"`
	Since   time.Time `json:"since"`
}
type Relay struct {
	Store         *store.Store
	Client        *http.Client
	mu            sync.Mutex
	active        map[uint64]Active
	next          uint64
	adaptiveSlots chan struct{}
	slots         chan struct{}
}

func New(s *store.Store) *Relay {
	return &Relay{Store: s, Client: NewClient(), active: map[uint64]Active{}, slots: make(chan struct{}, 16), adaptiveSlots: make(chan struct{}, 4)}
}
func (p *Relay) Active() []Active {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []Active{}
	for _, a := range p.active {
		out = append(out, a)
	}
	return out
}
func (p *Relay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/stream/")
	s, err := p.Store.Station(id, true)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err = ValidateURL(s.URL); err != nil {
		http.Error(w, "unsafe upstream", 502)
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		http.Error(w, "too many streams", 503)
		return
	}
	selected, res, kind, err := p.openStream(r, s)
	if err != nil {
		p.Store.Log("", "Stream failed", s.Name+": alternatives unavailable")
		http.Error(w, "station unavailable", 502)
		return
	}
	s = selected
	if kind != "" {
		p.serveAdaptive(w, r, s, kind)
		return
	}
	defer res.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Icy-MetaInt", "Icy-Br", "Icy-Description", "Icy-Genre", "Icy-Name", "Ice-Audio-Info", "Icy-Url"} {
		if v := res.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.StatusCode)
	if r.Method == "HEAD" || res.StatusCode == 416 {
		return
	}
	p.mu.Lock()
	p.next++
	key := p.next
	p.active[key] = Active{s.Name, time.Now().UTC()}
	p.mu.Unlock()
	p.Store.Log("", "Stream connected", s.Name+": "+res.Request.URL.Scheme+" upstream relayed over HTTP")
	defer func() {
		p.mu.Lock()
		delete(p.active, key)
		p.mu.Unlock()
		p.Store.Log("", "Stream disconnected", s.Name)
	}()
	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return
	}
	buf := make([]byte, 16<<10)
	for {
		n, readErr := res.Body.Read(buf)
		if n > 0 {
			_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err = w.Write(buf[:n]); err != nil {
				return
			}
			if err = rc.Flush(); err != nil {
				return
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && r.Context().Err() == nil {
				p.Store.Log("", "Stream interrupted", s.Name)
			}
			return
		}
	}
}

func audioContent(ct string) bool {
	ct = strings.ToLower(strings.Split(ct, ";")[0])
	if strings.Contains(ct, "mpegurl") || strings.Contains(ct, "scpls") {
		return false
	}
	return strings.HasPrefix(ct, "audio/") || ct == "application/octet-stream"
}
