package store

import (
	"path/filepath"
	"retroradio.local/server/internal/model"
	"testing"
)

func TestAssignmentsIndependentAndPersistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radio.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Seen("first-device", "pure", "8", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.Seen("second-device", "pure", "8", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.SaveStation(model.Station{Name: "Local FM", URL: "https://1.1.1.1/audio", Codec: "MP3", Source: "custom"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Assign(d.ID, a.ID, false); e != nil {
		t.Fatal(e)
	}
	if s.Assigned(other.ID, a.ID) {
		t.Fatal("assignment leaked")
	}
	if e = s.Favourite(d.ID, a.ID, true); e != nil {
		t.Fatal(e)
	}
	if e = s.Favourite(d.ID, a.ID, false); e != nil {
		t.Fatal(e)
	}
	if !s.Assigned(d.ID, a.ID) {
		t.Fatal("unheart removed assignment")
	}
	f, e := s.Favourites(d.ID)
	if e != nil || len(f) != 0 {
		t.Fatal(f, e)
	}
	if e = s.Assign(d.ID, a.ID, true); e != nil {
		t.Fatal(e)
	}
	if e = s.RemoveAssignment(d.ID, a.ID); e != nil {
		t.Fatal(e)
	}
	f, e = s.Favourites(d.ID)
	if e != nil || len(f) != 0 || s.Assigned(d.ID, a.ID) {
		t.Fatal("removal left favourite or assignment")
	}
	if _, e = s.Station(a.ID, false); e != nil {
		t.Fatal("removal deleted library station", e)
	}
	if e = s.RemoveAssignment(d.ID, "1001"); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	if _, e = s.Seen("first-device", "pure", "8", "127.0.0.1"); e != nil {
		t.Fatal(e)
	}
	if s.Assigned(d.ID, "1001") || s.Assigned(d.ID, a.ID) {
		t.Fatal("restart/contact resurrected removed station")
	}
	if !s.Assigned(other.ID, "1001") {
		t.Fatal("other device changed")
	}
}
func TestV2UpgradePreservesAvailableStations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Seen("old-device", "pure", "8", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.SaveStation(model.Station{Name: "Old FM", URL: "https://1.1.1.1/audio", Codec: "MP3", Source: "custom"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Favourite(d.ID, a.ID, true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`DROP TABLE device_stations; DELETE FROM schema_migrations WHERE version=3`); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	configured, e := s.DeviceStations(d.ID, "")
	if e != nil || len(configured) != 2 {
		t.Fatal(configured, e)
	}
	f, e := s.Favourites(d.ID)
	if e != nil || len(f) != 1 || f[0].StreamID != a.StreamID {
		t.Fatal("migration changed favourite", f, e)
	}
}
