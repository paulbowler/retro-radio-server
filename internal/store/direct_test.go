package store

import (
	"path/filepath"
	"retroradio.local/server/internal/model"
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

func TestDirectRetainsDirectoryOriginalURL(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "original.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	c := model.Candidate{UUID: "11111111-1111-1111-1111-111111111111", Name: "Original FM", URL: "http://example.com/feed?x=a%2Fb", Resolved: "https://example.com/resolved", Codec: "MP3", Bitrate: 128}
	if err = s.CachePut("original", []model.Candidate{c}); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveStation(model.CandidateStation(c))
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Variants) != 1 || saved.Variants[0].OriginalURL != c.URL || saved.Variants[0].URL != c.Resolved {
		t.Fatal(saved)
	}
}
