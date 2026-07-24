package assignment

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE template_assignments (
		sub_id TEXT NOT NULL,
		client_type TEXT NOT NULL,
		profile TEXT NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (sub_id, client_type)
	)`); err != nil {
		t.Fatalf("create template_assignments: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateLegacy_NoLegacyTableIsNoop(t *testing.T) {
	db := openMigrationTestDB(t)

	if err := MigrateLegacy(db); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}

	s := New(db)
	if p, err := s.Resolve("tok-bob", "xray_link"); err != nil || p != DefaultProfile {
		t.Errorf("expected default, got %q (err=%v)", p, err)
	}
}

func TestMigrateLegacy_FansOutOldGlobalProfileToEveryClientType(t *testing.T) {
	db := openMigrationTestDB(t)
	if _, err := db.Exec(`CREATE TABLE assignments (sub_id TEXT PRIMARY KEY, profile TEXT NOT NULL, updated_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create legacy assignments: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO assignments (sub_id, profile, updated_at) VALUES ('tok-alice', 'gaming', 42)`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := MigrateLegacy(db); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}

	s := New(db)
	got, err := s.ForSubID("tok-alice")
	if err != nil {
		t.Fatalf("ForSubID: %v", err)
	}
	for _, ct := range ClientTypes {
		if got[ct.Key] != "gaming" {
			t.Errorf("expected %s=gaming after migration, got %q", ct.Key, got[ct.Key])
		}
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='assignments'`).Scan(&count); err != nil {
		t.Fatalf("check legacy table: %v", err)
	}
	if count != 0 {
		t.Error("expected legacy 'assignments' table to be dropped after migration")
	}
}

func TestMigrateLegacy_SecondRunIsCleanNoop(t *testing.T) {
	db := openMigrationTestDB(t)
	if _, err := db.Exec(`CREATE TABLE assignments (sub_id TEXT PRIMARY KEY, profile TEXT NOT NULL, updated_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create legacy assignments: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO assignments (sub_id, profile, updated_at) VALUES ('tok-alice', 'gaming', 42)`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := MigrateLegacy(db); err != nil {
		t.Fatalf("first MigrateLegacy: %v", err)
	}
	if err := MigrateLegacy(db); err != nil {
		t.Fatalf("second MigrateLegacy: %v", err)
	}

	s := New(db)
	got, err := s.ForSubID("tok-alice")
	if err != nil {
		t.Fatalf("ForSubID: %v", err)
	}
	for _, ct := range ClientTypes {
		if got[ct.Key] != "gaming" {
			t.Errorf("expected %s=gaming to survive a second migration run unchanged, got %q", ct.Key, got[ct.Key])
		}
	}
}
