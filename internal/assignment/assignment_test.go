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
	if _, err := db.Exec(`CREATE TABLE template_assignments (
		sub_id TEXT NOT NULL,
		client_type TEXT NOT NULL,
		profile TEXT NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (sub_id, client_type)
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestResolve_AssignedProfile(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO template_assignments (sub_id, client_type, profile, updated_at) VALUES ('tok-alice', 'xray', 'gaming', 1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	s := New(db)
	profile, err := s.Resolve("tok-alice", "xray_json")
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

	profile, err := s.Resolve("tok-unknown", "xray_json")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profile != DefaultProfile {
		t.Errorf("expected %q, got %q", DefaultProfile, profile)
	}
}

func TestResolve_UnknownFormatErrors(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if _, err := s.Resolve("tok-alice", "not_a_real_format"); err == nil {
		t.Fatal("expected an error for an unknown format")
	}
}

func TestResolve_XrayClientTypeGovernsXrayJSON(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", "xray", "gaming"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	for _, format := range []string{"xray_json"} {
		profile, err := s.Resolve("tok-bob", format)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", format, err)
		}
		if profile != "gaming" {
			t.Errorf("Resolve(%s): expected gaming, got %q", format, profile)
		}
	}
}

func TestSet_ThenResolveReflectsIt(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", "clash", "gaming"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	profile, err := s.Resolve("tok-bob", "clash")
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

	if err := s.Set("tok-bob", "clash", "gaming"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := s.Set("tok-bob", "clash", "minimal"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	profile, err := s.Resolve("tok-bob", "clash")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profile != "minimal" {
		t.Errorf("expected overwritten profile minimal, got %q", profile)
	}
}

func TestSet_IndependentPerClientType(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", "xray", "gaming"); err != nil {
		t.Fatalf("Set xray: %v", err)
	}
	if err := s.Set("tok-bob", "clash", "minimal"); err != nil {
		t.Fatalf("Set clash: %v", err)
	}

	if p, err := s.Resolve("tok-bob", "xray_json"); err != nil || p != "gaming" {
		t.Errorf("xray_json: expected gaming, got %q (err=%v)", p, err)
	}
	if p, err := s.Resolve("tok-bob", "clash"); err != nil || p != "minimal" {
		t.Errorf("clash: expected minimal, got %q (err=%v)", p, err)
	}
	if p, err := s.Resolve("tok-bob", "mihomo"); err != nil || p != DefaultProfile {
		t.Errorf("mihomo: expected default (unset), got %q (err=%v)", p, err)
	}
}

func TestForSubID_FillsDefaultsForUnsetClientTypes(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", "xray", "gaming"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := s.ForSubID("tok-bob")
	if err != nil {
		t.Fatalf("ForSubID: %v", err)
	}
	if len(got) != len(ClientTypes) {
		t.Fatalf("expected %d client types, got %d: %+v", len(ClientTypes), len(got), got)
	}
	if got["xray"] != "gaming" {
		t.Errorf("expected xray=gaming, got %q", got["xray"])
	}
	for _, ct := range ClientTypes {
		if ct.Key == "xray" {
			continue
		}
		if got[ct.Key] != DefaultProfile {
			t.Errorf("expected %s=%s, got %q", ct.Key, DefaultProfile, got[ct.Key])
		}
	}
}

func TestForSubID_UnknownSubIDReturnsAllDefaults(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	got, err := s.ForSubID("tok-nonexistent")
	if err != nil {
		t.Fatalf("ForSubID: %v", err)
	}
	for _, ct := range ClientTypes {
		if got[ct.Key] != DefaultProfile {
			t.Errorf("expected %s=%s, got %q", ct.Key, DefaultProfile, got[ct.Key])
		}
	}
}

func TestDeleteAll_FallsBackToDefaultAcrossEveryClientType(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", "xray", "gaming"); err != nil {
		t.Fatalf("Set xray: %v", err)
	}
	if err := s.Set("tok-bob", "clash", "minimal"); err != nil {
		t.Fatalf("Set clash: %v", err)
	}
	if err := s.DeleteAll("tok-bob"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	if p, err := s.Resolve("tok-bob", "xray_json"); err != nil || p != DefaultProfile {
		t.Errorf("expected fallback to default after DeleteAll, got %q (err=%v)", p, err)
	}
	if p, err := s.Resolve("tok-bob", "clash"); err != nil || p != DefaultProfile {
		t.Errorf("expected fallback to default after DeleteAll, got %q (err=%v)", p, err)
	}
}

func TestDeleteAll_DoesNotAffectOtherSubscribers(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", "xray", "gaming"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("tok-alice", "xray", "minimal"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.DeleteAll("tok-bob"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	if p, err := s.Resolve("tok-alice", "xray_json"); err != nil || p != "minimal" {
		t.Errorf("expected tok-alice unaffected, got %q (err=%v)", p, err)
	}
}
