package store

import (
	"path/filepath"
	"retroradio.local/server/internal/model"
	"testing"
)

func TestLibraryRemovalIsAtomicAndDoesNotReuseIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	one, e := s.Seen("library-radio-one", "pure", "8", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	two, e := s.Seen("library-radio-two", "pure", "8", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.SaveStation(model.Station{Name: "Disposable FM", URL: "https://1.1.1.1/audio", Codec: "MP3", Source: "custom"})
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range []string{one.ID, two.ID} {
		if e = s.Assign(d, a.ID, true); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.SaveHealth(model.Health{StationID: a.ID, Working: true, Codec: "MP3"}); e != nil {
		t.Fatal(e)
	}
	if s.StationUsers(a.ID) != 2 {
		t.Fatal("wrong usage count")
	}
	if e = s.DeleteStation(a.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Station(a.ID, false); e == nil {
		t.Fatal("station remains")
	}
	if _, e = s.Health(a.ID); e == nil {
		t.Fatal("orphan health")
	}
	for _, d := range []string{one.ID, two.ID} {
		f, e := s.Favourites(d)
		if e != nil || len(f) != 0 || s.Assigned(d, a.ID) {
			t.Fatal("orphan favourite or assignment")
		}
		if !s.Assigned(d, "1001") {
			t.Fatal("unrelated station removed")
		}
	}
	s.DB.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	b, e := s.SaveStation(model.Station{Name: "Replacement FM", URL: "https://1.1.1.1/audio", Codec: "MP3", Source: "custom"})
	if e != nil {
		t.Fatal(e)
	}
	if b.ID == a.ID || b.StreamID == a.StreamID {
		t.Fatal("removed preset identity reused")
	}
	if e = s.DeleteStation(a.ID); e == nil {
		t.Fatal("missing station delete succeeded")
	}
	if _, e = s.Station(b.ID, false); e != nil {
		t.Fatal("failed deletion changed another station")
	}
}
