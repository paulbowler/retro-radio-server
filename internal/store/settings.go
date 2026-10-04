// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"encoding/json"
	"errors"
)

type Settings struct {
	BufferSeconds int    `json:"buffer_seconds"`
	AutoReconnect bool   `json:"auto_reconnect"`
	Quality       string `json:"quality"`
	Country       string `json:"country"`
}

func DefaultSettings() Settings { return Settings{Quality: "auto"} }
func (s *Store) migrateV9() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS app_settings(singleton INTEGER PRIMARY KEY CHECK(singleton=1),payload TEXT NOT NULL); INSERT OR IGNORE INTO schema_migrations VALUES(9);`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Settings() Settings {
	settings := DefaultSettings()
	var raw []byte
	if s.DB.QueryRow(`SELECT payload FROM app_settings WHERE singleton=1`).Scan(&raw) == nil {
		_ = json.Unmarshal(raw, &settings)
	}
	return settings
}
func (s *Store) SaveSettings(settings Settings) error {
	if settings.BufferSeconds < 0 || settings.BufferSeconds > 10 || (settings.Quality != "auto" && settings.Quality != "low") || (settings.Country != "" && len(settings.Country) != 2) {
		return errors.New("invalid settings")
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT INTO app_settings VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET payload=excluded.payload`, string(raw))
	return err
}
