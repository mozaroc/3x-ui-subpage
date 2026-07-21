package tmplcache

import (
	"database/sql"
	"strings"
	"testing"
	"text/template"

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

func insertTemplate(t *testing.T, db *sql.DB, format, profile, protocol, content string, updatedAt int64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO templates (format, profile, protocol, content, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(format, profile, protocol) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`,
		format, profile, protocol, content, updatedAt)
	if err != nil {
		t.Fatalf("insert template: %v", err)
	}
}

func simpleParse(name, content string) (*template.Template, error) {
	return template.New(name).Parse(content)
}

func execute(t *testing.T, tmpl *template.Template, data any) string {
	t.Helper()
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return sb.String()
}

func TestGet_ExactProfileMatch(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", "", "default-content", 1)
	insertTemplate(t, db, "clash", "gaming", "", "gaming-content", 1)

	c := New(db, "clash", simpleParse)

	tmpl, err := c.Get("gaming", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := execute(t, tmpl, nil); got != "gaming-content" {
		t.Errorf("expected gaming-content, got %q", got)
	}
}

func TestGet_FallsBackToDefault(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", "", "default-content", 1)

	c := New(db, "clash", simpleParse)

	tmpl, err := c.Get("nonexistent-profile", "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := execute(t, tmpl, nil); got != "default-content" {
		t.Errorf("expected fallback to default-content, got %q", got)
	}
}

func TestGet_NoDefaultFails(t *testing.T) {
	db := openTestDB(t)
	c := New(db, "clash", simpleParse)

	if _, err := c.Get("anything", ""); err == nil {
		t.Fatal("expected error when neither profile nor default exists")
	}
}

func TestGet_PerProtocolLookup(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "xray_link", "default", "vless", "vless-tmpl", 1)
	insertTemplate(t, db, "xray_link", "default", "vmess", "vmess-tmpl", 1)

	c := New(db, "xray_link", simpleParse)

	vless, err := c.Get("default", "vless")
	if err != nil {
		t.Fatalf("Get vless: %v", err)
	}
	if got := execute(t, vless, nil); got != "vless-tmpl" {
		t.Errorf("expected vless-tmpl, got %q", got)
	}

	vmess, err := c.Get("default", "vmess")
	if err != nil {
		t.Fatalf("Get vmess: %v", err)
	}
	if got := execute(t, vmess, nil); got != "vmess-tmpl" {
		t.Errorf("expected vmess-tmpl, got %q", got)
	}
}

func TestGet_HotReloadOnRowChange(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", "", "v1", 1)

	c := New(db, "clash", simpleParse)

	tmpl, err := c.Get("default", "")
	if err != nil {
		t.Fatalf("Get (initial): %v", err)
	}
	if got := execute(t, tmpl, nil); got != "v1" {
		t.Fatalf("expected v1, got %q", got)
	}

	insertTemplate(t, db, "clash", "default", "", "v2", 2)

	tmpl, err = c.Get("default", "")
	if err != nil {
		t.Fatalf("Get (after update): %v", err)
	}
	if got := execute(t, tmpl, nil); got != "v2" {
		t.Fatalf("expected reloaded v2, got %q", got)
	}
}

func TestGet_CachesUnchangedRow(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", "", "v1", 1)

	c := New(db, "clash", func(name, content string) (*template.Template, error) {
		return template.New(name).Parse(content + "-parsed-once")
	})

	tmpl1, err := c.Get("default", "")
	if err != nil {
		t.Fatalf("Get (1st): %v", err)
	}
	tmpl2, err := c.Get("default", "")
	if err != nil {
		t.Fatalf("Get (2nd): %v", err)
	}
	if tmpl1 != tmpl2 {
		t.Error("expected same *template.Template instance when row is unchanged")
	}
}
