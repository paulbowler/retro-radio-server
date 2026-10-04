// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"retroradio.local/server/internal/model"
	"sort"
	"strings"
	"time"
)

func supported(s model.Station, c model.Capabilities) bool {
	if adaptiveKind(s.URL, "", s.HLS) != "" {
		return c.HTTP && c.MP3
	}
	return s.Codec == "MP3" && c.MP3 || s.Codec == "AAC" && c.AAC
}

// Bitrates are compared within formats; AAC gets a modest efficiency allowance.
// Availability and direct radio compatibility take priority over this estimate.
func quality(v model.StreamVariant) int {
	n := v.Bitrate
	if n < 0 {
		n = 0
	}
	if n > 1024 {
		n = 1024
	}
	if v.Codec == "AAC" {
		n = n * 14 / 10
	}
	return n
}
func RankedStreams(s model.Station, c model.Capabilities, preferred string) []model.StreamVariant {
	variants := s.Variants
	if len(variants) == 0 {
		variants = []model.StreamVariant{{ID: s.VariantID, UUID: s.RBUUID, URL: s.URL, Codec: s.Codec, Bitrate: s.Bitrate, HLS: s.HLS}}
	}
	out := []model.StreamVariant{}
	for _, v := range variants {
		if ValidateURL(v.URL) == nil && supported(v.Station(s), c) {
			out = append(out, v)
		}
	}
	tier := func(v model.StreamVariant) int {
		if !v.Health.Checked.IsZero() && !v.Health.Working && time.Since(v.Health.Checked) < 2*time.Minute {
			return 2
		}
		if adaptiveKind(v.URL, "", v.HLS) != "" {
			return 1
		}
		return 0
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.ID == preferred) != (b.ID == preferred) {
			return a.ID == preferred
		}
		if tier(a) != tier(b) {
			return tier(a) < tier(b)
		}
		if preferred == "low-data" {
			if (a.Bitrate > 0) != (b.Bitrate > 0) {
				return a.Bitrate > 0
			}
			if a.Bitrate != b.Bitrate {
				return a.Bitrate < b.Bitrate
			}
		}
		if quality(a) != quality(b) {
			return quality(a) > quality(b)
		}
		if a.Codec != b.Codec {
			return a.Codec == "AAC"
		}
		return a.ID < b.ID
	})
	return out
}
func ChooseStream(s model.Station, c model.Capabilities, preferred string) (model.Station, error) {
	choices := RankedStreams(s, c, preferred)
	if len(choices) == 0 {
		return s, errors.New("no compatible audio stream")
	}
	selected := choices[0].Station(s)
	selected.Variants = s.Variants
	return selected, nil
}
func RadioPlayURL(base string, s model.Station, d model.Device, preferred string) (string, model.Station, error) {
	selected, err := ChooseStream(s, d.Capabilities, preferred)
	if err != nil {
		return "", s, err
	}
	// Radios use the relay so playback can be observed even when the upstream
	// could otherwise be played directly. The radio ID survives reverse proxies.
	play := base + "/stream/" + s.StreamID + "?radio=" + url.QueryEscape(d.ID)
	if len(s.Variants) > 1 {
		play += "&device=" + url.QueryEscape(d.ID)
	}

	return play, selected, err
}
func (p *Relay) probeChannel(ctx context.Context, s model.Station, persist bool) (model.Health, error) {
	if len(s.Variants) == 0 {
		return p.check(ctx, s, persist)
	}
	var last model.Health
	for _, v := range RankedStreams(s, model.LegacyXML, "") {
		h, err := p.check(ctx, v.Station(s), persist)
		last = h
		if err != nil {
			return h, err
		}
		if h.Working {
			return h, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	if last.Checked.IsZero() {
		last = model.Health{StationID: s.ID, Checked: time.Now().UTC(), Message: "No compatible audio stream"}
		if persist {
			return last, p.Store.SaveHealth(last)
		}
	}
	return last, nil
}
func (p *Relay) openStream(r *http.Request, s model.Station) (model.Station, *http.Response, string, error) {
	caps := model.LegacyXML
	preferred := ""
	if device := r.URL.Query().Get("device"); device != "" {
		d, err := p.Store.Device(device)
		if err != nil {
			return s, nil, "", err
		}
		caps = d.Capabilities
		preferred = p.Store.Preferred(device, s.ID)
	}
	if preferred == "" && p.Store.Settings().Quality == "low" {
		preferred = "low-data"
	}
	last := errors.New("no compatible audio stream")
	for _, v := range RankedStreams(s, caps, preferred) {
		if r.Context().Err() != nil {
			return s, nil, "", r.Context().Err()
		}
		chosen := v.Station(s)
		if kind := adaptiveKind(chosen.URL, "", chosen.HLS); kind != "" {
			h, err := p.check(r.Context(), chosen, s.ID != "")
			if err == nil && h.Working {
				return chosen, nil, kind, nil
			}
			if err != nil {
				last = err
			}
			continue
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, chosen.URL, nil)
		if err != nil {
			last = err
			continue
		}
		req.Header.Set("User-Agent", "RetroRadio/0.2")
		for _, header := range []string{"Icy-MetaData", "Range", "If-Range"} {
			if value := r.Header.Get(header); value != "" {
				req.Header.Set(header, value)
			}
		}
		if s.ID != "" && r.Method == "GET" && r.Header.Get("Range") == "" {
			req.Header.Set("Icy-MetaData", "1")
		}
		res, err := p.request(req)
		h := model.Health{StationID: s.ID, VariantID: v.ID, Checked: time.Now().UTC(), Codec: v.Codec, Bitrate: v.Bitrate}
		if err != nil {
			last = err
			h.Message = "Connection failed"
			if s.ID != "" {
				_ = p.Store.SaveHealth(h)
			}
			continue
		}
		h.Status = res.StatusCode
		h.FinalURL = res.Request.URL.String()
		h.HTTPS = res.Request.URL.Scheme == "https"
		h.ContentType = res.Header.Get("Content-Type")
		ct := strings.ToLower(h.ContentType)
		if kind := adaptiveKind(h.FinalURL, ct, chosen.HLS); kind != "" {
			res.Body.Close()
			chosen.HLS = true
			health, e := p.check(r.Context(), chosen, s.ID != "")
			if e == nil && health.Working {
				return chosen, nil, kind, nil
			}
			if e != nil {
				last = e
			}
			continue
		}
		if (res.StatusCode != 200 && res.StatusCode != 206 && res.StatusCode != 416) || (res.StatusCode != 416 && !audioContent(ct)) {
			res.Body.Close()
			h.Message = "Stream unavailable"
			if s.ID != "" {
				_ = p.Store.SaveHealth(h)
			}
			last = errors.New(h.Message)
			continue
		}
		actual := chosen
		switch strings.ToLower(strings.Split(ct, ";")[0]) {
		case "audio/mpeg", "audio/mp3":
			actual.Codec = "MP3"
		case "audio/aac", "audio/aacp":
			actual.Codec = "AAC"
		case "audio/flac", "audio/x-flac":
			actual.Codec = "FLAC"
		case "audio/ogg", "application/ogg":
			actual.Codec = "OGG"
		}
		if res.StatusCode != 416 && !supported(actual, caps) {
			res.Body.Close()
			h.Message = "Audio format changed"
			if s.ID != "" {
				_ = p.Store.SaveHealth(h)
			}
			last = errors.New(h.Message)
			continue
		}
		chosen = actual
		h.Codec = actual.Codec
		if r.Method == "GET" && res.StatusCode != 416 {
			// Validate some audio before committing headers, allowing a different codec fallback.
			prefix := make([]byte, 1024)
			n, e := res.Body.Read(prefix)
			if n == 0 || e != nil && e != io.ErrUnexpectedEOF && e != io.EOF {
				res.Body.Close()
				h.Message = "No audio received"
				if s.ID != "" {
					_ = p.Store.SaveHealth(h)
				}
				last = errors.New(h.Message)
				continue
			}
			res.Body = &prefixedBody{Reader: io.MultiReader(bytes.NewReader(prefix[:n]), res.Body), Closer: res.Body}
		}
		h.Working = true
		h.LastSuccess = h.Checked
		h.Message = "Audio received"
		if s.ID != "" {
			_ = p.Store.SaveHealth(h)
		}
		return chosen, res, "", nil
	}
	return s, nil, "", last
}

type prefixedBody struct {
	io.Reader
	io.Closer
}
