package routing

import (
	"database/sql"
	"reflect"
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
		sub_id TEXT PRIMARY KEY,
		enabled INTEGER NOT NULL DEFAULT 0,
		config TEXT NOT NULL DEFAULT '{}',
		updated_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestGet_NoRowReturnsDisabledZeroProfile(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	enabled, profile, err := s.Get("tok-unknown")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if enabled {
		t.Error("expected disabled for a subscriber with no row")
	}
	if !reflect.DeepEqual(profile, Profile{}) {
		t.Errorf("expected zero-value profile, got %+v", profile)
	}
}

func TestSet_ThenGetRoundTrips(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	profile := Profile{
		GlobalProxy:    true,
		RouteOrder:     "Proxy>Direct>Block",
		DomainStrategy: "IPIfNonMatch",
		DirectSites:    []string{"example.com"},
		BlockIP:        []string{"1.2.3.0/24"},
		DNSHosts:       map[string]string{"example.com": "1.2.3.4"},
		FakeDNS:        true,
		UseChunkFiles:  true,
	}
	if err := s.Set("tok-bob", true, profile); err != nil {
		t.Fatalf("Set: %v", err)
	}

	enabled, got, err := s.Get("tok-bob")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !enabled {
		t.Error("expected enabled=true")
	}
	if got.RouteOrder != profile.RouteOrder || got.DomainStrategy != profile.DomainStrategy {
		t.Errorf("expected profile to round-trip, got %+v", got)
	}
	if len(got.DirectSites) != 1 || got.DirectSites[0] != "example.com" {
		t.Errorf("expected DirectSites to round-trip, got %+v", got.DirectSites)
	}
	if got.DNSHosts["example.com"] != "1.2.3.4" {
		t.Errorf("expected DNSHosts to round-trip, got %+v", got.DNSHosts)
	}
}

func TestSet_OverwritesExisting(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", true, Profile{RouteOrder: "Proxy>Direct>Block"}); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := s.Set("tok-bob", false, Profile{RouteOrder: "Block>Proxy>Direct"}); err != nil {
		t.Fatalf("Set v2: %v", err)
	}

	enabled, profile, err := s.Get("tok-bob")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if enabled {
		t.Error("expected enabled=false after overwrite")
	}
	if profile.RouteOrder != "Block>Proxy>Direct" {
		t.Errorf("expected overwritten RouteOrder, got %q", profile.RouteOrder)
	}
}

func TestDeleteAll_FallsBackToDisabled(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", true, Profile{RouteOrder: "Proxy>Direct>Block"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.DeleteAll("tok-bob"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	enabled, profile, err := s.Get("tok-bob")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if enabled || !reflect.DeepEqual(profile, Profile{}) {
		t.Errorf("expected disabled zero-value profile after DeleteAll, got enabled=%v profile=%+v", enabled, profile)
	}
}

func TestDeleteAll_DoesNotAffectOtherSubscribers(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Set("tok-bob", true, Profile{RouteOrder: "Proxy>Direct>Block"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("tok-alice", true, Profile{RouteOrder: "Block>Proxy>Direct"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.DeleteAll("tok-bob"); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}

	enabled, profile, err := s.Get("tok-alice")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !enabled || profile.RouteOrder != "Block>Proxy>Direct" {
		t.Errorf("expected tok-alice unaffected, got enabled=%v profile=%+v", enabled, profile)
	}
}
