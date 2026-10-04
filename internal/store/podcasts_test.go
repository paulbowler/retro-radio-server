// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"fmt"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"testing"
	"time"
)

func TestPodcastPersistenceRefreshAndRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "podcasts.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.SavePodcast(model.Podcast{Feed: "https://example.org/feed", Title: "Show"}, []model.Episode{{GUID: "first", Title: "Episode", URL: "https://example.org/a.mp3", Codec: "MP3", Published: time.Now()}})
	if e != nil {
		t.Fatal(e)
	}
	eps, _ := s.Episodes(p.ID)
	id := eps[0].ID
	s.DB.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	ep, e := s.Episode(id)
	if e != nil || ep.Title != "Episode" {
		t.Fatal(ep, e)
	}
	history := []model.Episode{}
	for i := 0; i < 510; i++ {
		history = append(history, model.Episode{GUID: fmt.Sprint(i), Title: "History", URL: "https://example.org/a.mp3", Codec: "MP3", Published: time.Now().Add(-time.Duration(i+1) * time.Hour)})
	}
	history = append(history, model.Episode{GUID: "first", Title: "Updated", URL: "https://example.org/new.mp3", Codec: "MP3", Published: ep.Published})
	_, e = s.SavePodcast(p, history)
	if e != nil {
		t.Fatal(e)
	}
	eps, e = s.Episodes(p.ID)
	if e != nil || len(eps) != 500 || eps[0].ID != id || eps[0].Title != "Updated" {
		t.Fatal(len(eps), e)
	}
	if e = s.DeletePodcast(p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Episode(id); e == nil {
		t.Fatal("episode survives removal")
	}
	if _, e = s.SavePodcast(p, history); e == nil {
		t.Fatal("refresh resurrected removed podcast")
	}
	next, e := s.SavePodcast(model.Podcast{Feed: p.Feed, Title: p.Title}, history[:1])
	if e != nil || next.ID == p.ID {
		t.Fatal("removed ID reused", next, e)
	}
}
