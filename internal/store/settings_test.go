// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"path/filepath"
	"testing"
)

func TestSettingsPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Settings() != DefaultSettings() {
		t.Fatal(s.Settings())
	}
	want := Settings{AgentVolume: 250, AgentVoice: "ballad", BufferSeconds: 7, AutoReconnect: true, Quality: "low", Country: "GB"}
	if err = s.SaveSettings(want); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Settings{{BufferSeconds: 11, Quality: "auto"}, {BufferSeconds: -1, Quality: "auto"}, {Quality: "bad"}, {Quality: "auto", AgentVoice: "invalid"}, {Quality: "auto", AgentVolume: 24}, {Quality: "auto", AgentVolume: 401}} {
		if err = s.SaveSettings(bad); err == nil {
			t.Fatal("invalid setting accepted")
		}
		if s.Settings() != want {
			t.Fatal("invalid settings overwrote saved settings")
		}
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if s.Settings() != want {
		t.Fatal("settings lost after reopening", s.Settings())
	}
	stations, _ := s.Stations("")
	if len(stations) != 1 || stations[0].ID != "1001" {
		t.Fatal("library changed")
	}
}
