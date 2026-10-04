// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
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
	s, err := a.Store.Station(r.PathValue("station"), false)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if id := stationArtworkUUID(s); s.Favicon == "" && id != "" {
		s.Favicon, _ = a.Catalogue.Artwork(ctx, id)
		if s.Favicon != "" {
			_, _ = a.Store.DB.Exec(`UPDATE stations SET favicon=? WHERE id=?`, s.Favicon, s.ID)
		}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.Favicon, nil)
	if err != nil || delivery.ValidateURL(s.Favicon) != nil {
		http.NotFound(w, r)
		return
	}
	res, err := a.ArtworkClient.Do(req)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		http.NotFound(w, r)
		return
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		http.NotFound(w, r)
		return
	}
	kind := http.DetectContentType(data)
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon", "image/vnd.microsoft.icon":
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(data)
}

func stationArtworkUUID(s model.Station) string {
	if s.RBUUID != "" {
		return s.RBUUID
	}
	for _, stream := range s.Variants {
		if stream.UUID != "" {
			return stream.UUID
		}
	}
	return ""
}
