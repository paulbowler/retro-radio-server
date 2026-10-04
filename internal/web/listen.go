// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"net/http"
	"net/url"
)

func stationListenURL(c card) string {
	if c.Station.ID != "" {
		path := "/stream/" + url.PathEscape(c.Station.StreamID)
		if c.Context == "/devices" && c.Selected != "" {
			path += "?device=" + url.QueryEscape(c.Selected)
		}
		return path
	}
	return "/stations/listen?uuid=" + url.QueryEscape(c.Station.RBUUID)
}
func (a *App) listenCandidate(w http.ResponseWriter, r *http.Request) {
	candidate, err := a.Store.Candidate(r.URL.Query().Get("uuid"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Only cached directory records are accepted; clients cannot supply a target URL.
	station := candidateStationCard(candidateCard{Candidate: candidate}).Station
	a.Relay.ServeAudio(w, r, station)
}
