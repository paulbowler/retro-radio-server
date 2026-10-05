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

type didlEntry struct {
	ID        string `xml:"id,attr"`
	Parent    string `xml:"parentID,attr"`
	Title     string `xml:"title"`
	Class     string `xml:"class"`
	Artist    string `xml:"artist"`
	Creator   string `xml:"creator"`
	Album     string `xml:"album"`
	Art       string `xml:"albumArtURI"`
	Resources []struct {
		URL      string `xml:",chardata"`
		Protocol string `xml:"protocolInfo,attr"`
		Bitrate  string `xml:"bitrate,attr"`
		Duration string `xml:"duration,attr"`
	} `xml:"res"`
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
		item.Artist = entry.Artist
		if item.Artist == "" {
			item.Artist = entry.Creator
		}
		item.Album = entry.Album
		item.ArtURL = strings.TrimSpace(entry.Art)
		if start.Name.Local == "container" {
			item.Kind = content.Folder
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
