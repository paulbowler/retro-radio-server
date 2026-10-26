package store

import (
	"path/filepath"
	"testing"
)

func TestStationDirectPersistsAndDefaultsToRelay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	station, err := s.Station("1001", false)
	if err != nil || station.Direct {
		t.Fatal(station, err)
	}
	if err = s.SetStationDirect(station.ID, true); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	station, err = s.Station("1001", false)
	if err != nil || !station.Direct {
		t.Fatal(station, err)
	}
	stations, err := s.Stations("")
	if err != nil || len(stations) != 1 || !stations[0].Direct {
		t.Fatal(stations, err)
	}
	if err = s.SetStationDirect(station.ID, false); err != nil {
		t.Fatal(err)
	}
	station, err = s.Station("1001", false)
	if err != nil || station.Direct {
		t.Fatal(station, err)
	}
}
