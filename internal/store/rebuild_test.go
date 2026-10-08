package store

import (
	"context"
	"os"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"testing"
	"time"
)

func TestRebuildUsesCurrentMigrationsBacksUpAndRemovesStaleSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radio.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	settings := DefaultSettings()
	settings.Country = "GB"
	settings.AgentVolume = 200
	if err = s.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Seen("test", "pure", "", "192.168.1.2")
	custom, err := s.SaveStation(model.Station{Name: "Custom", URL: "https://example.org/audio", Codec: "MP3", Source: "custom"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Favourite(d.ID, custom.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = s.CachePut("stale", []model.Candidate{{UUID: "stale", Name: "Old"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordMusicPlay("old-token", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`CREATE TABLE obsolete_track_map(id TEXT); ALTER TABLE stations ADD COLUMN obsolete TEXT; INSERT INTO obsolete_track_map VALUES('wrong');`); err != nil {
		t.Fatal(err)
	}
	backup, err := s.Rebuild(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"devices", "favourites", "device_stations", "station_audio", "activity", "catalogue_cache", "catalogue_entries", "artwork_cache", "stream_health", "music_last_play", "podcasts", "podcast_episodes"} {
		var n int
		if err = s.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	if _, err = s.DB.Exec(`SELECT * FROM obsolete_track_map`); err == nil {
		t.Fatal("obsolete schema retained")
	}
	if _, err = s.DB.Exec(`SELECT obsolete FROM stations`); err == nil {
		t.Fatal("obsolete column retained")
	}
	if got := s.Settings(); got != settings {
		t.Fatal(got)
	}
	stations, err := s.Stations("")
	if err != nil || len(stations) != 1 || stations[0].Source != "seed" {
		t.Fatal(stations, err)
	}
	info, err := os.Stat(backup)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	old, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer old.DB.Close()
	if _, err = old.Device(d.ID); err != nil {
		t.Fatal("backup lost radio", err)
	}
	var fk int
	if err = s.DB.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatal(fk, err)
	}
	if _, err = s.Rebuild(context.Background()); err != nil {
		t.Fatal("second rebuild", err)
	}
	s.DB.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.DB.Close()
	if again.Settings() != settings {
		t.Fatal("settings lost after reopen")
	}
}
func TestCanceledRebuildLeavesOriginalDatabase(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "radio.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	d, _ := s.Seen("test", "pure", "", "192.168.1.2")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Rebuild(ctx); err == nil {
		t.Fatal("canceled rebuild succeeded")
	}
	if _, err = s.Device(d.ID); err != nil {
		t.Fatal("original data lost", err)
	}
}
