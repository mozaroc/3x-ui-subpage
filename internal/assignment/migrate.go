package assignment

import (
	"database/sql"
	"fmt"
)

// MigrateLegacy carries forward the old single-global-profile "assignments"
// table (sub_id -> one profile applied to every format) into the new
// per-client-type "template_assignments" table, then drops the old table.
// It is a no-op if "assignments" doesn't exist (fresh install, or already
// migrated on a previous run).
//
// The fan-out (insert + drop) runs in one transaction: SQLite supports
// transactional DDL, so a crash mid-migration rolls back cleanly and the
// old table survives untouched for a safe retry on the next start; once it
// commits, the old table is gone and this becomes a permanent no-op.
func MigrateLegacy(db *sql.DB) error {
	var exists int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'assignments'`).Scan(&exists)
	if err != nil {
		return fmt.Errorf("assignment: check legacy table: %w", err)
	}
	if exists == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("assignment: begin migration: %w", err)
	}
	defer tx.Rollback()

	for _, ct := range ClientTypes {
		_, err := tx.Exec(`
			INSERT INTO template_assignments (sub_id, client_type, profile, updated_at)
			SELECT sub_id, ?, profile, updated_at FROM assignments`,
			ct.Key)
		if err != nil {
			return fmt.Errorf("assignment: migrate legacy rows for %s: %w", ct.Key, err)
		}
	}

	if _, err := tx.Exec(`DROP TABLE assignments`); err != nil {
		return fmt.Errorf("assignment: drop legacy table: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("assignment: commit migration: %w", err)
	}
	return nil
}
