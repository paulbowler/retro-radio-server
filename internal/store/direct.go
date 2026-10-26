// SPDX-License-Identifier: GPL-3.0-only
package store

import "database/sql"

func (s *Store) migrateV12() error {
	var version int
	if err := s.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 12 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`ALTER TABLE stations ADD COLUMN direct INTEGER NOT NULL DEFAULT 0; INSERT INTO schema_migrations VALUES(12);`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetStationDirect(id string, direct bool) error {
	result, err := s.DB.Exec(`UPDATE stations SET direct=? WHERE id=?`, direct, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
