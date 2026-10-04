// SPDX-License-Identifier: GPL-3.0-only
package frontierxml

import (
	"net/url"
	"retroradio.local/server/internal/model"
	"sort"
	"strings"
)

const uncategorised = "__uncategorised__"

// Country and genre menus are derived from current saved metadata, never copied
// into a second catalogue. Multi-tag stations appear in each genre.
func groupValues(s model.Station, group string) []string {
	values := []string{s.Country}
	if group == "genre" {
		values = strings.Split(s.Tags, ",")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			out = append(out, value)
			seen[key] = true
		}
	}
	if len(out) == 0 {
		return []string{uncategorised}
	}
	return out
}
func groupedItems(stations []model.Station, group, value, base string) []Item {
	if value != "" {
		out := []Item{}
		for _, s := range stations {
			for _, key := range groupValues(s, group) {
				if strings.EqualFold(key, value) {
					out = append(out, Item{Type: "Station", ID: s.ID, Name: s.Name})
					break
				}
			}
		}
		return out
	}
	labels := map[string]string{}
	for _, s := range stations {
		for _, label := range groupValues(s, group) {
			key := strings.ToLower(label)
			if _, ok := labels[key]; !ok {
				labels[key] = label
			}
		}
	}
	keys := []string{}
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []Item{}
	for _, key := range keys {
		title := labels[key]
		if key == uncategorised {
			title = "Uncategorised"
		}
		link := base + "navXML.asp?group=" + group + "&value=" + url.QueryEscape(key)
		out = append(out, Item{Type: "Dir", Title: title, Dir: link, Backup: link})
	}
	return out
}
