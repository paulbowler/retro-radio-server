// SPDX-License-Identifier: GPL-3.0-only
package frontierxml

import (
	"net/url"
	"retroradio.local/server/internal/model"
	"strconv"
)

func episodeItem(base string, ep model.Episode, p model.Podcast, full bool) Item {
	item := Item{Type: "ShowEpisode", EpisodeID: ep.ID, ShowName: p.Title, EpisodeName: ep.Title}
	if full {
		item.EpisodeURL = base + "/episode/" + ep.ID
		item.ShowDesc = ep.Description
		if description := []rune(item.ShowDesc); len(description) > 256 {
			item.ShowDesc = "[truncated]" + string(description[:256])
		}
		item.ShowMime = ep.Codec
		item.ShowFormat = "Podcast"
		item.Logo = text("")
	}
	return item
}
func (h *Handler) podcastItems(base string, q url.Values) ([]Item, int, error) {
	root := base + "navXML.asp?gofile=Podcasts"
	items := []Item{{Type: "Previous", Previous: root, PreviousBackup: root}}
	entries := []Item{}
	if q.Get("sSearchtype") == "5" {
		ep, e := h.Store.Episode(q.Get("Search"))
		if e != nil {
			return nil, 0, e
		}
		p, e := h.Store.Podcast(ep.PodcastID)
		if e != nil {
			return nil, 0, e
		}
		parent := base + "navXML.asp?podcast=" + p.ID
		items[0] = Item{Type: "Previous", Previous: parent, PreviousBackup: parent}
		return append(items, episodeItem(h.Base, ep, p, true)), 1, nil
	}
	if id := q.Get("podcast"); id != "" {
		p, e := h.Store.Podcast(id)
		if e != nil {
			return nil, 0, e
		}
		episodes, e := h.Store.Episodes(id)
		if e != nil {
			return nil, 0, e
		}
		for _, ep := range episodes {
			entries = append(entries, episodeItem(h.Base, ep, p, true))
		}
	} else {
		items[0] = Item{Type: "Previous", Previous: base + "loginXML.asp?gofile=", PreviousBackup: base + "loginXML.asp?gofile="}
		podcasts, e := h.Store.Podcasts()
		if e != nil {
			return nil, 0, e
		}
		for _, p := range podcasts {
			link := base + "navXML.asp?podcast=" + p.ID
			entries = append(entries, Item{Type: "ShowOnDemand", ShowID: p.ID, ShowTitle: p.Title, ShowURL: link, ShowBackup: link})
		}
	}
	start, end := 1, len(entries)
	if raw := q.Get("startItems"); raw != "" {
		start, _ = strconv.Atoi(raw)
	}
	if raw := q.Get("endItems"); raw != "" {
		end, _ = strconv.Atoi(raw)
	}
	if start < 1 {
		start = 1
	}
	if end > len(entries) {
		end = len(entries)
	}
	for i, item := range entries {
		if i+1 >= start && i+1 <= end {
			items = append(items, item)
		}
	}
	return items, len(entries), nil
}
