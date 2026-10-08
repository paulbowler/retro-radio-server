// SPDX-License-Identifier: GPL-3.0-only
package store

import "time"

func (s *Store) migrateV11() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE IF NOT EXISTS music_last_play(track TEXT PRIMARY KEY,played INTEGER NOT NULL); INSERT OR IGNORE INTO schema_migrations VALUES(11);`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MusicLastPlays() (map[string]time.Time, error) {
	rows, err := s.DB.Query(`SELECT track,played FROM music_last_play`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	plays := make(map[string]time.Time)
	for rows.Next() {
		var key string
		var at int64
		if err := rows.Scan(&key, &at); err != nil {
			return nil, err
		}
		plays[key] = time.Unix(0, at)
	}
	return plays, rows.Err()
}

func (s *Store) RecordMusicPlay(key string, at time.Time) error {
	_, err := s.DB.Exec(`INSERT INTO music_last_play(track,played) VALUES(?,?) ON CONFLICT(track) DO UPDATE SET played=MAX(played,excluded.played)`, key, at.UnixNano())
	return err
}
