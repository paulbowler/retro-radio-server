// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"testing"
	"time"
)

func TestPersistentMigrationsAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radio.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Station("1001", false)
	if e != nil {
		t.Fatal(e)
	}
	one, e := s.Seen("token-one", "pure", "8", "192.168.1.2")
	if e != nil {
		t.Fatal(e)
	}
	two, e := s.Seen("token-two", "pure", "8", "192.168.1.3")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Favourite(one.ID, a.ID, true); e != nil {
		t.Fatal(e)
	}
	if e = s.Rename(one.ID, "Kitchen"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Seen("token-one", "pure", "9", "192.168.1.5"); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	b, e := s.Station("1001", false)
	if e != nil || b.StreamID != a.StreamID {
		t.Fatal("stream ID did not persist", e)
	}
	d, e := s.Device(one.ID)
	if e != nil || d.Name != "Kitchen" || d.Firmware != "9" {
		t.Fatal(d, e)
	}
	f, e := s.Favourites(two.ID)
	if e != nil || len(f) != 0 {
		t.Fatal("favourites leaked")
	}
	f, e = s.Favourites(one.ID)
	if e != nil || len(f) != 1 {
		t.Fatal("favourites lost")
	}
}

func TestCustomEditsAndHealthSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radio.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.SaveStation(model.Station{Name: "My FM", URL: "https://1.1.1.1/audio", Codec: "MP3", Source: "custom"})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Seen("custom-fixture", "pure", "8", "192.168.1.2")
	if e != nil {
		t.Fatal(e)
	}
	s.Favourite(d.ID, a.ID, true)
	h := model.Health{StationID: a.ID, Working: true, FinalURL: a.URL, Checked: time.Now().UTC(), LastSuccess: time.Now().UTC(), Codec: "MP3", Bitrate: 128}
	if e = s.SaveHealth(h); e != nil {
		t.Fatal(e)
	}
	a.Name = "Edited FM"
	saved, e := s.SaveStation(a)
	if e != nil || saved.StreamID != a.StreamID {
		t.Fatal("edit changed identity", e)
	}
	s.DB.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	got, e := s.Health(a.ID)
	if e != nil || got.FinalURL != a.URL || !got.Working {
		t.Fatal(got, e)
	}
	f, e := s.Favourites(d.ID)
	if e != nil || len(f) != 1 || f[0].Name != "Edited FM" {
		t.Fatal(f, e)
	}
	saved.URL = "https://1.1.1.1/changed"
	if _, e = s.SaveStation(saved); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Health(a.ID); e == nil {
		t.Fatal("stale health survived URL edit")
	}
	seed, _ := s.Station("1001", false)
	if _, e = s.SaveStation(seed); e == nil {
		t.Fatal("seed editable as custom")
	}
}

func TestUpgradeFromMilestone1PreservesRadioAndPreset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radio.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	schema, e := os.ReadFile("testdata/v1.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(schema)); e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	a, e := s.Station("1001", false)
	if e != nil || a.StreamID != "existing-opaque-stream-id" || a.URL != "https://media-ice.musicradio.com/SmoothUKMP3" {
		t.Fatal("preset mapping changed", a, e)
	}
	d, e := s.Device("legacy-device")
	if e != nil || d.Name != "Kitchen" || !d.Capabilities.MP3 {
		t.Fatal(d, e)
	}
	f, e := s.Favourites(d.ID)
	if e != nil || len(f) != 1 || f[0].ID != a.ID {
		t.Fatal("favourites lost", f, e)
	}
	var version int
	s.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version)
	if version != 5 {
		t.Fatal(version)
	}
}
