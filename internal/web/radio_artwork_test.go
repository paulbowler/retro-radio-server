// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/store"
	"strconv"
	"testing"
)

func TestRadioArtworkPublicHTTPJPEGAndHEAD(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "artwork.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	_, err = s.DB.Exec(`UPDATE stations SET favicon='https://1.1.1.1/logo.png' WHERE id='1001'`)
	if err != nil {
		t.Fatal(err)
	}
	src := image.NewNRGBA(image.Rect(0, 0, 320, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 320; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	var pngData bytes.Buffer
	png.Encode(&pngData, src)
	calls := 0
	a := &App{Store: s, Relay: delivery.New(s), User: "admin", Password: "secret", ArtworkClient: &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" {
			t.Fatal("upstream HTTPS lost")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(bytes.NewReader(pngData.Bytes())), Request: r}, nil
	})}}
	mux := http.NewServeMux()
	mux.HandleFunc("/artwork/", a.ServeRadioArtwork)
	mux.Handle("/", a.Handler())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 401 {
		t.Fatal("management auth changed")
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "http://radio.local/artwork/1001.jpg", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || calls != 1 {
		t.Fatal(w.Code, w.Header(), calls)
	}
	img, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes()))
	if err != nil || img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 {
		t.Fatal(img, err)
	}
	red, green, blue, _ := img.At(64, 64).RGBA()
	if red < 60000 || green > 5000 || blue > 5000 {
		t.Fatal("station logo not retained")
	}
	red, green, blue, _ = img.At(64, 4).RGBA()
	if red < 60000 || green < 60000 || blue < 60000 {
		t.Fatal("aspect ratio not retained")
	}
	length := w.Body.Len()
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("HEAD", "/artwork/1001.jpg", nil))
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != strconv.Itoa(length) {
		t.Fatal(w.Code, w.Body.Len(), w.Header())
	}
	for _, path := range []string{"/artwork/missing.jpg", "/artwork/1001/other.jpg", "/artwork/1001.jpg?url=http://127.0.0.1"} {
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if path == "/artwork/1001.jpg?url=http://127.0.0.1" {
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
		} else if w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	_, err = s.DB.Exec(`UPDATE stations SET favicon='http://127.0.0.1/private'`)
	if err != nil {
		t.Fatal(err)
	}
	before := calls
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/artwork/1001.jpg", nil))
	if w.Code != 404 || calls != before {
		t.Fatal("private artwork allowed", w.Code, calls)
	}
}
func TestRadioJPEGRejectsOversizedAndInvalidImages(t *testing.T) {
	var encoded bytes.Buffer
	png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 4097, 1)))
	for _, data := range [][]byte{encoded.Bytes(), []byte("<svg><script>evil</script></svg>"), []byte("not an image")} {
		if _, err := radioJPEG(data); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
}
