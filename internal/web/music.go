// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/upnp"
	"strconv"
)

type musicEntry struct {
	Title, BrowseURL, PlayURL, Codec string
	Artist, Album, Duration, ArtURL  string
	Folder                           bool
}

func musicBrowseURL(id string, offset int) string {
	return musicPageURL(id, offset, "")
}
func musicPageURL(id string, offset int, trail string) string {
	link := "/music?object=" + url.QueryEscape(base64.RawURLEncoding.EncodeToString([]byte(id))) + "&offset=" + strconv.Itoa(offset)
	if trail != "" {
		link += "&trail=" + url.QueryEscape(trail)
	}
	return link
}
func (a *App) musicView(r *http.Request, v *view) {
	if r.URL.Query().Get("discover") == "1" {
		servers, e := upnp.Discover(r.Context())
		v.MusicServers = servers
		if e != nil {
			v.MusicDiscovery = "Discovery unavailable on this network. Manual configuration still works."
		} else if len(servers) == 0 {
			v.MusicDiscovery = "No music servers discovered. Multicast may be blocked; use a manual device-description link."
		} else {
			v.MusicDiscovery = "Discovered music servers. Configure a device-description link on the server to connect."
		}
	}
	if a.Music == nil {
		v.Message = "No music server configured. Add its device-description link to the server configuration and restart."
		return
	}
	object := "0"
	if raw := r.URL.Query().Get("object"); raw != "" {
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if e != nil || len(b) == 0 || len(b) > 2048 {
			v.Message = "That music folder link is invalid."
			v.Error = true
			return
		}
		object = string(b)
	}
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 0 || n > 1000000 {
			v.Message = "That music page is invalid."
			v.Error = true
			return
		}
		offset = n
	}
	trail, e := discoveryTrail(r.URL.Query().Get("trail"), offset)
	if e != nil {
		v.Message = "That music page history is invalid."
		v.Error = true
		return
	}
	result, e := a.Music.Browse(r.Context(), object, offset, 24)
	if e != nil {
		v.Message = "Music server unavailable. Check its address and connection, then retry. Radio and podcasts are still available."
		v.Error = true
		return
	}
	v.MusicConnected = true
	v.MusicTitle = result.Title
	v.MusicRoot = musicBrowseURL("0", 0)
	if result.ParentID != "" && result.ParentID != "-1" {
		v.MusicParent = musicBrowseURL(result.ParentID, 0)
	}
	for _, item := range result.Items {
		entry := musicEntry{Title: item.Title, Folder: item.Kind == content.Folder, Artist: item.Artist, Album: item.Album, Duration: item.Duration}
		if item.ArtURL != "" && item.PlaybackID != "" {
			entry.ArtURL = "/artwork/upnp/" + item.PlaybackID + ".jpg"
		}
		if entry.Folder {
			entry.BrowseURL = musicBrowseURL(item.ID, 0)
		} else {
			if item.PlaybackCodec != "" {
				entry.Codec = item.PlaybackCodec
				if item.Transcoded {
					entry.Codec = "FLAC → MP3"
				}
				entry.PlayURL = "/stream/upnp/" + item.PlaybackID
			}
		}
		v.MusicItems = append(v.MusicItems, entry)
	}
	v.PageNumber = offset/24 + 1
	if offset > 0 {
		v.HasPrevious = true
		n := offset - 24
		if n < 0 {
			n = 0
		}
		if len(trail) > 0 {
			n = trail[len(trail)-1]
			v.PreviousURL = musicPageURL(object, n, cursorTrail(trail[:len(trail)-1]))
		} else {
			v.PreviousURL = musicBrowseURL(object, n)
		}
	}
	if result.Returned > 0 && offset+result.Returned < result.Total {
		v.HasNext = true
		nextTrail := append(trail, offset)
		if len(nextTrail) > 64 {
			nextTrail = nextTrail[len(nextTrail)-64:]
		}
		v.NextURL = musicPageURL(object, offset+result.Returned, cursorTrail(nextTrail))
	}
}
