package assignment

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE assignments (sub_id TEXT PRIMARY KEY, profile TEXT NOT NULL, updated_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestResolve_AssignedProfile(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO assignments (sub_id, profile, updated_at) VALUES ('tok-alice', 'gaming', 1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	s := New(db)
	profile, err := s.Resolve("tok-alice")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profile != "gaming" {
		t.Errorf("expected gaming, got %q", profile)
	}
}

func TestResolve_NoAssignmentDefaultsToDefault(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	profile, err := s.Resolve("tok-unknown")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profile != DefaultProfile {
		t.Errorf("expected %q, got %q", DefaultProfile, profile)
	}
}

func TestSet_ThenResolveReflectsIt(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", "gaming"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	profile, err := s.Resolve("tok-bob")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profile != "gaming" {
		t.Errorf("expected gaming, got %q", profile)
	}
}

func TestSet_OverwritesExisting(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", "gaming"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := s.Set("tok-bob", "minimal"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	profile, err := s.Resolve("tok-bob")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profile != "minimal" {
		t.Errorf("expected overwritten profile minimal, got %q", profile)
	}
}

func TestDelete_FallsBackToDefault(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", "gaming"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Delete("tok-bob"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	profile, err := s.Resolve("tok-bob")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profile != DefaultProfile {
		t.Errorf("expected fallback to default after delete, got %q", profile)
	}
}

func TestList_ReturnsOnlyExplicitAssignments(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	s.Set("tok-bob", "gaming")
	s.Set("tok-alice", "minimal")

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(list))
	}
	if list[0].SubID != "tok-alice" || list[1].SubID != "tok-bob" {
		t.Errorf("expected sorted order [tok-alice tok-bob], got %+v", list)
	}
}
