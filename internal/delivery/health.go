// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"retroradio.local/server/internal/model"
	"strconv"
	"strings"
	"time"
)

// CheckTarget validates at import time. Dial-time checks remain mandatory to stop rebinding.
func CheckTarget(ctx context.Context, raw string) error {
	if e := ValidateURL(raw); e != nil {
		return e
	}
	u, _ := url.Parse(raw)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if e != nil {
		return e
	}
	if len(ips) == 0 {
		return errors.New("no DNS addresses")
	}
	for _, ip := range ips {
		if !SafeIP(ip) {
			return ErrUnsafeTarget
		}
	}
	return nil
}

// literalIP rejects obviously unsafe addresses before any network access, including direct URLs.
func literalIP(host string) error {
	if ip, e := netip.ParseAddr(host); e == nil && !SafeIP(ip) {
		return ErrUnsafeTarget
	}
	return nil
}
func (p *Relay) Check(ctx context.Context, s model.Station) (model.Health, error) {
	return p.probeChannel(ctx, s, true)
}

// Probe tests an unsaved station without writing library or health records.
func (p *Relay) Probe(ctx context.Context, s model.Station) (model.Health, error) {
	return p.probeChannel(ctx, s, false)
}
func (p *Relay) check(ctx context.Context, s model.Station, persist bool) (model.Health, error) {
	save := func(h model.Health) error {
		if !persist {
			return nil
		}
		return p.Store.SaveHealth(h)
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	h := model.Health{StationID: s.ID, VariantID: s.VariantID, Checked: time.Now().UTC(), Codec: s.Codec, Bitrate: s.Bitrate}
	if old, e := p.Store.Health(s.ID); e == nil {
		h.LastSuccess = old.LastSuccess
	}
	fail := func(message string) (model.Health, error) { h.Message = message; return h, save(h) }
	if e := ValidateURL(s.URL); e != nil {
		return fail("Unsafe upstream URL")
	}
	if kind := adaptiveKind(s.URL, "", s.HLS); kind != "" {
		return p.checkAdaptive(ctx, s, h, kind, save)
	}
	req, e := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	if e != nil {
		return fail("Invalid upstream URL")
	}
	req.Header.Set("User-Agent", "RetroRadio/0.2 health check")
	res, e := p.request(req)
	if e != nil {
		if errors.Is(e, ErrUnsafeTarget) {
			return fail("Blocked address: check local DNS overrides")
		}
		return fail("Connection failed or timed out")
	}
	defer res.Body.Close()
	h.Status = res.StatusCode
	h.ContentType = res.Header.Get("Content-Type")
	h.FinalURL = res.Request.URL.String()
	h.HTTPS = res.Request.URL.Scheme == "https"
	if res.StatusCode != 200 && res.StatusCode != 206 {
		return fail("Upstream returned HTTP " + strconv.Itoa(res.StatusCode))
	}
	content := strings.ToLower(strings.Split(h.ContentType, ";")[0])
	switch content {
	case "audio/mpeg", "audio/mp3":
		h.Codec = "MP3"
	case "audio/aac", "audio/aacp":
		h.Codec = "AAC"
	case "audio/flac", "audio/x-flac":
		h.Codec = "FLAC"
	case "audio/ogg", "application/ogg":
		h.Codec = "OGG"
	}
	if res.Header.Get("Icy-Br") != "" {
		if n, e := strconv.Atoi(res.Header.Get("Icy-Br")); e == nil && n > 0 {
			h.Bitrate = n
		}
	}
	if kind := adaptiveKind(s.URL, content, s.HLS); kind != "" {
		res.Body.Close()
		return p.checkAdaptive(ctx, s, h, kind, save)
	}
	if content == "audio/x-scpls" {
		return fail("PLS playlist is not supported")
	}

	if !strings.HasPrefix(content, "audio/") && content != "application/octet-stream" {
		return fail("Upstream did not return audio")
	}
	buf := make([]byte, 1024)
	n, e := io.ReadFull(res.Body, buf)
	if n == 0 || e != nil && e != io.ErrUnexpectedEOF {
		return fail("No audio received")
	}
	h.Working = true
	h.LastSuccess = h.Checked
	h.Message = "Audio received"
	return h, save(h)
}
