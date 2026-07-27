package routing

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
)

func openMigrationTestDB(t *testing.T, legacyShape bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if legacyShape {
		if _, err := db.Exec(`CREATE TABLE user_routing (
			sub_id     TEXT PRIMARY KEY,
			enabled    INTEGER NOT NULL DEFAULT 0,
			config     TEXT NOT NULL DEFAULT '{}',
			updated_at INTEGER NOT NULL
		)`); err != nil {
			t.Fatalf("create legacy user_routing: %v", err)
		}
	} else {
		if _, err := db.Exec(`CREATE TABLE user_routing (
			sub_id      TEXT PRIMARY KEY,
			enabled     INTEGER NOT NULL DEFAULT 0,
			routing_b64 TEXT NOT NULL DEFAULT '',
			updated_at  INTEGER NOT NULL
		)`); err != nil {
			t.Fatalf("create current-shape user_routing: %v", err)
		}
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateLegacy_CurrentShapeIsNoop(t *testing.T) {
	db := openMigrationTestDB(t, false)

	if err := MigrateLegacy(db); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}

	s := New(db)
	enabled, b64, err := s.Get("tok-bob")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if enabled || b64 != "" {
		t.Errorf("expected untouched empty state, got enabled=%v b64=%q", enabled, b64)
	}
}

func TestMigrateLegacy_BackfillsRoutingB64FromLegacyConfig(t *testing.T) {
	db := openMigrationTestDB(t, true)

	profile := Profile{RouteOrder: "Proxy>Direct>Block", DirectSites: []string{"example.com"}}
	profileJSON, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal legacy profile: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_routing (sub_id, enabled, config, updated_at) VALUES ('tok-alice', 1, ?, 42)`, string(profileJSON)); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := MigrateLegacy(db); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}

	s := New(db)
	enabled, b64, err := s.Get("tok-alice")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !enabled {
		t.Error("expected enabled to survive migration")
	}
	if b64 == "" {
		t.Fatal("expected routing_b64 to be backfilled")
	}

	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode backfilled base64: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal backfilled JSON: %v", err)
	}
	if decoded["RouteOrder"] != "Proxy>Direct>Block" {
		t.Errorf("expected backfilled RouteOrder, got %+v", decoded)
	}

	// The legacy config column is left in place, dormant.
	var config string
	if err := db.QueryRow(`SELECT config FROM user_routing WHERE sub_id = 'tok-alice'`).Scan(&config); err != nil {
		t.Errorf("expected legacy config column to survive migration untouched: %v", err)
	}
}

func TestMigrateLegacy_SecondRunIsCleanNoop(t *testing.T) {
	db := openMigrationTestDB(t, true)

	profileJSON, _ := json.Marshal(Profile{RouteOrder: "Block>Proxy>Direct"})
	if _, err := db.Exec(`INSERT INTO user_routing (sub_id, enabled, config, updated_at) VALUES ('tok-alice', 1, ?, 42)`, string(profileJSON)); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := MigrateLegacy(db); err != nil {
		t.Fatalf("first MigrateLegacy: %v", err)
	}
	s := New(db)
	_, firstB64, err := s.Get("tok-alice")
	if err != nil {
		t.Fatalf("Get after first migration: %v", err)
	}

	if err := MigrateLegacy(db); err != nil {
		t.Fatalf("second MigrateLegacy: %v", err)
	}
	_, secondB64, err := s.Get("tok-alice")
	if err != nil {
		t.Fatalf("Get after second migration: %v", err)
	}
	if firstB64 != secondB64 {
		t.Errorf("expected routing_b64 to survive a second migration run unchanged, got %q then %q", firstB64, secondB64)
	}
}

func TestMigrateLegacy_SkipsRowsWithEmptyLegacyConfig(t *testing.T) {
	db := openMigrationTestDB(t, true)

	if _, err := db.Exec(`INSERT INTO user_routing (sub_id, enabled, config, updated_at) VALUES ('tok-nobody', 0, '{}', 42)`); err != nil {
		t.Fatalf("seed empty-config row: %v", err)
	}

	if err := MigrateLegacy(db); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}

	s := New(db)
	enabled, b64, err := s.Get("tok-nobody")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if enabled || b64 != "" {
		t.Errorf("expected a never-configured legacy row to stay empty, got enabled=%v b64=%q", enabled, b64)
	}
}
