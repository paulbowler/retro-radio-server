// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"net/http"
	"retroradio.local/server/internal/artwork"
	"strings"
	"sync"
)

// ServeRadioArtwork is public like audio playback: legacy radios cannot supply
// management credentials. Only a stored station can select the checked source.
func (a *App) ServeRadioArtwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.URL.Path == "/artwork/agent-fm.jpg" {
		serveFallbackArtwork(w, r, true)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/artwork/"), ".jpg")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	r = r.Clone(r.Context())
	r.SetPathValue("station", id)
	a.serveStationArtwork(w, r, true)
}

func radioJPEG(data []byte) ([]byte, error) { return artwork.RadioJPEG(data) }

const fallbackArtworkURL = "/static/retro-radio-logo.png"

var fallbackRadioJPEG = sync.OnceValues(func() ([]byte, error) {
	data, err := assets.ReadFile("static/retro-radio-logo.png")
	if err != nil {
		return nil, err
	}
	return radioJPEG(data)
})

func serveFallbackArtwork(w http.ResponseWriter, r *http.Request, radio bool) {
	data, err := assets.ReadFile("static/retro-radio-logo.png")
	kind := "image/png"
	if radio {
		data, err = fallbackRadioJPEG()
		kind = "image/jpeg"
	}
	if err != nil {
		http.Error(w, "artwork unavailable", http.StatusInternalServerError)
		return
	}
	// Retry the station's own artwork soon, rather than caching a temporary outage.
	w.Header().Set("Cache-Control", "private, max-age=300")
	writeArtwork(w, r, data, kind)
}
