// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"retroradio.local/server/internal/model"
	"strconv"
	"time"
)

func (s *Store) migrateV2(version int) error {
	if version >= 2 {
		return nil
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`ALTER TABLE stations ADD COLUMN rb_uuid TEXT;
 ALTER TABLE stations ADD COLUMN source TEXT NOT NULL DEFAULT 'seed';
 ALTER TABLE stations ADD COLUMN country TEXT NOT NULL DEFAULT 'United Kingdom';
 ALTER TABLE stations ADD COLUMN tags TEXT NOT NULL DEFAULT '';
 ALTER TABLE stations ADD COLUMN language TEXT NOT NULL DEFAULT '';
 ALTER TABLE stations ADD COLUMN hls INTEGER NOT NULL DEFAULT 0;
 CREATE UNIQUE INDEX stations_rb_uuid ON stations(rb_uuid);
 CREATE TABLE stream_health(station_id TEXT PRIMARY KEY REFERENCES stations(id),payload TEXT NOT NULL);
 CREATE TABLE catalogue_cache(key TEXT PRIMARY KEY,payload BLOB NOT NULL,updated TEXT NOT NULL);
 CREATE TABLE catalogue_entries(uuid TEXT PRIMARY KEY,payload BLOB NOT NULL,updated TEXT NOT NULL);
 INSERT INTO schema_migrations VALUES(2);`)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Cache(key string) ([]byte, time.Time, error) {
	var b []byte
	var ts string
	e := s.DB.QueryRow(`SELECT payload,updated FROM catalogue_cache WHERE key=?`, key).Scan(&b, &ts)
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return b, t, e
}
func (s *Store) CachePut(key string, entries []model.Candidate) error {
	b, e := json.Marshal(entries)
	if e != nil {
		return e
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, e = tx.Exec(`INSERT INTO catalogue_cache VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET payload=excluded.payload,updated=excluded.updated`, key, b, now); e != nil {
		return e
	}
	for _, a := range entries {
		b, e = json.Marshal(a)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(`INSERT INTO catalogue_entries VALUES(?,?,?) ON CONFLICT(uuid) DO UPDATE SET payload=excluded.payload,updated=excluded.updated`, a.UUID, b, now); e != nil {
			return e
		}
	}
	_, e = tx.Exec(`DELETE FROM catalogue_cache WHERE key NOT IN (SELECT key FROM catalogue_cache ORDER BY updated DESC LIMIT 128); DELETE FROM catalogue_entries WHERE uuid NOT IN (SELECT uuid FROM catalogue_entries ORDER BY updated DESC LIMIT 2000);`)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Candidate(uuid string) (model.Candidate, error) {
	var a model.Candidate
	var b []byte
	e := s.DB.QueryRow(`SELECT payload FROM catalogue_entries WHERE uuid=?`, uuid).Scan(&b)
	if e != nil {
		return a, e
	}
	e = json.Unmarshal(b, &a)
	return a, e
}
func (s *Store) SaveStation(a model.Station) (model.Station, error) {
	tx, e := s.DB.Begin()
	if e != nil {
		return a, e
	}
	defer tx.Rollback()
	if a.RBUUID != "" {
		var id string
		e = tx.QueryRow(`SELECT id FROM stations WHERE rb_uuid=?`, a.RBUUID).Scan(&id)
		if e == nil {
			tx.Rollback()
			return s.Station(id, false)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return a, e
		}
	}
	if a.ID == "" {
		var n int
		e = tx.QueryRow(`UPDATE station_ids SET next_id=next_id+1 WHERE singleton=1 RETURNING next_id-1`).Scan(&n)
		if e != nil {
			return a, e
		}
		a.ID = strconv.Itoa(n)
		b := make([]byte, 24)
		if _, e = rand.Read(b); e != nil {
			return a, e
		}
		a.StreamID = hex.EncodeToString(b)
		_, e = tx.Exec(`INSERT INTO stations(id,name,url,stream_id,codec,bitrate,rb_uuid,source,country,tags,language,hls) VALUES(?,?,?,?,?,?,NULLIF(?,''),?,?,?,?,?)`, a.ID, a.Name, a.URL, a.StreamID, a.Codec, a.Bitrate, a.RBUUID, a.Source, a.Country, a.Tags, a.Language, a.HLS)
	} else {
		var oldURL string
		e = tx.QueryRow(`SELECT url FROM stations WHERE id=? AND source='custom'`, a.ID).Scan(&oldURL)
		if e != nil {
			return a, e
		}
		_, e = tx.Exec(`UPDATE stations SET name=?,url=?,codec=?,bitrate=?,country=?,tags=?,language=?,hls=? WHERE id=? AND source='custom'`, a.Name, a.URL, a.Codec, a.Bitrate, a.Country, a.Tags, a.Language, a.HLS, a.ID)
		if e == nil && oldURL != a.URL {
			_, e = tx.Exec(`DELETE FROM stream_health WHERE station_id=?`, a.ID)
		}
	}
	if e != nil {
		return a, e
	}
	if e = tx.Commit(); e != nil {
		return a, e
	}
	return s.Station(a.ID, false)
}
func (s *Store) SaveAndFavourite(a model.Station, device string) (model.Station, error) {
	if _, e := s.Device(device); e != nil {
		return a, e
	}
	a, e := s.SaveStation(a)
	if e != nil {
		return a, e
	}
	return a, s.Favourite(device, a.ID, true)
}
func (s *Store) Health(id string) (model.Health, error) {
	var h model.Health
	var payload string
	e := s.DB.QueryRow(`SELECT payload FROM stream_health WHERE station_id=?`, id).Scan(&payload)
	if e != nil {
		return h, e
	}
	e = json.Unmarshal([]byte(payload), &h)
	return h, e
}
func (s *Store) SaveHealth(h model.Health) error {
	b, e := json.Marshal(h)
	if e != nil {
		return e
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`INSERT INTO stream_health VALUES(?,?) ON CONFLICT(station_id) DO UPDATE SET payload=excluded.payload`, h.StationID, string(b))
	if e != nil {
		return e
	}
	if h.Working && h.Codec != "" {
		_, e = tx.Exec(`UPDATE stations SET codec=?,bitrate=?,hls=CASE WHEN ?<>'' THEN 1 ELSE hls END WHERE id=?`, h.Codec, h.Bitrate, h.Adaptive, h.StationID)
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (s *Store) ByUUID(uuid string) (model.Station, error) {
	var id string
	e := s.DB.QueryRow(`SELECT id FROM stations WHERE rb_uuid=?`, uuid).Scan(&id)
	if e != nil {
		return model.Station{}, e
	}
	return s.Station(id, false)
}
