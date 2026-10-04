// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"retroradio.local/server/internal/model"
)

func (s *Store) migrateV6() error {
	var version int
	if err := s.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version >= 6 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`ALTER TABLE stations ADD COLUMN homepage TEXT NOT NULL DEFAULT '';
 CREATE TABLE stream_variants(id TEXT PRIMARY KEY,station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE,url TEXT NOT NULL,uuid TEXT UNIQUE,codec TEXT NOT NULL,bitrate INTEGER NOT NULL,hls INTEGER NOT NULL,health TEXT NOT NULL DEFAULT '{}',UNIQUE(station_id,url));
 CREATE TABLE variant_uuids(uuid TEXT PRIMARY KEY,variant_id TEXT NOT NULL REFERENCES stream_variants(id) ON DELETE CASCADE);
 CREATE TABLE station_aliases(old_id TEXT PRIMARY KEY,old_stream TEXT UNIQUE,station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE);
 CREATE TABLE station_audio(device_id TEXT NOT NULL REFERENCES devices(id),station_id TEXT NOT NULL REFERENCES stations(id) ON DELETE CASCADE,variant_id TEXT NOT NULL REFERENCES stream_variants(id) ON DELETE CASCADE,PRIMARY KEY(device_id,station_id));`)
	if err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id,name,url,stream_id,codec,bitrate,COALESCE(rb_uuid,''),source,country,tags,language,hls FROM stations ORDER BY CAST(id AS INTEGER),id`)
	if err != nil {
		return err
	}
	stations := []model.Station{}
	for rows.Next() {
		var a model.Station
		if err = rows.Scan(&a.ID, &a.Name, &a.URL, &a.StreamID, &a.Codec, &a.Bitrate, &a.RBUUID, &a.Source, &a.Country, &a.Tags, &a.Language, &a.HLS); err != nil {
			rows.Close()
			return err
		}
		stations = append(stations, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	canonical := []model.Station{}
	for _, a := range stations {
		if a.RBUUID != "" {
			var payload []byte
			if tx.QueryRow(`SELECT payload FROM catalogue_entries WHERE uuid=?`, a.RBUUID).Scan(&payload) == nil {
				var c model.Candidate
				if json.Unmarshal(payload, &c) == nil {
					a.Homepage = c.Homepage
				}
			}
		}
		if _, err = tx.Exec(`UPDATE stations SET homepage=? WHERE id=?`, a.Homepage, a.ID); err != nil {
			return err
		}
		var payload string
		if tx.QueryRow(`SELECT payload FROM stream_health WHERE station_id=?`, a.ID).Scan(&payload) != nil {
			payload = "{}"
		}
		if _, err = tx.Exec(`INSERT INTO stream_variants(id,station_id,url,uuid,codec,bitrate,hls,health) VALUES(?,?,?,NULLIF(?,''),?,?,?,?)`, a.StreamID, a.ID, a.URL, a.RBUUID, a.Codec, a.Bitrate, a.HLS, payload); err != nil {
			return err
		}
		if a.RBUUID != "" {
			if _, err = tx.Exec(`INSERT INTO variant_uuids VALUES(?,?)`, a.RBUUID, a.StreamID); err != nil {
				return err
			}
		}
		target := ""
		for _, b := range canonical {
			if model.SameChannel(a, b) {
				target = b.ID
				break
			}
		}
		if target == "" {
			a.Name = model.ChannelName(a.Name)
			canonical = append(canonical, a)
			if _, err = tx.Exec(`UPDATE stations SET name=? WHERE id=?`, a.Name, a.ID); err != nil {
				return err
			}
			continue
		}
		if err = mergeChannel(tx, a, target); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations VALUES(6)`); err != nil {
		return err
	}
	return tx.Commit()
}
func mergeChannel(tx *sql.Tx, old model.Station, target string) error {
	for _, table := range []string{"favourites", "device_stations"} {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO `+table+` SELECT device_id,? FROM `+table+` WHERE station_id=?`, target, old.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE station_id=?`, old.ID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE variant_uuids SET variant_id=(SELECT canonical.id FROM stream_variants old JOIN stream_variants canonical ON canonical.url=old.url WHERE old.id=variant_uuids.variant_id AND canonical.station_id=?) WHERE variant_id IN(SELECT old.id FROM stream_variants old JOIN stream_variants canonical ON canonical.url=old.url WHERE old.station_id=? AND canonical.station_id=?)`, target, old.ID, target); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM stream_variants WHERE station_id=? AND url IN(SELECT url FROM stream_variants WHERE station_id=?)`, old.ID, target); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE stream_variants SET station_id=? WHERE station_id=?`, target, old.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO station_aliases VALUES(?,?,?)`, old.ID, old.StreamID, target); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM stream_health WHERE station_id=?`, old.ID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM stations WHERE id=?`, old.ID)
	return err
}
func (s *Store) Variants(id string) ([]model.StreamVariant, error) {
	rows, err := s.DB.Query(`SELECT id,station_id,url,COALESCE(uuid,''),codec,bitrate,hls,health FROM stream_variants WHERE station_id=? ORDER BY bitrate DESC,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.StreamVariant{}
	for rows.Next() {
		var v model.StreamVariant
		var payload string
		if err = rows.Scan(&v.ID, &v.StationID, &v.URL, &v.UUID, &v.Codec, &v.Bitrate, &v.HLS, &payload); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(payload), &v.Health)
		v.Health.StationID = id
		v.Health.VariantID = v.ID
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) withVariants(a model.Station) (model.Station, error) {
	vs, err := s.Variants(a.ID)
	a.Variants = vs
	return a, err
}
func (s *Store) Preferred(device, station string) string {
	var id string
	_ = s.DB.QueryRow(`SELECT variant_id FROM station_audio WHERE device_id=? AND station_id=?`, device, station).Scan(&id)
	return id
}
func (s *Store) SetPreferred(device, station, variant string) error {
	if _, err := s.Device(device); err != nil {
		return err
	}
	if variant == "" {
		_, err := s.DB.Exec(`DELETE FROM station_audio WHERE device_id=? AND station_id=?`, device, station)
		return err
	}
	var count int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM stream_variants WHERE id=? AND station_id=?`, variant, station).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	_, err := s.DB.Exec(`INSERT INTO station_audio VALUES(?,?,?) ON CONFLICT(device_id,station_id) DO UPDATE SET variant_id=excluded.variant_id`, device, station, variant)
	return err
}
func addVariant(tx *sql.Tx, a model.Station) error {
	var id string
	err := tx.QueryRow(`SELECT id FROM stream_variants WHERE id=(SELECT variant_id FROM variant_uuids WHERE uuid=?) OR (station_id=? AND url=?)`, a.RBUUID, a.ID, a.URL).Scan(&id)
	if err == sql.ErrNoRows {
		b := make([]byte, 24)
		if _, err = rand.Read(b); err != nil {
			return err
		}
		id = hex.EncodeToString(b)
		_, err = tx.Exec(`INSERT INTO stream_variants(id,station_id,url,uuid,codec,bitrate,hls) VALUES(?,?,?,NULLIF(?,''),?,?,?)`, id, a.ID, a.URL, a.RBUUID, a.Codec, a.Bitrate, a.HLS)
		if err != nil {
			return err
		}
		if a.RBUUID != "" {
			_, err = tx.Exec(`INSERT OR IGNORE INTO variant_uuids VALUES(?,?)`, a.RBUUID, id)
		}
		return err
	}
	if err != nil {
		return err
	}
	if a.RBUUID != "" {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO variant_uuids VALUES(?,?)`, a.RBUUID, id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`UPDATE stream_variants SET url=?,codec=?,bitrate=?,hls=? WHERE id=?`, a.URL, a.Codec, a.Bitrate, a.HLS, id)
	return err
}
func (s *Store) ChannelFor(a model.Station) (model.Station, error) {
	if a.RBUUID != "" {
		if saved, err := s.ByUUID(a.RBUUID); err == nil {
			return saved, nil
		}
	}
	stations, err := s.Stations("")
	if err != nil {
		return model.Station{}, err
	}
	for _, saved := range stations {
		if model.SameChannel(a, saved) {
			return saved, nil
		}
	}
	return model.Station{}, sql.ErrNoRows
}

func (s *Store) CachedCandidates() ([]model.Candidate, error) {
	rows, err := s.DB.Query(`SELECT payload FROM catalogue_entries ORDER BY updated DESC LIMIT 2000`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Candidate{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var c model.Candidate
		if json.Unmarshal(b, &c) == nil {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}
