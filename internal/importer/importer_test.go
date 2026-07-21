package importer

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/irazin/3x-ui-subpage/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// repoWebDir locates the checked-in web/ tree relative to this test file.
const repoWebDir = "../../web"

func TestImport_ApplicationsRoundTrip(t *testing.T) {
	db := openTestDB(t)
	if err := Import(db, repoWebDir); err != nil {
		t.Fatalf("Import: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM applications`).Scan(&count); err != nil {
		t.Fatalf("count applications: %v", err)
	}
	if count == 0 {
		t.Fatal("expected at least one imported application")
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM applications WHERE name = 'Hiddify'`).Scan(&name); err != nil {
		t.Fatalf("expected Hiddify to be imported: %v", err)
	}
}

func TestImport_ThemeRoundTrip(t *testing.T) {
	db := openTestDB(t)
	if err := Import(db, repoWebDir); err != nil {
		t.Fatalf("Import: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM themes WHERE slug = 'default'`).Scan(&name); err != nil {
		t.Fatalf("expected default theme row: %v", err)
	}

	var fileCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM theme_files WHERE theme_slug = 'default'`).Scan(&fileCount); err != nil {
		t.Fatalf("count theme_files: %v", err)
	}
	if fileCount == 0 {
		t.Fatal("expected theme_files rows for default theme")
	}

	var layoutExists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM theme_files WHERE theme_slug = 'default' AND path = 'layout.html'`).Scan(&layoutExists); err != nil {
		t.Fatalf("query layout.html: %v", err)
	}
	if layoutExists != 1 {
		t.Error("expected layout.html to be imported as a theme_files row")
	}

	var staticExists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM theme_files WHERE theme_slug = 'default' AND path = 'static/css/style.css'`).Scan(&staticExists); err != nil {
		t.Fatalf("query static css: %v", err)
	}
	if staticExists != 1 {
		t.Error("expected static/css/style.css to be imported as a theme_files row")
	}
}

func TestImport_TemplatesRoundTrip(t *testing.T) {
	db := openTestDB(t)
	if err := Import(db, repoWebDir); err != nil {
		t.Fatalf("Import: %v", err)
	}

	cases := []struct {
		format, profile, protocol string
	}{
		{"xray_link", "default", "vless"},
		{"xray_link", "default", "vmess"},
		{"xray_link", "default", "trojan"},
		{"xray_link", "default", "shadowsocks"},
		{"xray_json", "default", ""},
		{"clash", "default", ""},
		{"mihomo", "default", ""},
	}

	for _, c := range cases {
		var content string
		err := db.QueryRow(`SELECT content FROM templates WHERE format=? AND profile=? AND protocol=?`, c.format, c.profile, c.protocol).Scan(&content)
		if err != nil {
			t.Errorf("expected template (%s,%s,%s) to be imported: %v", c.format, c.profile, c.protocol, err)
			continue
		}
		if content == "" {
			t.Errorf("expected non-empty content for (%s,%s,%s)", c.format, c.profile, c.protocol)
		}
	}
}

func TestImport_IsRerunnable(t *testing.T) {
	db := openTestDB(t)
	if err := Import(db, repoWebDir); err != nil {
		t.Fatalf("first Import: %v", err)
	}
	if err := Import(db, repoWebDir); err != nil {
		t.Fatalf("second Import: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM applications`).Scan(&count); err != nil {
		t.Fatalf("count applications: %v", err)
	}
	if count == 0 {
		t.Fatal("expected applications to survive a second import")
	}

	var templateCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM templates WHERE format='clash' AND profile='default'`).Scan(&templateCount); err != nil {
		t.Fatalf("count clash templates: %v", err)
	}
	if templateCount != 1 {
		t.Errorf("expected exactly 1 clash/default row after re-import, got %d", templateCount)
	}
}
