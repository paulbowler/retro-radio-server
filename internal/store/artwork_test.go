// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"path/filepath"
	"retroradio.local/server/internal/model"
	"testing"
)

func TestArtworkMigrationPreservesLibraryAndFavourites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artwork.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := s.Seen("kitchen", "pure", "", "192.168.1.50")
	s.Favourite(d.ID, "1001", true)
	uuid := "11111111-1111-1111-1111-111111111111"
	s.CachePut("fixture", []model.Candidate{{UUID: uuid, Favicon: "https://example.com/logo.png"}})
	if _, err = s.DB.Exec(`UPDATE stations SET rb_uuid=? WHERE id='1001'; ALTER TABLE stations DROP COLUMN favicon; DELETE FROM schema_migrations WHERE version=8`, uuid); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	stations, err := s.Stations("")
	if err != nil || len(stations) != 1 || stations[0].Favicon != "https://example.com/logo.png" {
		t.Fatal(stations, err)
	}
	favs, err := s.Favourites(d.ID)
	if err != nil || len(favs) != 1 || favs[0].ID != "1001" {
		t.Fatal(favs, err)
	}
	saved, err := s.SaveStation(model.Station{Name: "Custom", Source: "custom", URL: "https://example.com/audio", Codec: "MP3", Favicon: "https://example.com/custom.png"})
	if err != nil || saved.Favicon != "https://example.com/custom.png" {
		t.Fatal(saved, err)
	}
}
