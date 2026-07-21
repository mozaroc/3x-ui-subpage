package routing

import (
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE routing_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			type TEXT NOT NULL,
			value TEXT NOT NULL,
			outbound TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			updated_at INTEGER NOT NULL
		)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateGetUpdateDelete(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	id, err := s.Create(Rule{Profile: "default", Type: "geoip", Value: "CN", Outbound: "direct", Enabled: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Type != "geoip" || got.Value != "CN" || !got.Enabled {
		t.Errorf("unexpected rule: %+v", got)
	}

	if err := s.Update(id, Rule{Profile: "default", Type: "cidr", Value: "10.0.0.0/8", Outbound: "direct", Enabled: false}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = s.Get(id)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got.Type != "cidr" || got.Enabled {
		t.Errorf("update did not take effect: %+v", got)
	}

	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestForProfile_FallsBackToDefault(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if _, err := s.Create(Rule{Profile: "default", SortOrder: 0, Type: "geoip", Value: "CN", Outbound: "direct", Enabled: true}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rules, err := s.ForProfile("gaming")
	if err != nil {
		t.Fatalf("ForProfile: %v", err)
	}
	if len(rules) != 1 || rules[0].Value != "CN" {
		t.Fatalf("expected fallback to default profile's rule, got %+v", rules)
	}
}

func TestForProfile_OwnRulesTakePrecedence(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if _, err := s.Create(Rule{Profile: "default", Type: "geoip", Value: "CN", Outbound: "direct", Enabled: true}); err != nil {
		t.Fatalf("Create default: %v", err)
	}
	if _, err := s.Create(Rule{Profile: "gaming", Type: "domain", Value: "steampowered.com", Outbound: "proxy", Enabled: true}); err != nil {
		t.Fatalf("Create gaming: %v", err)
	}

	rules, err := s.ForProfile("gaming")
	if err != nil {
		t.Fatalf("ForProfile: %v", err)
	}
	if len(rules) != 1 || rules[0].Value != "steampowered.com" {
		t.Fatalf("expected gaming's own rule, not default's, got %+v", rules)
	}
}

func TestForProfile_IgnoresDisabledRules(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if _, err := s.Create(Rule{Profile: "default", Type: "geoip", Value: "CN", Outbound: "direct", Enabled: false}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rules, err := s.ForProfile("default")
	if err != nil {
		t.Fatalf("ForProfile: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("expected disabled rule excluded, got %+v", rules)
	}
}

func TestForProfile_OrderedBySortOrder(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if _, err := s.Create(Rule{Profile: "default", SortOrder: 10, Type: "domain", Value: "second", Enabled: true}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(Rule{Profile: "default", SortOrder: 0, Type: "domain", Value: "first", Enabled: true}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rules, err := s.ForProfile("default")
	if err != nil {
		t.Fatalf("ForProfile: %v", err)
	}
	if len(rules) != 2 || rules[0].Value != "first" || rules[1].Value != "second" {
		t.Fatalf("expected sort_order ordering, got %+v", rules)
	}
}

func TestList_ReturnsAllRules(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	s.Create(Rule{Profile: "default", Type: "geoip", Value: "CN", Enabled: true})
	s.Create(Rule{Profile: "gaming", Type: "domain", Value: "steampowered.com", Enabled: false})

	all, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 rules total (including disabled), got %d", len(all))
	}
}

func TestListProfiles(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	s.Create(Rule{Profile: "default", Type: "geoip", Value: "CN", Enabled: true})
	s.Create(Rule{Profile: "gaming", Type: "domain", Value: "steampowered.com", Enabled: true})
	s.Create(Rule{Profile: "gaming", Type: "domain", Value: "valvesoftware.com", Enabled: true})

	profiles, err := s.ListProfiles()
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) != 2 || profiles[0] != "default" || profiles[1] != "gaming" {
		t.Fatalf("expected [default gaming], got %v", profiles)
	}
}
