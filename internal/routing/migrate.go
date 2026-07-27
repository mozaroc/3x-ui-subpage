package routing

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// MigrateLegacy upgrades a v0.1.0-alpha.7-shaped "user_routing" table (one
// full JSON-encoded Profile per subscriber, in a "config" column) to the
// current shape (a single admin-authored Base64 string per subscriber, in
// "routing_b64"). It is a no-op once the table has been migrated, or on a
// fresh install where "user_routing" never had a "config" column at all.
// The legacy "config" column is left in place, unused -- the same
// "don't destroy, just stop reading it" precedent already applied to the
// old routing_rules table.
func MigrateLegacy(db *sql.DB) error {
	hasConfig, err := userRoutingHasColumn(db, "config")
	if err != nil {
		return fmt.Errorf("routing: check legacy column: %w", err)
	}
	if !hasConfig {
		return nil
	}

	hasB64, err := userRoutingHasColumn(db, "routing_b64")
	if err != nil {
		return fmt.Errorf("routing: check routing_b64 column: %w", err)
	}
	if !hasB64 {
		if _, err := db.Exec(`ALTER TABLE user_routing ADD COLUMN routing_b64 TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("routing: add routing_b64 column: %w", err)
		}
	}

	rows, err := db.Query(`SELECT sub_id, config FROM user_routing WHERE routing_b64 = '' AND config != '' AND config != '{}'`)
	if err != nil {
		return fmt.Errorf("routing: query legacy rows: %w", err)
	}
	type legacyRow struct{ subID, config string }
	var pending []legacyRow
	for rows.Next() {
		var lr legacyRow
		if err := rows.Scan(&lr.subID, &lr.config); err != nil {
			rows.Close()
			return fmt.Errorf("routing: scan legacy row: %w", err)
		}
		pending = append(pending, lr)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("routing: iterate legacy rows: %w", err)
	}
	rows.Close()

	for _, lr := range pending {
		var profile Profile
		if err := json.Unmarshal([]byte(lr.config), &profile); err != nil {
			return fmt.Errorf("routing: decode legacy profile for %s: %w", lr.subID, err)
		}
		generated, err := profile.Encode(lr.subID)
		if err != nil {
			return fmt.Errorf("routing: encode legacy profile for %s: %w", lr.subID, err)
		}
		if _, err := db.Exec(`UPDATE user_routing SET routing_b64 = ? WHERE sub_id = ?`, generated.Base64, lr.subID); err != nil {
			return fmt.Errorf("routing: backfill routing_b64 for %s: %w", lr.subID, err)
		}
	}
	return nil
}

// userRoutingHasColumn reports whether "user_routing" currently has a
// column named col. Assumes the table exists (schema.sql always creates it).
func userRoutingHasColumn(db *sql.DB, col string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(user_routing)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return false, err
	}
	dest := make([]any, len(cols))
	nameIdx := -1
	for i, c := range cols {
		if c == "name" {
			nameIdx = i
		}
		dest[i] = new(any)
	}
	if nameIdx < 0 {
		return false, fmt.Errorf("routing: unexpected PRAGMA table_info shape")
	}

	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return false, err
		}
		if name, _ := (*dest[nameIdx].(*any)).(string); name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}
