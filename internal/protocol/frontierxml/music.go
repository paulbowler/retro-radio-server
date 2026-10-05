// SPDX-License-Identifier: GPL-3.0-only
package frontierxml

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"strconv"
	"strings"
)

func musicLink(base, object string) string {
	return base + "navXML.asp?music=" + base64.RawURLEncoding.EncodeToString([]byte(object))
}
func (h *Handler) musicItems(ctx context.Context, base string, q url.Values, caps model.Capabilities) ([]Item, int, error) {
	root := base + "navXML.asp?gofile=MyMusic"
	previous := Item{Type: "Previous", Previous: root, PreviousBackup: root}
	if h.Music == nil {
		return nil, 0, errors.New("music server not configured")
	}
	if q.Get("sSearchtype") == "3" {
		play, e := h.Music.Resolve(ctx, q.Get("Search"), caps)
		if e != nil {
			return nil, 0, e
		}
		previous.Previous = musicLink(base, play.Item.ParentID)
		previous.PreviousBackup = previous.Previous
		playURL := h.Base + "/stream/upnp/" + play.Item.PlaybackID
		if play.Direct {
			playURL = play.Resource.URL
		}
		description := []string{}
		for _, detail := range []string{play.Item.Artist, play.Item.Album, play.Item.Duration} {
			if detail != "" {
				description = append(description, detail)
			}
		}
		logo := ""
		if play.Item.ArtURL != "" {
			logo = h.Base + "/artwork/upnp/" + play.Item.PlaybackID + ".jpg"
		}
		// Reuse the established station lookup schema. No new Pure wire item types.
		item := Item{Type: "Station", ID: play.Item.PlaybackID, Name: play.Item.Title, URL: playURL, Desc: strings.Join(description, " · "), Logo: text(logo), Format: "Music", Mime: play.Codec(), Bitrate: play.Bitrate(), Reliability: 5}
		return []Item{previous, item}, 1, nil
	}
	if !q.Has("music") {
		previous.Previous = base + "loginXML.asp?gofile="
		previous.PreviousBackup = previous.Previous
		link := musicLink(base, "0")
		return []Item{previous, {Type: "Dir", Title: "MinimServer", Dir: link, Backup: link}}, 1, nil
	}
	object, e := base64.RawURLEncoding.DecodeString(q.Get("music"))
	if e != nil || len(object) == 0 || len(object) > 2048 {
		return nil, 0, errors.New("invalid music folder")
	}
	start, end := 1, 24
	if q.Has("startItems") {
		start, e = strconv.Atoi(q.Get("startItems"))
		if e != nil {
			return nil, 0, e
		}
	}
	if q.Has("endItems") {
		end, e = strconv.Atoi(q.Get("endItems"))
		if e != nil {
			return nil, 0, e
		}
	} else if start > 1 {
		end = start + 23
	}
	if start < 1 || start > 1000001 || end < start || end-start+1 > 100 {
		return nil, 0, errors.New("invalid music page")
	}
	page, e := h.Music.Browse(ctx, string(object), start-1, end-start+1)
	if e != nil {
		return nil, 0, e
	}
	if page.ParentID != "" && page.ParentID != "-1" {
		previous.Previous = musicLink(base, page.ParentID)
		previous.PreviousBackup = previous.Previous
	}
	items := []Item{previous}
	for _, entry := range page.Items {
		if entry.Kind == content.Folder {
			link := musicLink(base, entry.ID)
			items = append(items, Item{Type: "Dir", Title: entry.Title, Dir: link, Backup: link})
		} else {
			items = append(items, Item{Type: "Station", ID: entry.PlaybackID, Name: entry.Title})
		}
	}
	return items, page.Total, nil
}
