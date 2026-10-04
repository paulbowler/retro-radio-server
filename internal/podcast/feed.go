// SPDX-License-Identifier: GPL-3.0-only
// Package podcast reads public RSS/Atom feeds without downloading episode files.
package podcast

import (
	"encoding/xml"
	"errors"
	"html"
	"net/url"
	"path"
	"regexp"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"strings"
	"time"
)

var tags = regexp.MustCompile(`<[^>]*>`)

func plain(s string, limit int) string {
	s = strings.Join(strings.Fields(html.UnescapeString(tags.ReplaceAllString(s, " "))), " ")
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

type enclosure struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}
type link struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}
type entry struct {
	Title       string      `xml:"title"`
	GUID        string      `xml:"guid"`
	ID          string      `xml:"id"`
	Description string      `xml:"description"`
	Summary     string      `xml:"summary"`
	Content     string      `xml:"content"`
	Date        string      `xml:"pubDate"`
	Published   string      `xml:"published"`
	Updated     string      `xml:"updated"`
	Duration    string      `xml:"duration"`
	Enclosures  []enclosure `xml:"enclosure"`
	Links       []link      `xml:"link"`
}
type channel struct {
	Title       string  `xml:"title"`
	Description string  `xml:"description"`
	Author      string  `xml:"author"`
	Items       []entry `xml:"item"`
}
type feedXML struct {
	XMLName  xml.Name
	Channel  channel `xml:"channel"`
	Title    string  `xml:"title"`
	Subtitle string  `xml:"subtitle"`
	Author   struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Entries []entry `xml:"entry"`
}

func date(raw string) time.Time {
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, "Mon, 2 Jan 2006 15:04:05 -0700", "2006-01-02"} {
		if t, e := time.Parse(layout, strings.TrimSpace(raw)); e == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
func codec(raw, mime string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0])) {
	case "audio/mpeg", "audio/mp3", "audio/x-mpeg":
		return "MP3"
	case "audio/aac", "audio/aacp":
		return "AAC"
	}
	if mime != "" && mime != "application/octet-stream" {
		return ""
	}
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	switch strings.ToLower(path.Ext(u.Path)) {
	case ".mp3":
		return "MP3"
	case ".aac":
		return "AAC"
	}
	return ""
}
func Parse(data []byte, base string) (model.Podcast, []model.Episode, error) {
	var f feedXML
	if e := xml.Unmarshal(data, &f); e != nil {
		return model.Podcast{}, nil, e
	}
	p := model.Podcast{Feed: base}
	entries := f.Channel.Items
	switch f.XMLName.Local {
	case "rss":
		p.Title = f.Channel.Title
		p.Description = f.Channel.Description
		p.Author = f.Channel.Author
	case "feed":
		p.Title = f.Title
		p.Description = f.Subtitle
		p.Author = f.Author.Name
		entries = f.Entries
	default:
		return p, nil, errors.New("not an RSS or Atom feed")
	}
	p.Title = plain(p.Title, 200)
	p.Author = plain(p.Author, 160)
	p.Description = plain(p.Description, 6000)
	if p.Title == "" {
		return p, nil, errors.New("feed has no title")
	}
	origin, e := url.Parse(base)
	if e != nil {
		return p, nil, e
	}
	out := []model.Episode{}
	seen := map[string]bool{}
	for _, item := range entries {
		enclosures := item.Enclosures
		for _, l := range item.Links {
			if l.Rel == "enclosure" {
				enclosures = append(enclosures, enclosure{l.Href, l.Type})
			}
		}
		for _, enc := range enclosures {
			u, e := url.Parse(strings.TrimSpace(enc.URL))
			if e != nil || enc.URL == "" {
				continue
			}
			raw := origin.ResolveReference(u).String()
			format := codec(raw, enc.Type)
			if format == "" || delivery.ValidateURL(raw) != nil {
				continue
			}
			guid := strings.TrimSpace(item.GUID)
			if guid == "" {
				guid = strings.TrimSpace(item.ID)
			}
			if guid == "" {
				guid = raw
			}
			if len(guid) > 4096 || len(raw) > 4096 || seen[guid] {
				continue
			}
			seen[guid] = true
			title := plain(item.Title, 250)
			if title == "" {
				title = "Untitled episode"
			}
			desc := item.Description
			if desc == "" {
				desc = item.Summary
			}
			if desc == "" {
				desc = item.Content
			}
			published := date(item.Date)
			if published.IsZero() {
				published = date(item.Published)
			}
			if published.IsZero() {
				published = date(item.Updated)
			}
			duration := strings.TrimSpace(item.Duration)
			if len(duration) > 20 {
				duration = ""
			}
			out = append(out, model.Episode{GUID: guid, Title: title, Description: plain(desc, 800), URL: raw, Codec: format, Published: published, Duration: duration})
			break
		}
		if len(out) >= 200 {
			break
		}
	}
	if len(out) == 0 {
		return p, nil, errors.New("feed has no MP3 or AAC episodes")
	}
	return p, out, nil
}
