// Package store opens the service's single SQLite database — the backing
// store for every admin-editable surface (settings, app catalog, themes,
// generator templates, per-user template assignments). It is intentionally
// thin: schema management only, no query logic (that lives in the packages
// that own each table: internal/apps, internal/theme, internal/generator/*,
// internal/assignment, internal/config).
package store

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// Open opens (creating if necessary) the SQLite database at path, applies
// pragmas suited to a single-writer/many-reader web service, and ensures the
// schema exists. Safe to call against an existing database — every DDL
// statement is CREATE TABLE IF NOT EXISTS.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("store: apply %q: %w", p, err)
		}
	}

	// WAL allows concurrent readers, but modernc.org/sqlite serializes
	// writers at the file level; a moderate pool lets reads run concurrently
	// while busy_timeout queues any writer contention instead of failing.
	db.SetMaxOpenConns(10)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}

	return db, nil
}
