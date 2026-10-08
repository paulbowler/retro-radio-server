// SPDX-License-Identifier: GPL-3.0-only
package upnp

import (
	"context"
	"io"
	"net/http"
	"retroradio.local/server/internal/artwork"
	"strconv"
	"strings"
	"time"
)

// ServeArtwork translates server-supplied albumArtURI to the existing radio JPEG format.
func (p *Provider) ServeArtwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/artwork/upnp/") || !strings.HasSuffix(r.URL.Path, ".jpg") {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/artwork/upnp/"), ".jpg")
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		http.Error(w, "music server busy", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	item, e := p.artworkMetadata(ctx, id)
	if e != nil || item.ArtURL == "" {
		http.NotFound(w, r)
		return
	}
	if e = p.scope.check(item.ArtURL); e != nil {
		http.NotFound(w, r)
		return
	}
	req, e := http.NewRequestWithContext(ctx, "GET", item.ArtURL, nil)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	res, e := p.client.Do(req)
	if e != nil {
		http.Error(w, "music artwork unavailable", 502)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		http.NotFound(w, r)
		return
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if e != nil || len(data) > 1<<20 {
		http.Error(w, "music artwork unavailable", 502)
		return
	}
	data, e = artwork.RadioJPEG(data)
	if e != nil {
		http.Error(w, "music artwork unavailable", 502)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "private, max-age=300")
	if r.Method == "GET" {
		w.Write(data)
	}
}
