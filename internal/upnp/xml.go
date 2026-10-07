// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"retroradio.local/server/internal/content"
	"strconv"
	"strings"
)

type device struct {
	Name     string    `xml:"friendlyName"`
	Services []service `xml:"serviceList>service"`
	Devices  []device  `xml:"deviceList>device"`
}
type service struct {
	Type    string `xml:"serviceType"`
	Control string `xml:"controlURL"`
}

var directoryType = regexp.MustCompile(`^urn:schemas-upnp-org:service:ContentDirectory:[1-9][0-9]*$`)

func directory(d device) (service, bool) {
	for _, s := range d.Services {
		if directoryType.MatchString(s.Type) && s.Control != "" {
			return s, true
		}
	}
	for _, child := range d.Devices {
		if s, ok := directory(child); ok {
			return s, true
		}
	}
	return service{}, false
}

type didlCredit struct {
	Name string `xml:",chardata"`
	Role string `xml:"role,attr"`
}

type didlEntry struct {
	ID        string       `xml:"id,attr"`
	Parent    string       `xml:"parentID,attr"`
	Title     string       `xml:"title"`
	Class     string       `xml:"class"`
	Artists   []didlCredit `xml:"artist"`
	Authors   []didlCredit `xml:"author"`
	Composer  string       `xml:"composer"`
	Genres    []string     `xml:"genre"`
	Date      string       `xml:"date"`
	Creator   string       `xml:"creator"`
	Album     string       `xml:"album"`
	Art       string       `xml:"albumArtURI"`
	Resources []struct {
		URL      string `xml:",chardata"`
		Protocol string `xml:"protocolInfo,attr"`
		Bitrate  string `xml:"bitrate,attr"`
		Duration string `xml:"duration,attr"`
	} `xml:"res"`
}

// DIDL can contain several artist elements with different roles. Prefer the
// recording's performer and keep writing credits separate from its display name.
func (e didlEntry) credits() (string, string) {
	best := 0
	var performers, composers []string
	add := func(list []string, name string) []string {
		name = strings.TrimSpace(name)
		if name == "" {
			return list
		}
		for _, old := range list {
			if strings.EqualFold(old, name) {
				return list
			}
		}
		return append(list, name)
	}
	composers = add(composers, e.Composer)
	for _, author := range e.Authors {
		switch strings.ToLower(strings.TrimSpace(author.Role)) {
		case "composer", "writer", "songwriter", "lyricist":
			composers = add(composers, author.Name)
		}
	}
	for _, artist := range e.Artists {
		role := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(artist.Role), " ", ""))
		rank := 0
		switch role {
		case "composer", "writer", "songwriter", "lyricist":
			composers = add(composers, artist.Name)
			continue
		case "performer", "artist", "leadartist":
			rank = 5
		case "albumartist":
			rank = 3
		case "":
			rank = 4
		case "orchestra", "ensemble", "band":
			rank = 2
		default:
			rank = 1
		}
		if strings.TrimSpace(artist.Name) == "" {
			continue
		}
		if rank > best {
			best = rank
			performers = nil
		}
		if rank == best {
			performers = add(performers, artist.Name)
		}
	}
	// dc:creator is ambiguous and often the composer. Only retain the legacy
	// fallback when no role-bearing artist or composer credits were supplied.
	if len(performers) == 0 && len(e.Artists) == 0 && len(composers) == 0 {
		performers = add(performers, e.Creator)
	}
	return strings.Join(performers, ", "), strings.Join(composers, ", ")
}

func parseDIDL(raw string) ([]content.Item, error) {
	// Preserve the server's order, including mixed folders and items.
	dec := xml.NewDecoder(strings.NewReader(raw))
	first, e := dec.Token()
	if e != nil {
		return nil, e
	}
	for {
		if _, ok := first.(xml.StartElement); ok {
			break
		}
		first, e = dec.Token()
		if e != nil {
			return nil, e
		}
	}
	root := first.(xml.StartElement)
	if root.Name.Local != "DIDL-Lite" {
		return nil, errors.New("invalid DIDL root")
	}
	out := []content.Item{}
	for {
		t, e := dec.Token()
		if e != nil {
			return nil, e
		}
		if end, ok := t.(xml.EndElement); ok && end.Name == root.Name {
			break
		}
		start, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != "container" && start.Name.Local != "item" {
			if e = dec.Skip(); e != nil {
				return nil, e
			}
			continue
		}
		var entry didlEntry
		if e = dec.DecodeElement(&entry, &start); e != nil {
			return nil, e
		}
		if entry.ID == "" || len(entry.ID) > 2048 || entry.Title == "" {
			return nil, errors.New("invalid UPnP entry")
		}
		item := content.Item{ID: entry.ID, ParentID: entry.Parent, Title: entry.Title, Source: "upnp", Kind: content.PlayableItem}
		item.Artist, item.Composer = entry.credits()
		item.GenreName = strings.Join(entry.Genres, ", ")
		item.Date = strings.TrimSpace(entry.Date)
		item.Album = entry.Album
		item.ArtURL = strings.TrimSpace(entry.Art)
		if start.Name.Local == "container" {
			item.Kind = content.Folder
			item.Genre = entry.Class == "object.container.genre.musicGenre"
		} else if !strings.HasPrefix(entry.Class, "object.item.audioItem") { // Keep a slot for non-audio objects so TotalMatches stays exact.
			item.Resources = nil
		} else {
			for _, r := range entry.Resources {
				if item.Duration == "" {
					item.Duration = r.Duration
				}
				p := strings.SplitN(r.Protocol, ":", 4)
				if len(p) != 4 {
					continue
				}
				mime := strings.ToLower(strings.Split(p[2], ";")[0])
				codec := ""
				switch mime {
				case "audio/mpeg", "audio/mp3":
					codec = "MP3"
				case "audio/flac", "audio/x-flac":
					codec = "FLAC"
				case "audio/aac", "audio/aacp", "audio/x-aac":
					codec = "AAC"
				}
				b, _ := strconv.Atoi(r.Bitrate)
				if b < 0 {
					b = 0
				}
				item.Resources = append(item.Resources, content.Resource{URL: strings.TrimSpace(r.URL), Protocol: p[0], MIME: mime, Codec: codec, Bitrate: b / 125})
			}
		}
		out = append(out, item)
	}
	// Consume the remainder to reject truncated/extra documents.
	for {
		t, e := dec.Token()
		if e != nil {
			if e == io.EOF {
				break
			}
			return nil, e
		}
		if _, ok := t.(xml.StartElement); ok {
			return nil, errors.New("extra DIDL root")
		}
	}
	return out, nil
}
