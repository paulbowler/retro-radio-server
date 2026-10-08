// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMusicLastPlayPersistsAndNeverMovesBackwards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "radio.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := s.RecordMusicPlay("song", at); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMusicPlay("song", at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	plays, err := s.MusicLastPlays()
	if err != nil || len(plays) != 1 || !plays["song"].Equal(at) {
		t.Fatal(plays, err)
	}
}
