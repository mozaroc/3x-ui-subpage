package store

import (
	"path/filepath"
	"testing"
)

func TestOpen_CreatesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	tables := []string{"settings", "applications", "themes", "theme_files", "templates", "assignments"}
	for _, tbl := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&name)
		if err != nil {
			t.Errorf("table %s not found: %v", tbl, err)
		}
	}
}

func TestOpen_IdempotentOnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := db1.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('k', 'v', 1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	db1.Close()

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()

	var value string
	if err := db2.QueryRow("SELECT value FROM settings WHERE key='k'").Scan(&value); err != nil {
		t.Fatalf("expected data to survive reopen: %v", err)
	}
	if value != "v" {
		t.Errorf("expected 'v', got %q", value)
	}
}

func TestOpen_WALMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("expected wal journal mode, got %q", mode)
	}
}
