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
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strconv"
	"strings"
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
	_, err = s.DB.Exec(`UPDATE stations SET favicon='http://1.1.1.1/logo.png'`)
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/artwork/1001.jpg", nil))
	if w.Code != 200 {
		t.Fatal("HTTP image was not negotiated to HTTPS", w.Code)
	}
	_, err = s.DB.Exec(`UPDATE stations SET favicon='http://127.0.0.1/private'`)
	if err != nil {
		t.Fatal(err)
	}
	before := calls
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/artwork/1001.jpg", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || calls != before {
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

func TestCandidateArtworkAndCardPlaceholder(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "candidate-artwork.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	uuid := "11111111-1111-1111-1111-111111111111"
	candidate := model.Candidate{UUID: uuid, Name: "Test radio", URL: "https://1.1.1.1/audio", Codec: "MP3", Favicon: "https://1.1.1.1/logo.png"}
	s.CachePut("fixture", []model.Candidate{candidate})
	var pngData bytes.Buffer
	png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 32, 32)))
	app := &App{Store: s, Relay: delivery.New(s), ArtworkClient: &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(pngData.Bytes())), Request: r}, nil
	})}}
	h := app.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/stations/candidate/"+uuid+"/artwork", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal(w.Code, w.Header())
	}
	stations, _ := s.Stations("")
	if len(stations) != 1 {
		t.Fatal("image lookup added a station")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/stations/candidate/unknown/artwork?url=http://127.0.0.1", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, hasImage := range []bool{true, false} {
		if !hasImage {
			candidate.Favicon = ""
		}
		var html bytes.Buffer
		card := candidateStationCard(candidateCard{Candidate: candidate})
		if err = page.ExecuteTemplate(&html, "radio-station", card); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(html.String(), "station-thumb") || !strings.Contains(html.String(), "retro-radio-logo.png") || !strings.Contains(html.String(), ">Add</button>") {
			t.Fatal(html.String())
		}
		if strings.Contains(html.String(), "/stations/candidate/"+uuid+"/artwork") != hasImage {
			t.Fatal("incorrect image/placeholder", html.String())
		}
	}
}

func TestSharedArtworkFallback(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "fallback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	app := &App{Store: s, Relay: delivery.New(s)}
	h := app.Handler()
	original, err := assets.ReadFile("static/retro-radio-logo.png")
	if err != nil {
		t.Fatal(err)
	}
	radioLogo, err := fallbackRadioJPEG()
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(radioLogo))
	if err != nil || img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 {
		t.Fatal("invalid radio logo", err)
	}
	for _, scenario := range []string{"missing", "private", "offline", "not-found", "invalid", "oversized", "unsupported"} {
		t.Run(scenario, func(t *testing.T) {
			favicon := "https://1.1.1.1/logo.png"
			if scenario == "missing" {
				favicon = ""
			}
			if scenario == "private" {
				favicon = "http://127.0.0.1/logo.png"
			}
			// A custom station with no directory identity needs no external lookup.
			_, err := s.DB.Exec(`UPDATE stations SET favicon=?, source='custom' WHERE id='1001'`, favicon)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			app.ArtworkClient = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if scenario == "offline" {
					return nil, io.ErrUnexpectedEOF
				}
				body := []byte("not an image")
				status := http.StatusOK
				if scenario == "not-found" {
					status = 404
				}
				if scenario == "oversized" {
					body = bytes.Repeat([]byte("x"), (1<<20)+1)
				}
				if scenario == "unsupported" {
					body = []byte("<svg></svg>")
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
			})}
			for _, radio := range []bool{false, true} {
				path := "/stations/1001/artwork"
				want, kind := original, "image/png"
				if radio {
					path = "/artwork/1001.jpg"
					want, kind = radioLogo, "image/jpeg"
				}
				for _, method := range []string{"GET", "HEAD"} {
					w := httptest.NewRecorder()
					r := httptest.NewRequest(method, path, nil)
					if radio {
						app.ServeRadioArtwork(w, r)
					} else {
						h.ServeHTTP(w, r)
					}
					if w.Code != 200 || w.Header().Get("Content-Type") != kind || w.Header().Get("Content-Length") != strconv.Itoa(len(want)) || w.Header().Get("Cache-Control") != "private, max-age=300" {
						t.Fatal(w.Code, w.Header())
					}
					if method == "GET" && !bytes.Equal(w.Body.Bytes(), want) {
						t.Fatal("incorrect fallback")
					}
					if method == "HEAD" && w.Body.Len() != 0 {
						t.Fatal("HEAD returned body")
					}
				}
			}
			if (scenario == "missing" || scenario == "private") && calls != 0 {
				t.Fatal("unexpected upstream request", calls)
			}
		})
	}
}
