// SPDX-License-Identifier: GPL-3.0-only
package store

// Existing devices retain their other playback settings and favourites. AAC is
// allowed for trials; this is a policy change, not a hardware support assertion.
func (s *Store) migrateV5() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE devices SET capabilities=json_set(capabilities,'$.supports_aac',json('true')) WHERE protocol='frontierxml' AND NOT EXISTS(SELECT 1 FROM schema_migrations WHERE version=5);
 INSERT OR IGNORE INTO schema_migrations VALUES(5);`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
