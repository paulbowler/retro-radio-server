// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"sort"
	"strings"
)

func stationMatches(s model.Station, query, country, genre string) bool {
	if query != "" && !strings.Contains(strings.ToLower(s.Name), strings.ToLower(query)) {
		return false
	}
	if country != "" && countryCodes[strings.ToLower(strings.TrimSpace(s.Country))] != country {
		return false
	}
	return genre == "" || strings.Contains(strings.ToLower(s.Tags), strings.ToLower(genre))
}

func stationGenres(s *store.Store) []string {
	seen := map[string]bool{}
	for _, genre := range strings.Split("80s,90s,ambient,blues,chillout,classical,country,dance,disco,electronic,folk,hip hop,house,indie,jazz,latin,metal,news,oldies,pop,reggae,rock,soul,sports,talk,techno,trance", ",") {
		seen[genre] = true
	}
	stations, _ := s.Stations("")
	for _, station := range stations {
		for _, tag := range strings.Split(station.Tags, ",") {
			tag = strings.ToLower(strings.TrimSpace(tag))
			if tag != "" && len(tag) <= 100 {
				seen[tag] = true
			}
		}
	}
	genres := []string{}
	for genre := range seen {
		genres = append(genres, genre)
	}
	sort.Strings(genres)
	return genres
}
