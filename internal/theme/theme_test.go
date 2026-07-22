package theme

import (
	"bytes"
	"database/sql"
	"net/http/httptest"
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
		CREATE TABLE themes (
			slug TEXT PRIMARY KEY, name TEXT, description TEXT, logo TEXT, favicon TEXT,
			colors TEXT NOT NULL DEFAULT '{}', fonts TEXT NOT NULL DEFAULT '{}', updated_at INTEGER NOT NULL
		);
		CREATE TABLE theme_files (
			theme_slug TEXT NOT NULL, path TEXT NOT NULL, content BLOB NOT NULL, updated_at INTEGER NOT NULL,
			PRIMARY KEY (theme_slug, path)
		);
	`); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedTheme(t *testing.T, db *sql.DB, slug string, updatedAt int64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO themes (slug, name, description, logo, favicon, colors, fonts, updated_at) VALUES (?, ?, '', '', '', '{}', '{}', ?)`,
		slug, "Test Theme", updatedAt)
	if err != nil {
		t.Fatalf("seed theme: %v", err)
	}
}

func seedFile(t *testing.T, db *sql.DB, slug, path, content string, updatedAt int64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO theme_files (theme_slug, path, content, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(theme_slug, path) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`,
		slug, path, content, updatedAt)
	if err != nil {
		t.Fatalf("seed file %s: %v", path, err)
	}
}

type dummyApp struct {
	Name, Icon, Description, Download, Deeplink string
	Platforms                                   []string
}

type dummyPage struct {
	Username string
	Apps     []dummyApp
}

func TestRender_BasicThemeSet(t *testing.T) {
	db := openTestDB(t)
	seedTheme(t, db, "test", 1)
	seedFile(t, db, "test", "layout.html", `{{define "layout"}}<html>{{template "content" .}}</html>{{end}}`, 1)
	seedFile(t, db, "test", "pages/subscription.html", `{{define "content"}}hello {{.Data.Username}}{{end}}`, 1)

	e := New(db, "test")

	var buf bytes.Buffer
	if err := e.Render(&buf, dummyPage{Username: "alice"}); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("hello alice")) {
		t.Errorf("expected rendered page to contain username, got: %s", buf.String())
	}
}

func TestRender_AppCardViaDict(t *testing.T) {
	db := openTestDB(t)
	seedTheme(t, db, "test", 1)
	seedFile(t, db, "test", "layout.html", `{{define "layout"}}{{template "content" .}}{{end}}`, 1)
	seedFile(t, db, "test", "partials/app-card.html", `{{define "app-card"}}[{{.Theme.Slug}}:{{.App.Name}}]{{end}}`, 1)
	seedFile(t, db, "test", "pages/subscription.html", `{{define "content"}}{{range .Data.Apps}}{{template "app-card" (dict "Theme" $.Theme "App" .)}}{{end}}{{end}}`, 1)

	e := New(db, "test")

	var buf bytes.Buffer
	err := e.Render(&buf, dummyPage{Apps: []dummyApp{{Name: "Clash Verge"}}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("[test:Clash Verge]")) {
		t.Errorf("expected app-card output, got: %s", buf.String())
	}
}

func TestRender_HotReload(t *testing.T) {
	db := openTestDB(t)
	seedTheme(t, db, "test", 1)
	seedFile(t, db, "test", "layout.html", `{{define "layout"}}{{template "content" .}}{{end}}`, 1)
	seedFile(t, db, "test", "pages/subscription.html", `{{define "content"}}version-1{{end}}`, 1)

	e := New(db, "test")

	var buf bytes.Buffer
	if err := e.Render(&buf, nil); err != nil {
		t.Fatalf("Render (initial): %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("version-1")) {
		t.Fatalf("expected version-1, got %q", buf.String())
	}

	seedFile(t, db, "test", "pages/subscription.html", `{{define "content"}}version-2{{end}}`, 2)

	buf.Reset()
	if err := e.Render(&buf, nil); err != nil {
		t.Fatalf("Render (after edit): %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("version-2")) {
		t.Fatalf("expected hot-reloaded version-2, got %q", buf.String())
	}
}

// TestServeStatic_DeletedFileInvisibleEvenWhenMaxUpdatedAtUnchanged guards
// against a real bug: deleting a theme_files row can only ever leave the
// remaining MAX(updated_at) the same or lower, never higher, so a
// staleness check based on MAX(updated_at) alone can't detect a delete
// unless the deleted row happened to be the single most-recently-touched
// one. Here the *older* of two static files is deleted, so the survivor's
// updated_at is already equal to what was cached as the max — a MAX-only
// check would wrongly consider the cache still fresh and keep serving the
// deleted file's stale content.
func TestServeStatic_DeletedFileInvisibleEvenWhenMaxUpdatedAtUnchanged(t *testing.T) {
	db := openTestDB(t)
	seedTheme(t, db, "test", 1)
	seedFile(t, db, "test", "layout.html", `{{define "layout"}}{{template "content" .}}{{end}}`, 1)
	seedFile(t, db, "test", "pages/subscription.html", `{{define "content"}}x{{end}}`, 1)
	seedFile(t, db, "test", "static/old.txt", "old-content", 1)
	seedFile(t, db, "test", "static/newer.txt", "newer-content", 2)

	e := New(db, "test")

	req := httptest.NewRequest("GET", "/assets/test/old.txt", nil)
	rec := httptest.NewRecorder()
	found, err := e.ServeStatic(rec, req, "old.txt")
	if err != nil || !found {
		t.Fatalf("expected static/old.txt to be served initially, found=%v err=%v", found, err)
	}

	if _, err := db.Exec(`DELETE FROM theme_files WHERE theme_slug = 'test' AND path = 'static/old.txt'`); err != nil {
		t.Fatalf("delete static/old.txt: %v", err)
	}

	req = httptest.NewRequest("GET", "/assets/test/old.txt", nil)
	rec = httptest.NewRecorder()
	found, err = e.ServeStatic(rec, req, "old.txt")
	if err != nil {
		t.Fatalf("ServeStatic after delete: %v", err)
	}
	if found {
		t.Fatal("expected deleted static file to no longer be served")
	}
}

func TestServeStatic_ServesAndReloads(t *testing.T) {
	db := openTestDB(t)
	seedTheme(t, db, "test", 1)
	seedFile(t, db, "test", "layout.html", `{{define "layout"}}{{template "content" .}}{{end}}`, 1)
	seedFile(t, db, "test", "pages/subscription.html", `{{define "content"}}x{{end}}`, 1)
	seedFile(t, db, "test", "static/css/style.css", "body { color: red; }", 1)

	e := New(db, "test")

	req := httptest.NewRequest("GET", "/assets/test/css/style.css", nil)
	rec := httptest.NewRecorder()
	found, err := e.ServeStatic(rec, req, "css/style.css")
	if err != nil {
		t.Fatalf("ServeStatic: %v", err)
	}
	if !found {
		t.Fatal("expected static asset to be found")
	}
	if rec.Body.String() != "body { color: red; }" {
		t.Errorf("unexpected body: %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Error("expected content-type to be set for .css")
	}
}

func TestServeStatic_NotFound(t *testing.T) {
	db := openTestDB(t)
	seedTheme(t, db, "test", 1)
	seedFile(t, db, "test", "layout.html", `{{define "layout"}}x{{end}}`, 1)
	seedFile(t, db, "test", "pages/subscription.html", `{{define "content"}}x{{end}}`, 1)

	e := New(db, "test")
	req := httptest.NewRequest("GET", "/assets/test/does-not-exist.css", nil)
	rec := httptest.NewRecorder()

	found, err := e.ServeStatic(rec, req, "does-not-exist.css")
	if err != nil {
		t.Fatalf("ServeStatic: %v", err)
	}
	if found {
		t.Fatal("expected not found for missing asset")
	}
}

func TestNew_MissingThemeErrors(t *testing.T) {
	db := openTestDB(t)
	e := New(db, "does-not-exist")

	var buf bytes.Buffer
	if err := e.Render(&buf, nil); err == nil {
		t.Fatal("expected error for missing theme row")
	}
}
