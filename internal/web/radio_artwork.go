// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"bytes"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
)

// ServeRadioArtwork is public like audio playback: legacy radios cannot supply
// management credentials. Only a stored station can select the checked source.
func (a *App) ServeRadioArtwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
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

func radioJPEG(data []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || config.Width*config.Height > 4<<20 {
		return nil, http.ErrContentLength
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	const size = 128
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	bounds := src.Bounds()
	width, height := size, size
	if bounds.Dx() > bounds.Dy() {
		height = max(1, size*bounds.Dy()/bounds.Dx())
	} else {
		width = max(1, size*bounds.Dx()/bounds.Dy())
	}
	left, top := (size-width)/2, (size-height)/2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			pixel := color.RGBA{255, 255, 255, 255}
			if x >= left && x < left+width && y >= top && y < top+height {
				r, g, b, alpha := src.At(bounds.Min.X+(x-left)*bounds.Dx()/width, bounds.Min.Y+(y-top)*bounds.Dy()/height).RGBA()
				pixel = color.RGBA{uint8((r + 65535 - alpha) >> 8), uint8((g + 65535 - alpha) >> 8), uint8((b + 65535 - alpha) >> 8), 255}
			}
			dst.SetRGBA(x, y, pixel)
		}
	}
	var encoded bytes.Buffer
	err = jpeg.Encode(&encoded, dst, &jpeg.Options{Quality: 85})
	return encoded.Bytes(), err
}
