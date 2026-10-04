// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"fmt"
	"net/url"
	"retroradio.local/server/internal/catalogue"
	"strconv"
	"strings"
)

func stationPageURL(v view, offset int, trail string) string {
	params := url.Values{"mode": {v.Mode}, "q": {v.Query}, "country": {v.Country}, "genre": {v.Genre}, "offset": {strconv.Itoa(offset)}}
	if trail != "" {
		params.Set("trail", trail)
	}
	return "?" + params.Encode()
}
func setStationPagination(v *view) {
	v.PageNumber = v.Offset/catalogue.PageSize + 1
	v.HasPrevious = v.Offset > 0
	v.PreviousURL = stationPageURL(*v, v.Previous, "")
	v.NextURL = stationPageURL(*v, v.Next, "")
	v.DiscoveryURL = "/stations/discoveries?" + url.Values{"offset": {strconv.Itoa(v.Offset)}, "trail": {v.Trail}}.Encode()
}

// Live checks may skip directory entries, so retain the exact cursors for Back.
func discoveryTrail(raw string, offset int) ([]int, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 64 {
		return nil, fmt.Errorf("too many pages")
	}
	result := []int{}
	previous := -1
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || value >= offset || value <= previous {
			return nil, fmt.Errorf("invalid page history")
		}
		result = append(result, value)
		previous = value
	}
	return result, nil
}
func cursorTrail(offsets []int) string {
	parts := make([]string, len(offsets))
	for i, offset := range offsets {
		parts[i] = strconv.Itoa(offset)
	}
	return strings.Join(parts, ",")
}
