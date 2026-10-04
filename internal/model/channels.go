// SPDX-License-Identifier: GPL-3.0-only
package model

import (
	"net/url"
	"regexp"
	"strings"
)

// Only technical suffixes are removed; programme and regional names are retained.
var audioSuffix = regexp.MustCompile(`(?i)(?:\s*[-–|·:]?\s*[\[(]?\s*(?:mp3|aac\+?|he-aac|aac-lc|flac|ogg|opus|hls|dash|\d{2,4}\s*(?:kbps|kbit/s|k|kb))\s*[\])]?)$`)

func ChannelName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	for {
		cleaned := strings.TrimSpace(audioSuffix.ReplaceAllString(name, ""))
		if cleaned == name || cleaned == "" {
			return name
		}
		name = cleaned
	}
}
func site(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}
func streamHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
func SameChannel(a, b Station) bool {
	if a.RBUUID != "" && a.RBUUID == b.RBUUID {
		return true
	}
	if a.Source == "custom" || b.Source == "custom" {
		return a.ID != "" && a.ID == b.ID
	}
	if strings.ToLower(ChannelName(a.Name)) != strings.ToLower(ChannelName(b.Name)) || a.Country == "" || countryIdentity(a.Country) != countryIdentity(b.Country) {
		return false
	}
	if a.Language != "" && b.Language != "" && !sharedLanguage(a.Language, b.Language) {
		return false
	}
	// A matching name alone is not sufficient for an automatic merge.
	if site(a.Homepage) != "" && site(a.Homepage) == site(b.Homepage) {
		return true
	}
	return streamHost(a.URL) != "" && streamHost(a.URL) == streamHost(b.URL)
}
func CandidateStation(c Candidate) Station {
	raw := c.Resolved
	if raw == "" {
		raw = c.URL
	}
	country := c.Country
	if country == "" {
		country = c.CountryCode
	}
	return Station{Favicon: c.Favicon, Name: c.Name, URL: raw, Homepage: c.Homepage, Country: country, Codec: c.Codec, Bitrate: c.Bitrate, HLS: c.HLS != 0, RBUUID: c.UUID, Source: "radio-browser", Tags: c.Tags, Language: c.Language}
}
func GroupCandidates(items []Candidate) []Candidate {
	out := []Candidate{}
	for _, c := range items {
		found := -1
		for i, g := range out {
			if SameChannel(CandidateStation(g), CandidateStation(c)) {
				found = i
				break
			}
		}
		if found < 0 {
			c.Name = ChannelName(c.Name)
			c.Variants = nil
			out = append(out, c)
			continue
		}
		duplicate := out[found].UUID == c.UUID
		for _, v := range out[found].Variants {
			duplicate = duplicate || v.UUID == c.UUID
		}
		if !duplicate {
			c.Variants = nil
			out[found].Variants = append(out[found].Variants, c)
		}
	}
	return out
}

type StreamVariant struct {
	ID        string `json:"id"`
	StationID string `json:"station_id"`
	URL       string `json:"-"`
	UUID      string `json:"radio_browser_uuid,omitempty"`
	Codec     string `json:"codec"`
	Bitrate   int    `json:"bitrate_kbps"`
	HLS       bool   `json:"adaptive"`
	Health    Health `json:"health"`
}

func (v StreamVariant) Station(channel Station) Station {
	channel.URL, channel.Codec, channel.Bitrate, channel.HLS = v.URL, v.Codec, v.Bitrate, v.HLS
	channel.RBUUID, channel.VariantID = v.UUID, v.ID
	channel.Variants = nil
	return channel
}

func countryIdentity(country string) string {
	name := strings.ToLower(strings.TrimSpace(country))
	switch name {
	case "gb", "uk", "united kingdom", "the united kingdom of great britain and northern ireland", "united kingdom of great britain and northern ireland":
		return "gb"
	case "us", "usa", "united states", "united states of america", "the united states of america":
		return "us"
	}
	return name
}
func sharedLanguage(a, b string) bool {
	normalize := func(language string) string {
		language = strings.ToLower(strings.TrimSpace(language))
		if language == "en" || language == "eng" || strings.HasSuffix(language, "english") {
			return "english"
		}
		return language
	}
	for _, left := range strings.Split(a, ",") {
		for _, right := range strings.Split(b, ",") {
			if normalize(left) == normalize(right) {
				return true
			}
		}
	}
	return false
}
