// SPDX-License-Identifier: GPL-3.0-only
package delivery

import (
	"context"
	"math"
	"net"
	"net/http"
	"regexp"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Playback ownership comes from a protocol radio ID or its address, never
// the web player's device preference. Do not trust forwarded IP headers on this public route.
func (p *Relay) beginPlayback(r *http.Request, s model.Station, h http.Header) uint64 {
	device := ""
	info, finite := r.Context().Value(playbackContext{}).(playbackInfo)
	explicitContentRadio := finite && (info.active.Kind == "Music" || info.active.Kind == "Podcast") && r.URL.Query().Get("radio") != ""
	if r.URL.Query().Get("listener") != "web" && (!strings.Contains(r.UserAgent(), "Mozilla/") || explicitContentRadio) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if d, err := p.Store.Device(r.URL.Query().Get("radio")); err == nil {
			device = d.ID
		}
		devices, _ := p.Store.Devices()
		for _, d := range devices {
			if r.URL.Query().Get("radio") != "" {
				break
			}
			if net.ParseIP(host) != nil && net.ParseIP(d.IP) != nil && net.ParseIP(host).Equal(net.ParseIP(d.IP)) {
				if device != "" {
					device = ""
					break
				}
				device = d.ID
			}
		}
	}
	bitrate := s.Bitrate
	if n, err := strconv.Atoi(h.Get("Icy-Br")); err == nil && n > 0 {
		bitrate = n
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	entry := Active{Station: s.Name, StationID: s.ID, Device: device, Since: time.Now().UTC(), Codec: s.Codec, Bitrate: bitrate, Genre: cleanMetadata(h.Get("Icy-Genre")), Description: cleanMetadata(h.Get("Icy-Description"))}
	entry.Kind = "Radio"
	if finite {
		entry.Kind = info.active.Kind
		entry.StationID = info.active.StationID
		entry.Station = info.active.Station
		entry.Description = info.active.Description
		entry.Artwork = info.active.Artwork
		if info.duration > 0 && device != "" {
			entry.Until = entry.Since.Add(info.duration)
		}
	}
	if device != "" {
		for id, old := range p.active {
			if old.Device == device && !old.Until.IsZero() {
				delete(p.active, id)
			}
		}
	}
	p.active[p.next] = entry

	return p.next
}

func (p *Relay) observeMetadata(key uint64, title string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.active[key]
	if !ok {
		return
	}
	a.Title = cleanMetadata(title)
	p.active[key] = a
}

func cleanMetadata(value string) string {
	value = strings.ToValidUTF8(value, "")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 512 {
		runes = runes[:512]
	}
	return string(runes)
}

var icyTitle = regexp.MustCompile(`(?s)StreamTitle='(.*?)';`)

// Observe bytes as they pass through, keeping the stream untouched. Audio and
// metadata may be split at any read boundary; metadata blocks are at most 4080B.
type icyObserver struct {
	interval, remaining, metadata int
	block                         []byte
	update                        func(string)
}

func newICYObserver(header string, update func(string)) *icyObserver {
	n, err := strconv.Atoi(header)
	if err != nil || n <= 0 || n > 16<<20 {
		return nil
	}
	return &icyObserver{interval: n, remaining: n, metadata: -1, update: update}
}
func (o *icyObserver) Write(data []byte) { o.Process(data, false) }
func (o *icyObserver) Process(data []byte, strip bool) []byte {
	if o == nil {
		return data
	}
	original := data
	var audio []byte
	for len(data) > 0 {
		if o.remaining > 0 {
			n := min(o.remaining, len(data))
			o.remaining -= n
			if strip {
				audio = append(audio, data[:n]...)
			}
			data = data[n:]
			continue
		}
		if o.metadata < 0 {
			o.metadata = int(data[0]) * 16
			o.block = o.block[:0]
			data = data[1:]
			if o.metadata == 0 {
				o.remaining = o.interval
				o.metadata = -1
			}
			continue
		}
		n := min(o.metadata, len(data))
		o.block = append(o.block, data[:n]...)
		data = data[n:]
		o.metadata -= n
		if o.metadata == 0 {
			if match := icyTitle.FindSubmatch(o.block); len(match) == 2 {
				o.update(string(match[1]))
			}
			o.remaining = o.interval
			o.metadata = -1
		}
	}
	if strip {
		return audio
	}
	return original
}

type playbackContext struct{}
type playbackInfo struct {
	active   Active
	duration time.Duration
}

// TrackMusic starts only when audio bytes have been delivered, never for artwork,
// metadata lookups, HEAD requests or failed streams. Attribution shares radio rules.
func (p *Relay) TrackMusic(r *http.Request, play content.Playback) func(bool) {
	description := strings.Join(nonempty(play.Item.Artist, play.Item.Album), " · ")
	artwork := ""
	if play.Item.ArtURL != "" {
		artwork = "/artwork/upnp/" + play.Item.PlaybackID + ".jpg"
	}
	info := playbackInfo{active: Active{Kind: "Music", StationID: play.Item.PlaybackID, Station: play.Item.Title, Description: description, Artwork: artwork}, duration: PlaybackDuration(play.Item.Duration)}
	r = r.WithContext(context.WithValue(r.Context(), playbackContext{}, info))
	key := p.beginPlayback(r, model.Station{Name: play.Item.Title, Codec: play.Codec(), Bitrate: play.Bitrate()}, http.Header{})
	p.mu.Lock()
	device := p.active[key].Device
	p.mu.Unlock()
	status := "radio not linked"
	if device != "" {
		status = "radio linked"
	}
	p.Store.Log(device, "Music stream connected", play.Item.Title+": "+status)
	return func(complete bool) {
		p.endPlayback(key, complete)
		status := "transfer interrupted"
		if complete {
			status = "transfer complete; duration estimate retained for linked radio"
		}
		p.Store.Log(device, "Music stream ended", play.Item.Title+": "+status)
	}
}
func nonempty(values ...string) []string {
	out := []string{}
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
func PlaybackDuration(raw string) time.Duration {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) == 0 || len(parts) > 3 {
		return 0
	}
	seconds := float64(0)
	for _, part := range parts {
		n, e := strconv.ParseFloat(part, 64)
		if e != nil || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return 0
		}
		seconds = seconds*60 + n
	}
	if seconds <= 0 || seconds > 86400 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}
func (p *Relay) endPlayback(key uint64, complete bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	current, ok := p.active[key]
	if ok && complete && current.Device != "" && !current.Until.IsZero() && time.Now().Before(current.Until) {
		return
	}
	delete(p.active, key)
}
