// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
)

// Discover publisher artwork without relying on the station being in a directory.
// Stream bodies are never read: only the broadcaster's icy-url header is needed.
func (a *App) customArtwork(ctx context.Context, s model.Station) (store.Artwork, error) {
	pages := []string{s.Homepage}
	if res, err := a.artworkRequest(ctx, s, s.URL); err == nil {
		if res.StatusCode == http.StatusOK {
			base := s.URL
			if res.Request != nil {
				base = res.Request.URL.String()
			}
			pages = append(pages, artworkReference(base, res.Header.Get("icy-url")))
		}
		res.Body.Close()
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return store.Artwork{}, err
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = "/", "", "", ""
	u.ForceQuery = false
	pages = append(pages, u.String())
	seen := map[string]bool{}
	for _, page := range pages {
		if page == "" || seen[page] {
			continue
		}
		seen[page] = true
		res, err := a.artworkRequest(ctx, s, page)
		if err != nil {
			continue
		}
		base := page
		if res.Request != nil {
			base = res.Request.URL.String()
		}
		var data []byte
		if res.StatusCode == http.StatusOK && strings.Contains(strings.ToLower(res.Header.Get("Content-Type")), "text/html") {
			data, _ = io.ReadAll(io.LimitReader(res.Body, (256<<10)+1))
		}
		res.Body.Close()
		if len(data) > 256<<10 {
			continue
		}
		images := pageArtwork(base, data)
		images = append(images, artworkReference(base, "/favicon.ico"))
		for i, image := range images {
			if i >= 12 {
				break
			}
			if image == "" || seen[image] {
				continue
			}
			seen[image] = true
			logo, err := a.downloadArtwork(ctx, s, image)
			if err != nil || len(logo.Radio) == 0 {
				continue
			}
			// Do not attach a logo discovered for an old stream during a concurrent edit.
			if s.ID != "" {
				_, _ = a.Store.DB.Exec(`UPDATE stations SET favicon=?,homepage=? WHERE id=? AND source='custom' AND url=? AND favicon=''`, image, base, s.ID, s.URL)
			}
			return logo, nil
		}
	}
	return store.Artwork{}, fmt.Errorf("station artwork unavailable")
}

func artworkReference(base, raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.ResolveReference(ref).String()
}

func pageArtwork(base string, data []byte) []string {
	var logos, icons []string
	z := html.NewTokenizer(strings.NewReader(string(data)))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return append(logos, icons...)
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			attrs := map[string]string{}
			for _, attr := range t.Attr {
				attrs[attr.Key] = attr.Val
			}
			if t.Data == "meta" {
				name := strings.ToLower(attrs["property"])
				if name == "" {
					name = strings.ToLower(attrs["name"])
				}
				if name == "og:image" || name == "og:image:url" || name == "twitter:image" {
					logos = append(logos, artworkReference(base, attrs["content"]))
				}
			}
			if t.Data == "link" {
				for _, rel := range strings.Fields(strings.ToLower(attrs["rel"])) {
					if rel == "apple-touch-icon" || rel == "apple-touch-icon-precomposed" {
						logos = append(logos, artworkReference(base, attrs["href"]))
					} else if rel == "icon" {
						icons = append(icons, artworkReference(base, attrs["href"]))
					}
				}
			}
		}
	}
}

func (a *App) artworkRequest(ctx context.Context, s model.Station, raw string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	client := a.ArtworkClient
	if s.Source == "custom" && a.Relay != nil {
		client, err = a.Relay.StationResourceClient(s, raw)
		if err != nil {
			return nil, err
		}
	}
	if s.Source != "custom" || a.Relay == nil {
		if err := delivery.ValidateURL(raw); err != nil {
			return nil, err
		}
	} else if client == a.Relay.Client && a.ArtworkClient != nil {
		client = a.ArtworkClient
	}
	if client == nil {
		return nil, fmt.Errorf("artwork client unavailable")
	}
	return client.Do(req)
}
