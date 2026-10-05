// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"retroradio.local/server/internal/content"
	"strconv"
)

type musicEntry struct {
	Title, BrowseURL, PlayURL       string
	Artist, Album, Duration, ArtURL string
	Folder                          bool
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
	v.MusicRootPage = true
	if a.Music == nil {
		v.Message = "Looking for your music…"
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
	v.MusicRootPage = object == "0"
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
		v.Message = "Your music is temporarily unavailable. Please try again shortly."
		v.Error = true
		return
	}
	if object == "0" && result.Total == 0 {
		v.Message = "Looking for your music…"
		return
	}
	v.MusicConnected = true
	v.MusicTitle = result.Title
	if !v.MusicRootPage {
		v.Title = result.Title
	}
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
				entry.PlayURL = "/stream/upnp/" + item.PlaybackID + "?listener=web"
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
