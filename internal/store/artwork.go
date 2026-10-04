// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"encoding/json"
	"retroradio.local/server/internal/model"
)

func (s *Store) migrateV8() error {
	var version int
	if err := s.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 8 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`ALTER TABLE stations ADD COLUMN favicon TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT stations.id,catalogue_entries.payload FROM stations JOIN stream_variants ON stream_variants.station_id=stations.id JOIN catalogue_entries ON catalogue_entries.uuid=stations.rb_uuid OR catalogue_entries.uuid=stream_variants.uuid`)
	if err != nil {
		return err
	}
	images := map[string]string{}
	for rows.Next() {
		var id string
		var payload []byte
		if err = rows.Scan(&id, &payload); err != nil {
			rows.Close()
			return err
		}
		var c model.Candidate
		if json.Unmarshal(payload, &c) == nil && c.Favicon != "" {
			images[id] = c.Favicon
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id, image := range images {
		if _, err = tx.Exec(`UPDATE stations SET favicon=? WHERE id=?`, image, id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations VALUES(8)`); err != nil {
		return err
	}
	return tx.Commit()
}
