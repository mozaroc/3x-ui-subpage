package templatestore

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
		CREATE TABLE templates (
			format TEXT NOT NULL, profile TEXT NOT NULL, protocol TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL, updated_at INTEGER NOT NULL,
			PRIMARY KEY (format, profile, protocol)
		)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestPut_AndGet(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Put("clash", "default", "", "proxies: []"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	row, err := s.Get("clash", "default", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Content != "proxies: []" {
		t.Errorf("unexpected content: %q", row.Content)
	}
}

func TestPut_OverwritesExisting(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Put("clash", "default", "", "v1"); err != nil {
		t.Fatalf("Put v1: %v", err)
	}
	if err := s.Put("clash", "default", "", "v2"); err != nil {
		t.Fatalf("Put v2: %v", err)
	}

	row, err := s.Get("clash", "default", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.Content != "v2" {
		t.Errorf("expected overwritten content v2, got %q", row.Content)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM templates`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 row after overwrite, got %d", count)
	}
}

func TestGet_NotFound(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if _, err := s.Get("clash", "missing", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestList_OrderedAndAllFormats(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Put("xray_link", "default", "vmess", "vmess-tmpl"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put("xray_link", "default", "vless", "vless-tmpl"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put("clash", "default", "", "clash-tmpl"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rows, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// "clash" sorts before "xray_link"; within xray_link, "vless" before "vmess".
	if rows[0].Format != "clash" {
		t.Errorf("expected clash first, got %s", rows[0].Format)
	}
	if rows[1].Protocol != "vless" || rows[2].Protocol != "vmess" {
		t.Errorf("expected vless before vmess, got %s then %s", rows[1].Protocol, rows[2].Protocol)
	}
}

func TestProfilesForFormats_AlwaysIncludesDefault(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	profiles, err := s.ProfilesForFormats([]string{"xray_link", "xray_json"})
	if err != nil {
		t.Fatalf("ProfilesForFormats: %v", err)
	}
	if len(profiles) != 1 || profiles[0] != "default" {
		t.Errorf("expected only default on a fresh store, got %v", profiles)
	}
}

func TestProfilesForFormats_UnionAcrossFormatsDeduped(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Put("xray_link", "gaming", "vless", "tmpl"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put("xray_json", "gaming", "", "tmpl"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put("xray_link", "minimal", "vless", "tmpl"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put("clash", "unrelated", "", "tmpl"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	profiles, err := s.ProfilesForFormats([]string{"xray_link", "xray_json"})
	if err != nil {
		t.Fatalf("ProfilesForFormats: %v", err)
	}
	want := map[string]bool{"default": true, "gaming": true, "minimal": true}
	if len(profiles) != len(want) {
		t.Fatalf("expected %d profiles, got %v", len(want), profiles)
	}
	for _, p := range profiles {
		if !want[p] {
			t.Errorf("unexpected profile %q in result %v", p, profiles)
		}
	}
	for p := range want {
		found := false
		for _, got := range profiles {
			if got == p {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %q in result, got %v", p, profiles)
		}
	}
}

func TestDelete(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.Put("clash", "gaming", "", "content"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete("clash", "gaming", ""); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("clash", "gaming", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected row to be gone after Delete, got %v", err)
	}
}
