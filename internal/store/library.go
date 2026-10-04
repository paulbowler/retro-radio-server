// SPDX-License-Identifier: GPL-3.0-only
package store

import "database/sql"

func (s *Store) migrateV4() error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`CREATE TABLE IF NOT EXISTS station_ids(singleton INTEGER PRIMARY KEY CHECK(singleton=1),next_id INTEGER NOT NULL);
 INSERT OR IGNORE INTO station_ids SELECT 1,MAX(1001,COALESCE(MAX(CAST(id AS INTEGER)),1000))+1 FROM stations;
 INSERT OR IGNORE INTO schema_migrations VALUES(4);`)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) StationUsers(id string) int {
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE EXISTS(SELECT 1 FROM stations WHERE id=?)`, id).Scan(&n)
	return n
}

// DeleteStation removes library membership and dependent data in one transaction.
// The persistent station ID counter prevents a deleted preset ID being reused.
func (s *Store) DeleteStation(id string) error {
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, table := range []string{"favourites", "device_stations", "stream_health"} {
		if _, e = tx.Exec(`DELETE FROM `+table+` WHERE station_id=?`, id); e != nil {
			return e
		}
	}
	result, e := tx.Exec(`DELETE FROM stations WHERE id=?`, id)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}
