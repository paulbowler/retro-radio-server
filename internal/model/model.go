// SPDX-License-Identifier: GPL-3.0-only
package model

import "time"

type Capabilities struct {
	HTTP  bool `json:"supports_http"`
	HTTPS bool `json:"supports_https"`
	MP3   bool `json:"supports_mp3"`
	AAC   bool `json:"supports_aac"`
	DASH  bool `json:"supports_dash"`
	HLS   bool `json:"supports_hls"`
	ICY   bool `json:"supports_icy_metadata"`
}

// MP3 and AAC playback are owner-confirmed on the Pure ELAN IR5.
// Transport translation cannot fix codecs.
var LegacyXML = Capabilities{HTTP: true, MP3: true, AAC: true, ICY: true}

type Station struct {
	Homepage  string          `json:"homepage,omitempty"`
	VariantID string          `json:"-"`
	Variants  []StreamVariant `json:"streams,omitempty"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	URL       string          `json:"-"`
	StreamID  string          `json:"stream_id"`
	Codec     string          `json:"codec"`
	Bitrate   int             `json:"bitrate_kbps"`
	RBUUID    string          `json:"radio_browser_uuid,omitempty"`
	Source    string          `json:"source"`
	Country   string          `json:"country"`
	Tags      string          `json:"tags"`
	Language  string          `json:"language"`
	HLS       bool            `json:"hls"`
}
type Device struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Manufacturer string       `json:"manufacturer"`
	Model        string       `json:"model"`
	Protocol     string       `json:"protocol"`
	Firmware     string       `json:"firmware"`
	IP           string       `json:"ip"`
	LastSeen     time.Time    `json:"last_seen"`
	Capabilities Capabilities `json:"capabilities"`
}
type Event struct {
	Time   time.Time `json:"time"`
	Device string    `json:"device"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
}

// Candidate is cached catalogue information, not yet a managed station.
type Candidate struct {
	Homepage    string      `json:"homepage"`
	Variants    []Candidate `json:"variants,omitempty"`
	UUID        string      `json:"stationuuid"`
	Name        string      `json:"name"`
	URL         string      `json:"url"`
	Resolved    string      `json:"url_resolved"`
	Codec       string      `json:"codec"`
	Bitrate     int         `json:"bitrate"`
	Country     string      `json:"country"`
	CountryCode string      `json:"countrycode"`
	Language    string      `json:"language"`
	Tags        string      `json:"tags"`
	HLS         int         `json:"hls"`
}
type Health struct {
	VariantID   string    `json:"variant_id,omitempty"`
	Adaptive    string    `json:"adaptive,omitempty"`
	StationID   string    `json:"station_id"`
	Checked     time.Time `json:"checked_at"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	Status      int       `json:"http_status"`
	ContentType string    `json:"content_type"`
	FinalURL    string    `json:"final_url"`
	HTTPS       bool      `json:"https_required"`
	Bitrate     int       `json:"bitrate_kbps"`
	Codec       string    `json:"codec"`
	Working     bool      `json:"working"`
	Message     string    `json:"message"`
}
