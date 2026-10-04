// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"retroradio.local/server/internal/model"
	"sync"
	"time"
)

type Store struct {
	DB           *sql.DB
	stationWrite sync.Mutex
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON; CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY);`); err != nil {
		return err
	}
	var version int
	if err := s.DB.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	if version > 7 {
		return fmt.Errorf("database schema %d is newer than this server", version)
	}
	if version >= 1 {
		if err := s.migrateV2(version); err != nil {
			return err
		}
		if err := s.migrateV3(); err != nil {
			return err
		}
		if err := s.migrateV4(); err != nil {
			return err
		}
		if err := s.migrateV5(); err != nil {
			return err
		}
		if err := s.migrateV6(); err != nil {
			return err
		}
		return s.migrateV7()
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE stations(id TEXT PRIMARY KEY,name TEXT NOT NULL,url TEXT NOT NULL,stream_id TEXT NOT NULL UNIQUE,codec TEXT NOT NULL,bitrate INTEGER NOT NULL);
 CREATE TABLE devices(id TEXT PRIMARY KEY,name TEXT NOT NULL,manufacturer TEXT NOT NULL,model TEXT NOT NULL,protocol TEXT NOT NULL,firmware TEXT NOT NULL,ip TEXT NOT NULL,last_seen TEXT NOT NULL,capabilities TEXT NOT NULL);
 CREATE TABLE favourites(device_id TEXT NOT NULL REFERENCES devices(id),station_id TEXT NOT NULL REFERENCES stations(id),PRIMARY KEY(device_id,station_id));
 CREATE TABLE activity(id INTEGER PRIMARY KEY,time TEXT NOT NULL,device TEXT NOT NULL,kind TEXT NOT NULL,detail TEXT NOT NULL);
 INSERT INTO schema_migrations(version) VALUES(1);`)
	if err != nil {
		return err
	}
	b := make([]byte, 24)
	if _, err = rand.Read(b); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO stations VALUES(?,?,?,?,?,?)`, "1001", "Smooth Radio", "https://media-ice.musicradio.com/SmoothUKMP3", hex.EncodeToString(b), "MP3", 128)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err := s.migrateV2(1); err != nil {
		return err
	}
	if err := s.migrateV3(); err != nil {
		return err
	}
	if err := s.migrateV4(); err != nil {
		return err
	}
	if err := s.migrateV5(); err != nil {
		return err
	}
	if err := s.migrateV6(); err != nil {
		return err
	}
	return s.migrateV7()
}
func (s *Store) Stations(search string) ([]model.Station, error) {
	rows, err := s.DB.Query(`SELECT id,name,url,stream_id,codec,bitrate,COALESCE(rb_uuid,''),source,country,tags,language,hls,homepage FROM stations WHERE instr(lower(name),lower(?))>0 ORDER BY name`, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Station{}
	for rows.Next() {
		var a model.Station
		if err = rows.Scan(&a.ID, &a.Name, &a.URL, &a.StreamID, &a.Codec, &a.Bitrate, &a.RBUUID, &a.Source, &a.Country, &a.Tags, &a.Language, &a.HLS, &a.Homepage); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i], err = s.withVariants(out[i])
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) Station(id string, stream bool) (model.Station, error) {
	var a model.Station
	col := "id"
	if stream {
		col = "stream_id"
	}
	err := s.DB.QueryRow(`SELECT id,name,url,stream_id,codec,bitrate,COALESCE(rb_uuid,''),source,country,tags,language,hls,homepage FROM stations WHERE `+col+`=?`, id).Scan(&a.ID, &a.Name, &a.URL, &a.StreamID, &a.Codec, &a.Bitrate, &a.RBUUID, &a.Source, &a.Country, &a.Tags, &a.Language, &a.HLS, &a.Homepage)
	if err == sql.ErrNoRows {
		var canonical string
		aliasColumn := "old_id"
		if stream {
			aliasColumn = "old_stream"
		}
		if s.DB.QueryRow(`SELECT station_id FROM station_aliases WHERE `+aliasColumn+`=?`, id).Scan(&canonical) == nil {
			return s.Station(canonical, false)
		}
	}
	if err != nil {
		return a, err
	}
	return s.withVariants(a)
}

// The protocol's 'mac' is an opaque identifier, not necessarily a hardware MAC.
func DeviceID(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:16])
}
func (s *Store) Seen(token, vendor, firmware, ip string) (model.Device, error) {
	id := DeviceID(token)
	var exists int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM devices WHERE id=?`, id).Scan(&exists); err != nil {
		return model.Device{}, err
	}
	caps, _ := json.Marshal(model.LegacyXML)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.DB.Exec(`INSERT INTO devices VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET firmware=excluded.firmware,ip=excluded.ip,last_seen=excluded.last_seen`, id, "New radio", vendor, "Unverified Frontier XML radio", "frontierxml", firmware, ip, now, string(caps))
	if err != nil {
		return model.Device{}, err
	}
	if exists == 0 {
		if _, err = s.DB.Exec(`INSERT OR IGNORE INTO device_stations SELECT ?,id FROM stations WHERE source='seed'`, id); err != nil {
			return model.Device{}, err
		}
	}
	return s.Device(id)
}
func scanDevice(row interface{ Scan(...any) error }) (model.Device, error) {
	var d model.Device
	var seen, caps string
	err := row.Scan(&d.ID, &d.Name, &d.Manufacturer, &d.Model, &d.Protocol, &d.Firmware, &d.IP, &seen, &caps)
	if err != nil {
		return d, err
	}
	d.LastSeen, err = time.Parse(time.RFC3339Nano, seen)
	if err != nil {
		return d, err
	}
	err = json.Unmarshal([]byte(caps), &d.Capabilities)
	return d, err
}
func (s *Store) Device(id string) (model.Device, error) {
	return scanDevice(s.DB.QueryRow(`SELECT * FROM devices WHERE id=?`, id))
}
func (s *Store) Devices() ([]model.Device, error) {
	rows, err := s.DB.Query(`SELECT * FROM devices ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) Rename(id, name string) error {
	r, err := s.DB.Exec(`UPDATE devices SET name=? WHERE id=?`, name, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return err
}
func (s *Store) Favourite(device, station string, on bool) error {
	if on {
		return s.Assign(device, station, true)
	}
	channel, err := s.Station(station, false)
	if err != nil {
		return err
	}
	station = channel.ID
	_, err = s.DB.Exec(`DELETE FROM favourites WHERE device_id=? AND station_id=?`, device, station)
	return err
}
func (s *Store) Favourites(device string) ([]model.Station, error) {
	rows, e := s.DB.Query(`SELECT id,name,url,stream_id,codec,bitrate,COALESCE(rb_uuid,''),source,country,tags,language,hls,homepage FROM stations JOIN favourites ON favourites.station_id=stations.id WHERE favourites.device_id=? ORDER BY name`, device)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Station{}
	for rows.Next() {
		var a model.Station
		if e = rows.Scan(&a.ID, &a.Name, &a.URL, &a.StreamID, &a.Codec, &a.Bitrate, &a.RBUUID, &a.Source, &a.Country, &a.Tags, &a.Language, &a.HLS, &a.Homepage); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for i := range out {
		out[i], e = s.withVariants(out[i])
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (s *Store) Log(device, kind, detail string) error {
	_, err := s.DB.Exec(`INSERT INTO activity(time,device,kind,detail) VALUES(?,?,?,?); DELETE FROM activity WHERE id <= (SELECT COALESCE(MAX(id),0)-1000 FROM activity);`, time.Now().UTC().Format(time.RFC3339Nano), device, kind, detail)
	return err
}
func (s *Store) Events() ([]model.Event, error) {
	rows, err := s.DB.Query(`SELECT time,device,kind,detail FROM activity ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var e model.Event
		var ts string
		if err = rows.Scan(&ts, &e.Device, &e.Kind, &e.Detail); err != nil {
			return nil, err
		}
		e.Time, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, e)
	}
	return out, rows.Err()
}
