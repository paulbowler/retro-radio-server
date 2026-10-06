// SPDX-License-Identifier: GPL-3.0-only
package frontierxml

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
	root := base + "navXML.asp?gofile=Music"
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
		var play content.Playback
		playURL := ""
		if strings.HasPrefix(playbackID, "all_") {
			var selection musicSelection
			raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(playbackID, "all_"))
			sequential, ok := h.Music.(content.SequentialProvider)
			if err != nil || json.Unmarshal(raw, &selection) != nil || !ok {
				return nil, 0, errors.New("invalid music selection")
			}
			var session string
			play, session, e = sequential.StartSequence(ctx, selection.Folder, selection.Start, caps)
			playURL = h.Base + "/stream/upnp-queue/" + session
		} else {
			play, e = h.Music.Resolve(ctx, playbackID, caps)
			playURL = h.Base + "/stream/upnp/" + play.Item.PlaybackID
		}
		if e != nil {
			return nil, 0, e
		}
		previous.Previous = musicLink(base, play.Item.ParentID)
		previous.PreviousBackup = previous.Previous

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
	// Play All is about the listed tracks, not album tags or folder names.
	var tracks []content.Item
	if sequential, ok := h.Music.(content.SequentialProvider); ok {
		tracks, e = sequential.TrackList(ctx, string(object))
		if e != nil {
			return nil, 0, e
		}
	}
	offset, count := start-1, end-start+1
	if len(tracks) > 0 {
		if offset > 0 {
			offset--
		} else {
			count--
		}
	}
	// Browse metadata even when the requested page contains only Play All.
	page, e := h.Music.Browse(ctx, string(object), offset, max(1, count))
	if count == 0 {
		page.Items = nil
	}
	if e != nil {
		return nil, 0, e
	}
	if page.ParentID != "" && page.ParentID != "-1" {
		previous.Previous = musicLink(base, page.ParentID)
		previous.PreviousBackup = previous.Previous
	}
	items := []Item{previous}
	if len(tracks) > 0 && start == 1 {
		id, err := h.musicStationID(sequenceSelection(string(object), ""))
		if err != nil {
			return nil, 0, err
		}
		items = append(items, Item{Type: "Station", ID: id, Name: "[Play All]"})
	}
	for _, entry := range page.Items {
		if entry.Kind == content.Folder {
			link := musicLink(base, entry.ID)
			items = append(items, Item{Type: "Dir", Title: entry.Title, Dir: link, Backup: link})
		} else {
			playback := entry.PlaybackID
			if len(tracks) > 0 {
				playback = sequenceSelection(string(object), entry.ID)
			}
			id, e := h.musicStationID(playback)
			if e != nil {
				return nil, 0, e
			}
			items = append(items, Item{Type: "Station", ID: id, Name: entry.Title})
		}
	}
	total := page.Total
	if len(tracks) > 0 {
		total++
	}
	return items, total, nil
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

type musicSelection struct{ Folder, Start string }

func sequenceSelection(folder, start string) string {
	b, _ := json.Marshal(musicSelection{Folder: folder, Start: start})
	return "all_" + base64.RawURLEncoding.EncodeToString(b)
}
