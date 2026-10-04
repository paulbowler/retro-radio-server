// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"strings"
	"time"
)

type radioOverview struct {
	Device     model.Device
	Favourites int
	Playing    *delivery.Active
	Image      string
}

func listeningDuration(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "Just started"
	}
	minutes := int(d / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("Listening for %d min", minutes)
	}
	return fmt.Sprintf("Listening for %d hr %d min", minutes/60, minutes%60)
}

// Artwork is addressed by a saved station ID, never an arbitrary URL. The
// delivery client pins checked public addresses and validates every redirect.
func (a *App) stationArtwork(w http.ResponseWriter, r *http.Request) {
	a.serveStationArtwork(w, r, false)
}
func (a *App) serveStationArtwork(w http.ResponseWriter, r *http.Request, radio bool) {
	s, err := a.Store.Station(r.PathValue("station"), false)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.serveArtwork(w, r, s, radio)
}
func (a *App) candidateArtwork(w http.ResponseWriter, r *http.Request) {
	candidate, err := a.Store.Candidate(r.PathValue("uuid"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.serveArtwork(w, r, model.CandidateStation(candidate), false)
}
func (a *App) serveArtwork(w http.ResponseWriter, r *http.Request, s model.Station, radio bool) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if s.Favicon == "" {
		s.Favicon, _ = a.Catalogue.ArtworkForStation(ctx, s)
		if s.Favicon != "" && s.ID != "" {
			_, _ = a.Store.DB.Exec(`UPDATE stations SET favicon=? WHERE id=?`, s.Favicon, s.ID)
		}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.Favicon, nil)
	if err != nil || delivery.ValidateURL(s.Favicon) != nil {
		serveFallbackArtwork(w, r, radio)
		return
	}
	client := a.ArtworkClient
	if client == nil {
		client = delivery.NewClient()
	}
	var res *http.Response
	if u, e := url.Parse(s.Favicon); e == nil && u.Scheme == "http" && (u.Port() == "" || u.Port() == "80") {
		u.Scheme = "https"
		u.Host = strings.TrimSuffix(u.Host, ":80")
		secure, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		res, err = client.Do(secure)
		if res != nil && res.StatusCode != 200 {
			res.Body.Close()
			res = nil
		}
	}
	if res == nil {
		res, err = client.Do(req)
	}
	if err != nil {
		serveFallbackArtwork(w, r, radio)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		serveFallbackArtwork(w, r, radio)
		return
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		serveFallbackArtwork(w, r, radio)
		return
	}
	kind := http.DetectContentType(data)
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon", "image/vnd.microsoft.icon":
	default:
		serveFallbackArtwork(w, r, radio)
		return
	}
	if radio {
		data, err = radioJPEG(data)
		if err != nil {
			serveFallbackArtwork(w, r, radio)
			return
		}
		kind = "image/jpeg"
	}
	writeArtwork(w, r, data, kind)
}

func writeArtwork(w http.ResponseWriter, r *http.Request, data []byte, kind string) {
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Header().Set("Content-Type", kind)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	if r.Method != "HEAD" {
		w.Write(data)
	}
}

func stationArtworkURL(c card) string {
	if c.Station.ID != "" {
		return "/stations/" + c.Station.ID + "/artwork"
	}
	if c.Station.Favicon != "" && c.Station.RBUUID != "" {
		return "/stations/candidate/" + c.Station.RBUUID + "/artwork"
	}
	return fallbackArtworkURL
}
