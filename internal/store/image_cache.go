// SPDX-License-Identifier: GPL-3.0-only
package store

import "time"

type Artwork struct {
	Data       []byte
	Kind       string
	Radio      []byte
	Updated    time.Time
	RetryAfter time.Time
}

func (s *Store) migrateV10() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS artwork_cache(key TEXT PRIMARY KEY,data BLOB NOT NULL,kind TEXT NOT NULL,radio BLOB NOT NULL,updated TEXT NOT NULL,retry_after TEXT NOT NULL);
 INSERT INTO schema_migrations SELECT 10 WHERE NOT EXISTS(SELECT 1 FROM schema_migrations WHERE version=10);`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Artwork(key string) (Artwork, error) {
	var a Artwork
	var stamp, retry string
	err := s.DB.QueryRow(`SELECT data,kind,radio,updated,retry_after FROM artwork_cache WHERE key=?`, key).Scan(&a.Data, &a.Kind, &a.Radio, &stamp, &retry)
	a.Updated, _ = time.Parse(time.RFC3339Nano, stamp)
	a.RetryAfter, _ = time.Parse(time.RFC3339Nano, retry)
	return a, err
}
func (s *Store) SaveArtwork(key string, a Artwork) error {
	if a.Data == nil {
		a.Data = []byte{}
	}
	if a.Radio == nil {
		a.Radio = []byte{}
	}
	if a.Updated.IsZero() {
		a.Updated = time.Now().UTC()
	}
	retry := ""
	if !a.RetryAfter.IsZero() {
		retry = a.RetryAfter.UTC().Format(time.RFC3339Nano)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO artwork_cache VALUES(?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET data=excluded.data,kind=excluded.kind,radio=excluded.radio,updated=excluded.updated,retry_after=excluded.retry_after`, key, a.Data, a.Kind, a.Radio, a.Updated.UTC().Format(time.RFC3339Nano), retry)
	if err != nil {
		return err
	}
	// Bound disk use to 64 MiB and 2048 entries, including failed lookups.
	_, err = tx.Exec(`DELETE FROM artwork_cache WHERE key IN (SELECT key FROM (SELECT key,ROW_NUMBER() OVER(ORDER BY updated DESC,key) AS n,SUM(length(data)+length(radio)) OVER(ORDER BY updated DESC,key) AS bytes FROM artwork_cache) WHERE n>2048 OR bytes>67108864)`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
