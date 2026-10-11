// SPDX-License-Identifier: GPL-3.0-only
package web

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"retroradio.local/server/internal/delivery"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
)

func TestCustomArtworkFromPublisherAndSharedCache(t *testing.T) {
	for _, source := range []string{"stream website", "origin page"} {
		t.Run(source, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.DB.Close()
			station, err := db.SaveStation(model.Station{Name: "Custom", Source: "custom", URL: "https://1.1.1.1/live", Codec: "MP3"})
			if err != nil {
				t.Fatal(err)
			}
			var logo bytes.Buffer
			img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
			img.Set(16, 16, color.NRGBA{R: 255, A: 255})
			png.Encode(&logo, img)
			calls := []string{}
			client := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
				calls = append(calls, r.URL.String())
				body, kind := "", "text/html"
				headers := http.Header{}
				switch r.URL.Path {
				case "/live":
					kind = "audio/mpeg"
					if source == "stream website" {
						headers.Set("icy-url", "https://8.8.8.8/listen/custom")
					}
				case "/listen/custom", "/":
					body = `<html><head><meta content='../logos/custom.png?size=128&amp;fit=cover' property='og:image'><link rel='icon' href='/wrong.png'></head></html>`
				case "/logos/custom.png":
					body, kind = logo.String(), "image/png"
				default:
					t.Fatalf("unexpected lookup %s", r.URL)
				}
				headers.Set("Content-Type", kind)
				return &http.Response{StatusCode: 200, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			relay := delivery.New(db)
			app := &App{Store: db, Relay: relay, ArtworkClient: client}
			h := app.Handler()
			// Existing missing-image caches must not suppress new publisher discovery.
			oldKeyStation := station
			oldKeyStation.Source = "seed"
			db.SaveArtwork(artworkKey(oldKeyStation), store.Artwork{Data: []byte{}, Radio: []byte{}})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/stations/"+station.ID+"/artwork", nil))
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), logo.Bytes()) {
				t.Fatal("publisher logo missing", w.Code, w.Header())
			}
			saved, _ := db.Station(station.ID, false)
			if !strings.Contains(saved.Favicon, "/logos/custom.png?size=128&fit=cover") {
				t.Fatal(saved.Favicon)
			}
			before := len(calls)
			w = httptest.NewRecorder()
			app.ServeRadioArtwork(w, httptest.NewRequest("GET", "/artwork/"+station.ID+".jpg", nil))
			fallback, _ := fallbackRadioJPEG()
			if w.Header().Get("Content-Type") != "image/jpeg" || bytes.Equal(w.Body.Bytes(), fallback) || len(calls) != before {
				t.Fatal("radio failed to reuse publisher logo", calls)
			}
			saved.URL = "https://1.1.1.1/new-live"
			changed, err := db.SaveStation(saved)
			if err != nil || changed.Favicon != "" || changed.Homepage != "" {
				t.Fatal("old publisher retained", changed, err)
			}
		})
	}
}

func TestPageArtworkIgnoresFakeTagsAndResolvesRelativeImages(t *testing.T) {
	data := []byte(`<script>"<meta property='og:image' content='/fake.png'>"</script><!-- <meta property='og:image' content='/fake2.png'> --><link href='icon.png' rel='shortcut icon'><meta NAME='twitter:image' content='../logo.png?a=1&amp;b=2'>`)
	got := pageArtwork("https://1.1.1.1/listen/custom", data)
	if len(got) != 2 || got[0] != "https://1.1.1.1/logo.png?a=1&b=2" || got[1] != "https://1.1.1.1/listen/icon.png" {
		t.Fatal(got)
	}
}

func TestCustomArtworkRejectsPrivatePublisherTargets(t *testing.T) {
	app := &App{Relay: delivery.New(nil), ArtworkClient: &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		t.Fatal("private resource fetched", r.URL)
		return nil, io.EOF
	})}}
	station := model.Station{Source: "custom", URL: "https://1.1.1.1/live"}
	for _, raw := range []string{"http://127.0.0.1/logo.png", "http://192.168.1.4/logo.png", "file:///logo.png", "https://user:pass@1.1.1.1/logo.png"} {
		if _, err := app.artworkRequest(context.Background(), station, raw); err == nil {
			t.Fatal("unsafe publisher permitted", raw)
		}
	}
}

func TestArtworkDiscoveryDoesNotCacheOldStreamLogoAfterEdit(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "edit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	old, err := db.SaveStation(model.Station{Source: "custom", Name: "Custom", URL: "https://1.1.1.1/old", Codec: "MP3"})
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.URL = "https://1.1.1.1/new"
	next, err = db.SaveStation(next)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{Store: db}
	app.saveStationArtwork(artworkKey(old), old, store.Artwork{Data: []byte("old-logo"), Radio: []byte("old-jpeg")})
	if _, err = db.Artwork(artworkKey(next)); err == nil {
		t.Fatal("old logo cached for new listening link")
	}
}
