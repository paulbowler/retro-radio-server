// SPDX-License-Identifier: GPL-3.0-only
package web

import "net/http"

// Installation metadata and branding contain no private radio or library data.
// Browsers may fetch these without management credentials when adding the app.
var publicAppAssets = map[string]string{
	"/app.webmanifest":                  "static/app.webmanifest",
	"/apple-touch-icon.png":             "static/app-icon-180.png",
	"/static/app-icon-32.png":           "static/app-icon-32.png",
	"/static/app-icon-152.png":          "static/app-icon-152.png",
	"/static/app-icon-167.png":          "static/app-icon-167.png",
	"/static/app-icon-192.png":          "static/app-icon-192.png",
	"/static/app-icon-512.png":          "static/app-icon-512.png",
	"/static/app-icon-maskable-512.png": "static/app-icon-maskable-512.png",
}

func serveAppAsset(w http.ResponseWriter, r *http.Request, path string) {
	data, err := assets.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	kind := "image/png"
	if path == "static/app.webmanifest" {
		kind = "application/manifest+json"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeArtwork(w, r, data, kind)
}
