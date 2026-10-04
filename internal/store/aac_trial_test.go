// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"encoding/json"
	"path/filepath"
	"retroradio.local/server/internal/model"
	"testing"
)

func TestAACTrialUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trial.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.Seen("aac-trial-radio", "pure", "8", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	if !d.Capabilities.AAC {
		t.Fatal("new radio cannot try AAC")
	}
	if e = s.Favourite(d.ID, "1001", true); e != nil {
		t.Fatal(e)
	}
	c := model.LegacyXML
	c.AAC = false
	c.HTTPS = true
	data, _ := json.Marshal(c)
	if _, e = s.DB.Exec(`UPDATE devices SET capabilities=? WHERE id=?`, string(data), d.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`DELETE FROM schema_migrations WHERE version=5`); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	upgraded, e := s.Device(d.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !upgraded.Capabilities.AAC || !upgraded.Capabilities.MP3 || !upgraded.Capabilities.HTTPS || upgraded.Capabilities.HLS {
		t.Fatal("upgrade changed other settings", upgraded.Capabilities)
	}
	f, e := s.Favourites(d.ID)
	if e != nil || len(f) != 1 || f[0].ID != "1001" {
		t.Fatal("lost favourites", f, e)
	}
}
