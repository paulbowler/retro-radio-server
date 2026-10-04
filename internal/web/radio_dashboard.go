// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strings"
	"sync"
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
	key := artworkKey(s)
	cached, err := a.Store.Artwork(key)
	if err == nil {
		age := time.Since(cached.Updated)
		lifetime := 7 * 24 * time.Hour
		if len(cached.Data) == 0 {
			lifetime = 15 * time.Minute
		}
		if age > lifetime && time.Now().After(cached.RetryAfter) {
			a.refreshArtwork(key, s)
		}
		serveCachedArtwork(w, r, cached, radio)
		return
	}
	// Coalesce simultaneous first requests for the same logo.
	lock, _ := a.artworkLoads.LoadOrStore(key, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer func() { lock.(*sync.Mutex).Unlock(); a.artworkLoads.Delete(key) }()
	cached, err = a.Store.Artwork(key)
	if err != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		cached, err = a.fetchArtwork(ctx, s)
		if err != nil {
			cached = store.Artwork{Data: []byte{}, Radio: []byte{}}
		}
		_ = a.Store.SaveArtwork(key, cached)
		if len(cached.Data) > 0 && s.Favicon == "" && s.ID != "" {
			if resolved, e := a.Store.Station(s.ID, false); e == nil && artworkKey(resolved) != key {
				_ = a.Store.SaveArtwork(artworkKey(resolved), cached)
			}
		}
	}
	serveCachedArtwork(w, r, cached, radio)
}
func artworkKey(s model.Station) string {
	// Share downloaded bytes between search results, saved cards and radio JPEGs.
	raw := s.Favicon
	if raw == "" {
		raw = s.RBUUID + ":" + s.URL
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
}
func serveCachedArtwork(w http.ResponseWriter, r *http.Request, c store.Artwork, radio bool) {
	data, kind := c.Data, c.Kind
	if radio {
		data, kind = c.Radio, "image/jpeg"
	}
	if len(data) == 0 {
		serveFallbackArtwork(w, r, radio)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeArtwork(w, r, data, kind)
}
func (a *App) refreshArtwork(key string, s model.Station) {
	a.artworkMu.Lock()
	if a.artworkPending == nil {
		a.artworkPending = map[string]bool{}
	}
	if a.artworkPending[key] || len(a.artworkPending) >= 4 {
		a.artworkMu.Unlock()
		return
	}
	a.artworkPending[key] = true
	a.artworkMu.Unlock()
	go func() {
		defer func() { a.artworkMu.Lock(); delete(a.artworkPending, key); a.artworkMu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cached, err := a.fetchArtwork(ctx, s)
		if err != nil {
			// Keep the last good image during outages; defer the next retry.
			cached, err = a.Store.Artwork(key)
			if err != nil {
				return
			}
			cached.RetryAfter = time.Now().Add(15 * time.Minute)
		}
		_ = a.Store.SaveArtwork(key, cached)
	}()
}
func (a *App) fetchArtwork(ctx context.Context, s model.Station) (store.Artwork, error) {
	if s.Favicon == "" {
		s.Favicon, _ = a.Catalogue.ArtworkForStation(ctx, s)
		if s.Favicon != "" && s.ID != "" {
			_, _ = a.Store.DB.Exec(`UPDATE stations SET favicon=? WHERE id=?`, s.Favicon, s.ID)
		}
	}
	if delivery.ValidateURL(s.Favicon) != nil {
		return store.Artwork{}, fmt.Errorf("invalid artwork URL")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.Favicon, nil)
	if err != nil {
		return store.Artwork{}, err
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
		return store.Artwork{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return store.Artwork{}, fmt.Errorf("artwork HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return store.Artwork{}, fmt.Errorf("invalid artwork size")
	}
	kind := http.DetectContentType(data)
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon", "image/vnd.microsoft.icon":
	default:
		return store.Artwork{}, fmt.Errorf("unsupported artwork")
	}
	jpeg, _ := radioJPEG(data)
	return store.Artwork{Data: data, Kind: kind, Radio: jpeg}, nil
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
