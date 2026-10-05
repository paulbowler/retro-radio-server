// SPDX-License-Identifier: GPL-3.0-only
package frontierxml

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"math/big"
	"net/url"
	"retroradio.local/server/internal/content"
	"retroradio.local/server/internal/model"
	"strconv"
	"strings"
	"time"
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
		stationID := q.Get("Search")
		playbackID, e := h.musicPlaybackID(stationID)
		if e != nil {
			return nil, 0, e
		}
		play, e := h.Music.Resolve(ctx, playbackID, caps)
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
		item := Item{Type: "Station", ID: stationID, Name: play.Item.Title, URL: playURL, Desc: strings.Join(description, " · "), Logo: text(logo), Format: "Radio", Mime: play.Codec(), Bitrate: play.Bitrate(), Reliability: 5}
		return []Item{previous, item}, 1, nil
	}
	object := []byte("0")
	if q.Has("music") {
		var e error
		object, e = base64.RawURLEncoding.DecodeString(q.Get("music"))
		if e != nil || len(object) == 0 || len(object) > 2048 {
			return nil, 0, errors.New("invalid music folder")
		}
	} else {
		previous.Previous = base + "loginXML.asp?gofile="
		previous.PreviousBackup = previous.Previous
	}

	var e error
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
			id, e := h.musicStationID(entry.PlaybackID)
			if e != nil {
				return nil, 0, e
			}
			items = append(items, Item{Type: "Station", ID: id, Name: entry.Title})
		}
	}
	return items, page.Total, nil
}

// Station IDs use the same numeric shape as the established radio catalogue.
// The media endpoint retains the provider's opaque token; this bounded mapping
// contains no source URLs or mirrored library metadata.
type musicStationID struct {
	playback string
	seen     time.Time
}

const musicIDBase = 1000000000
const musicIDLimit = 2000000000

func (h *Handler) isMusicStation(id string) bool {
	if strings.HasPrefix(id, "upnp_") {
		return true
	} // Existing in-flight menus.
	n, e := strconv.Atoi(id)
	return e == nil && n >= musicIDBase && n < musicIDLimit
}
func (h *Handler) musicPlaybackID(id string) (string, error) {
	if strings.HasPrefix(id, "upnp_") {
		return id, nil
	}
	h.musicMu.Lock()
	defer h.musicMu.Unlock()
	token, ok := h.musicIDs[id]
	if !ok || time.Since(token.seen) > 24*time.Hour {
		return "", errors.New("music selection expired; reopen the album")
	}
	token.seen = time.Now()
	h.musicIDs[id] = token
	return token.playback, nil
}
func (h *Handler) musicStationID(playback string) (string, error) {
	if playback == "" {
		return "", errors.New("music playback ID missing")
	}
	h.musicMu.Lock()
	defer h.musicMu.Unlock()
	if h.musicIDs == nil {
		h.musicIDs = make(map[string]musicStationID)
	}
	now := time.Now()
	for id, token := range h.musicIDs {
		if now.Sub(token.seen) > 24*time.Hour {
			delete(h.musicIDs, id)
			continue
		}
		if token.playback == playback {
			token.seen = now
			h.musicIDs[id] = token
			return id, nil
		}
	}
	if len(h.musicIDs) >= 4096 {
		oldest := ""
		for id, token := range h.musicIDs {
			if oldest == "" || token.seen.Before(h.musicIDs[oldest].seen) {
				oldest = id
			}
		}
		delete(h.musicIDs, oldest)
	}
	for {
		random, e := rand.Int(rand.Reader, big.NewInt(musicIDLimit-musicIDBase))
		if e != nil {
			return "", e
		}
		id := strconv.FormatInt(musicIDBase+random.Int64(), 10)
		if _, ok := h.musicIDs[id]; !ok {
			h.musicIDs[id] = musicStationID{playback: playback, seen: now}
			return id, nil
		}
	}
}
