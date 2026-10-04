// SPDX-License-Identifier: GPL-3.0-only
package catalogue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"retroradio.local/server/internal/store"
	"strconv"
	"strings"
	"testing"
)

func TestDistinctChannelPagesAcrossDirectoryBoundaries(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "channels.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB.Close()
	raw := []model.Candidate{}
	for i := 0; i < 55; i++ {
		for variant := 0; variant < 2; variant++ {
			codec := "MP3"
			if variant == 1 {
				codec = "AAC"
			}
			raw = append(raw, model.Candidate{UUID: fmt.Sprintf("11111111-1111-1111-1111-%012d", i*2+variant), Name: fmt.Sprintf("Channel %02d (%s)", i, codec), Country: "United Kingdom", CountryCode: "GB", Homepage: fmt.Sprintf("https://stations.example/%d", i), URL: fmt.Sprintf("https://1.1.1.1/%d/%d", i, variant), Codec: codec, Bitrate: 128})
		}
	}
	service := &Service{Store: db, Mirrors: []string{"https://directory.example"}, Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		end := offset + PageSize
		if end > len(raw) {
			end = len(raw)
		}
		if offset > len(raw) {
			offset = len(raw)
		}
		b, _ := json.Marshal(raw[offset:end])
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b))), Request: r}, nil
	})}}
	for _, popular := range []bool{false, true} {
		seen := map[string]bool{}
		for _, offset := range []int{0, 24, 48} {
			var page Result
			if popular {
				page, err = service.Popular(context.Background(), "GB", offset)
			} else {
				page, err = service.SearchFiltered(context.Background(), "Channel", "GB", "", offset)
			}
			if err != nil {
				t.Fatal(err)
			}
			want := 24
			if offset == 48 {
				want = 7
			}
			if len(page.Stations) != want || page.More != (offset < 48) {
				t.Fatal(offset, len(page.Stations), page.More)
			}
			for _, channel := range page.Stations {
				if seen[channel.Name] {
					t.Fatal("channel on multiple pages", channel.Name)
				}
				seen[channel.Name] = true
				if len(channel.Variants) != 1 {
					t.Fatal("alternative missing", channel)
				}
			}
		}
	}
	station, err := service.Resolve(context.Background(), raw[0].UUID)
	if err != nil || len(station.Variants) != 2 {
		t.Fatal("resolve discarded alternatives", station, err)
	}
}
