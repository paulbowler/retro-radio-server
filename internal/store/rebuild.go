// SPDX-License-Identifier: GPL-3.0-only
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func quoteIdentifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// Rebuild installs a pristine database produced by Open's current migrations and
// seed path. Callers MUST drain all database users first. Settings are preserved;
// saved library/radio data is intentionally reset in this development operation.
// The same connection and filename survive, and schema replacement is atomic.
func (s *Store) Rebuild(ctx context.Context) (string, error) {
	var path string
	rows, err := s.DB.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var seq int
		var name, file string
		if err = rows.Scan(&seq, &name, &file); err != nil {
			break
		}
		if name == "main" {
			path = file
		}
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if readErr != nil {
		return "", readErr
	}
	if path == "" {
		return "", fmt.Errorf("development rebuild requires a file-backed database")
	}
	template, err := os.CreateTemp(filepath.Dir(path), ".rebuild-*.db")
	if err != nil {
		return "", err
	}
	templatePath := template.Name()
	template.Close()
	defer os.Remove(templatePath)
	defer os.Remove(templatePath + "-wal")
	defer os.Remove(templatePath + "-shm")
	fresh, err := Open(templatePath)
	if err != nil {
		return "", err
	}
	err = fresh.SaveSettings(s.Settings())
	closeErr := fresh.DB.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	backup, err := os.CreateTemp(filepath.Dir(path), "retro-radio-before-rebuild-*.db")
	if err != nil {
		return "", err
	}
	backupPath := backup.Name()
	backup.Close()
	// VACUUM INTO accepts an empty destination. Keep CreateTemp's 0600 mode
	// throughout; SQLite includes committed WAL content in the copy.
	if _, err = s.DB.ExecContext(ctx, `VACUUM main INTO ?`, backupPath); err != nil {
		return "", err
	}
	if err = os.Chmod(backupPath, 0600); err != nil {
		return backupPath, err
	}
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return backupPath, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `ATTACH DATABASE ? AS rebuild`, templatePath); err != nil {
		return backupPath, err
	}
	defer conn.ExecContext(context.Background(), `DETACH DATABASE rebuild`)
	if _, err = conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return backupPath, err
	}
	defer conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return backupPath, err
	}
	defer tx.Rollback()
	type definition struct{ kind, name, sql string }
	readSchema := func(schema string) ([]definition, error) {
		rs, e := tx.QueryContext(ctx, `SELECT type,name,sql FROM `+schema+`.sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY CASE type WHEN 'table' THEN 0 ELSE 1 END`)
		if e != nil {
			return nil, e
		}
		defer rs.Close()
		var definitions []definition
		for rs.Next() {
			var d definition
			if e = rs.Scan(&d.kind, &d.name, &d.sql); e != nil {
				return nil, e
			}
			definitions = append(definitions, d)
		}
		return definitions, rs.Err()
	}
	old, err := readSchema("main")
	if err != nil {
		return backupPath, err
	}
	current, err := readSchema("rebuild")
	if err != nil {
		return backupPath, err
	}
	// Drop views/triggers first; table-owned indexes disappear with their table.
	for _, d := range old {
		if d.kind == "view" || d.kind == "trigger" {
			if _, err = tx.ExecContext(ctx, "DROP "+d.kind+" "+quoteIdentifier(d.name)); err != nil {
				return backupPath, err
			}
		}
	}
	for _, d := range old {
		if d.kind == "table" {
			if _, err = tx.ExecContext(ctx, "DROP TABLE "+quoteIdentifier(d.name)); err != nil {
				return backupPath, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM main.sqlite_sequence`); err != nil {
		return backupPath, err
	}
	for _, d := range current {
		if _, err = tx.ExecContext(ctx, d.sql); err != nil {
			return backupPath, err
		}
		if d.kind == "table" {
			name := quoteIdentifier(d.name)
			if _, err = tx.ExecContext(ctx, "INSERT INTO main."+name+" SELECT * FROM rebuild."+name); err != nil {
				return backupPath, err
			}
		}
	}
	checks, err := tx.QueryContext(ctx, `PRAGMA main.foreign_key_check`)
	if err != nil {
		return backupPath, err
	}
	invalid := checks.Next()
	checkErr := checks.Err()
	checks.Close()
	if invalid {
		return backupPath, fmt.Errorf("rebuilt database failed foreign key validation")
	}
	if checkErr != nil {
		return backupPath, checkErr
	}
	var integrity string
	if err = tx.QueryRowContext(ctx, `PRAGMA main.quick_check`).Scan(&integrity); err != nil {
		return backupPath, err
	}
	if integrity != "ok" {
		return backupPath, fmt.Errorf("rebuilt database failed integrity validation: %s", integrity)
	}
	return backupPath, tx.Commit()
}
