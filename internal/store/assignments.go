// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"retroradio.local/server/internal/model"
)

func (s *Store) migrateV3() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS device_stations(device_id TEXT NOT NULL REFERENCES devices(id),station_id TEXT NOT NULL REFERENCES stations(id),PRIMARY KEY(device_id,station_id));
 INSERT OR IGNORE INTO device_stations SELECT devices.id,stations.id FROM devices CROSS JOIN stations WHERE NOT EXISTS(SELECT 1 FROM schema_migrations WHERE version=3);
 INSERT OR IGNORE INTO schema_migrations VALUES(3);`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Assign keeps favourite status independent of membership. Existing favourites survive re-adds.
func (s *Store) Assign(device, station string, favourite bool) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT OR IGNORE INTO device_stations VALUES(?,?)`, device, station); err != nil {
		return err
	}
	if favourite {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO favourites VALUES(?,?)`, device, station); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) RemoveAssignment(device, station string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM favourites WHERE device_id=? AND station_id=?`, device, station); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM device_stations WHERE device_id=? AND station_id=?`, device, station); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Assigned(device, station string) bool {
	var n int
	return s.DB.QueryRow(`SELECT COUNT(*) FROM device_stations WHERE device_id=? AND station_id=?`, device, station).Scan(&n) == nil && n > 0
}
func (s *Store) DeviceStations(device, search string) ([]model.Station, error) {
	rows, err := s.DB.Query(`SELECT station_id FROM device_stations JOIN stations ON stations.id=station_id WHERE device_id=? AND instr(lower(name),lower(?))>0 ORDER BY name`, device, search)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []model.Station{}
	for _, id := range ids {
		a, e := s.Station(id, false)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, nil
}
func (s *Store) SaveAndAssign(a model.Station, device string, favourite bool) (model.Station, error) {
	if _, err := s.Device(device); err != nil {
		return a, err
	}
	a, err := s.SaveStation(a)
	if err != nil {
		return a, err
	}
	return a, s.Assign(device, a.ID, favourite)
}
