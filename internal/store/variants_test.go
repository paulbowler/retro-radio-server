// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"testing"
)

func TestMigrationMergesVariantsPreservingFavouritesAndOldLinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile("testdata/v1.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db}
	db.SetMaxOpenConns(1)
	for _, migrate := range []func() error{func() error { return s.migrateV2(1) }, s.migrateV3, s.migrateV4, s.migrateV5} {
		if err = migrate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		id, stream, uuid, url, codec string
		bitrate                      int
	}{
		{"1002", "old-aac", "11111111-1111-1111-1111-111111111111", "https://media-ice.musicradio.com/smooth-aac", "AAC", 192},
		{"1003", "old-duplicate", "22222222-2222-2222-2222-222222222222", "https://media-ice.musicradio.com/SmoothUKMP3", "MP3", 128},
	} {
		if _, err = db.Exec(`INSERT INTO stations(id,name,url,stream_id,codec,bitrate,rb_uuid,source,country,tags,language,hls) VALUES(?, 'Smooth Radio (128k)', ?,?,?,?,?,'radio-browser','United Kingdom','','',0)`, item.id, item.url, item.stream, item.codec, item.bitrate, item.uuid); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO favourites VALUES('legacy-device',?)`, item.id); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.migrateV6(); err != nil {
		t.Fatal(err)
	}
	stations, err := s.Stations("")
	if err != nil || len(stations) != 1 || len(stations[0].Variants) != 2 {
		t.Fatal("streams lost or duplicated", stations, err)
	}
	favs, err := s.Favourites("legacy-device")
	if err != nil || len(favs) != 1 || favs[0].ID != "1001" {
		t.Fatal("favourites not combined", favs, err)
	}
	for _, id := range []string{"1002", "1003"} {
		station, err := s.Station(id, false)
		if err != nil || station.ID != "1001" {
			t.Fatal("preset alias broken", id, err)
		}
	}
	for _, stream := range []string{"old-aac", "old-duplicate"} {
		station, err := s.Station(stream, true)
		if err != nil || station.ID != "1001" {
			t.Fatal("stream alias broken", stream, err)
		}
	}
	for _, uuid := range []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"} {
		station, err := s.ByUUID(uuid)
		if err != nil || station.ID != "1001" {
			t.Fatal("directory alias broken", uuid, err)
		}
	}
	if err = s.migrateV6(); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	if err = s.DeleteStation("1001"); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM stream_variants`).Scan(&n)
	if n != 0 {
		t.Fatal("orphan streams")
	}
}
func TestSavingAlternativeKeepsOneChannelAndRadioPreferences(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "streams.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	first, err := s.SaveStation(model.Station{Name: "Jazz FM MP3", URL: "https://1.1.1.1/jazz", Country: "United Kingdom", Source: "radio-browser", RBUUID: "11111111-1111-1111-1111-111111111111", Codec: "MP3", Bitrate: 128})
	if err != nil {
		t.Fatal(err)
	}
	device, _ := s.Seen("test-device", "pure", "", "127.0.0.1")
	if err = s.Favourite(device.ID, first.ID, true); err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveStation(model.Station{Name: "Jazz FM AAC", URL: "https://1.1.1.1/jazz-aac", Country: first.Country, Source: first.Source, RBUUID: "22222222-2222-2222-2222-222222222222", Codec: "AAC", Bitrate: 192})
	if err != nil || second.ID != first.ID || second.StreamID != first.StreamID || len(second.Variants) != 2 {
		t.Fatal(second, err)
	}
	if err = s.SetPreferred(device.ID, second.ID, second.VariantID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetPreferred(device.ID, second.ID, "not-a-stream"); err == nil {
		t.Fatal("invalid preference accepted")
	}
	favs, _ := s.Favourites(device.ID)
	if len(favs) != 1 {
		t.Fatal("favourite duplicated")
	}
	if err = s.SetPreferred(device.ID, second.ID, ""); err != nil || s.Preferred(device.ID, second.ID) != "" {
		t.Fatal("automatic reset failed", err)
	}
}
