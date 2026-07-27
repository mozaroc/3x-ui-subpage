package routing

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE user_routing (
		sub_id      TEXT PRIMARY KEY,
		enabled     INTEGER NOT NULL DEFAULT 0,
		routing_b64 TEXT NOT NULL DEFAULT '',
		updated_at  INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("create user_routing table: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE routing_generator (
		id            INTEGER PRIMARY KEY CHECK (id = 1),
		name          TEXT NOT NULL DEFAULT '',
		profile       TEXT NOT NULL DEFAULT '{}',
		generated_b64 TEXT NOT NULL DEFAULT '',
		updated_at    INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatalf("create routing_generator table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestGet_NoRowReturnsDisabledEmptyString(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	enabled, b64, err := s.Get("tok-unknown")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if enabled {
		t.Error("expected disabled for a subscriber with no row")
	}
	if b64 != "" {
		t.Errorf("expected empty routing_b64, got %q", b64)
	}
}

func TestSet_ThenGetRoundTrips(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", true, "abc123=="); err != nil {
		t.Fatalf("Set: %v", err)
	}

	enabled, b64, err := s.Get("tok-bob")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !enabled {
		t.Error("expected enabled=true")
	}
	if b64 != "abc123==" {
		t.Errorf("expected routing_b64 to round-trip, got %q", b64)
	}
}

func TestSet_OverwritesExisting(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", true, "first"); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := s.Set("tok-bob", false, "second"); err != nil {
		t.Fatalf("Set v2: %v", err)
	}

	enabled, b64, err := s.Get("tok-bob")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if enabled {
		t.Error("expected enabled=false after overwrite")
	}
	if b64 != "second" {
		t.Errorf("expected overwritten routing_b64, got %q", b64)
	}
}

func TestDeleteAll_FallsBackToDisabled(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", true, "abc"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.DeleteAll("tok-bob"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	enabled, b64, err := s.Get("tok-bob")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if enabled || b64 != "" {
		t.Errorf("expected disabled/empty after DeleteAll, got enabled=%v b64=%q", enabled, b64)
	}
}

func TestDeleteAll_DoesNotAffectOtherSubscribers(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", true, "bob-b64"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("tok-alice", true, "alice-b64"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.DeleteAll("tok-bob"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	enabled, b64, err := s.Get("tok-alice")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !enabled || b64 != "alice-b64" {
		t.Errorf("expected tok-alice unaffected, got enabled=%v b64=%q", enabled, b64)
	}
}

func TestGetGenerator_NoRowReturnsZeroValues(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	name, profile, b64, err := s.GetGenerator()
	if err != nil {
		t.Fatalf("GetGenerator: %v", err)
	}
	if name != "" || b64 != "" {
		t.Errorf("expected empty name/b64, got name=%q b64=%q", name, b64)
	}
	if profile.RouteOrder != "" {
		t.Errorf("expected zero-value profile, got %+v", profile)
	}
}

func TestSaveGenerator_ThenGetRoundTrips(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	profile := Profile{
		RouteOrder:  "Proxy>Direct>Block",
		DirectSites: []string{"example.com"},
	}

	generated, err := s.SaveGenerator("my-profile", profile)
	if err != nil {
		t.Fatalf("SaveGenerator: %v", err)
	}
	if generated.Base64 == "" {
		t.Fatal("expected a non-empty generated base64")
	}
	if _, err := base64.StdEncoding.DecodeString(generated.Base64); err != nil {
		t.Errorf("expected valid base64, got error: %v", err)
	}

	name, gotProfile, b64, err := s.GetGenerator()
	if err != nil {
		t.Fatalf("GetGenerator: %v", err)
	}
	if name != "my-profile" {
		t.Errorf("expected name to round-trip, got %q", name)
	}
	if gotProfile.RouteOrder != "Proxy>Direct>Block" {
		t.Errorf("expected profile to round-trip, got %+v", gotProfile)
	}
	if len(gotProfile.DirectSites) != 1 || gotProfile.DirectSites[0] != "example.com" {
		t.Errorf("expected DirectSites to round-trip, got %+v", gotProfile.DirectSites)
	}
	if b64 != generated.Base64 {
		t.Errorf("expected stored generated_b64 to match SaveGenerator's return, got %q vs %q", b64, generated.Base64)
	}
}

func TestSaveGenerator_OverwritesExisting(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if _, err := s.SaveGenerator("first", Profile{RouteOrder: "Proxy>Direct>Block"}); err != nil {
		t.Fatalf("SaveGenerator v1: %v", err)
	}
	if _, err := s.SaveGenerator("second", Profile{RouteOrder: "Block>Proxy>Direct"}); err != nil {
		t.Fatalf("SaveGenerator v2: %v", err)
	}

	name, profile, _, err := s.GetGenerator()
	if err != nil {
		t.Fatalf("GetGenerator: %v", err)
	}
	if name != "second" || profile.RouteOrder != "Block>Proxy>Direct" {
		t.Errorf("expected overwritten generator state, got name=%q profile=%+v", name, profile)
	}
}

func TestSaveGenerator_Base64DecodesToExpectedJSON(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	generated, err := s.SaveGenerator("checkme", Profile{RouteOrder: "Proxy>Block>Direct"})
	if err != nil {
		t.Fatalf("SaveGenerator: %v", err)
	}

	raw, err := base64.StdEncoding.DecodeString(generated.Base64)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal decoded JSON: %v", err)
	}
	if decoded["RouteOrder"] != "Proxy>Block>Direct" {
		t.Errorf("expected RouteOrder in decoded JSON, got %+v", decoded)
	}
	if decoded["Name"] != "checkme" {
		t.Errorf("expected Name=checkme in decoded JSON, got %+v", decoded)
	}
}
