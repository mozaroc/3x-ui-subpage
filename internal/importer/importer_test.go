package importer

import (
	"database/sql"
	"os"
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
	if err := db.QueryRow(`SELECT name FROM applications WHERE name = 'Prizrak-Box'`).Scan(&name); err != nil {
		t.Fatalf("expected Prizrak-Box to be imported: %v", err)
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
		{"xray_json", "default", ""},
		{"clash", "default", ""},
		{"mihomo", "default", ""},
		{"happ", "default", ""},
		{"incy", "default", ""},
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

// copyWebDir copies the checked-in web/ tree into a fresh temp dir so a test
// can mutate it (remove a template file) without touching the repo.
func copyWebDir(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(repoWebDir)); err != nil {
		t.Fatalf("copy web dir: %v", err)
	}
	return dst
}

func TestImport_PrunesOrphanedTemplateAfterFileRemoved(t *testing.T) {
	db := openTestDB(t)
	webDir := copyWebDir(t)

	// A synthetic extra clash profile -- no shipped format has more than one
	// profile file, so this is added purely to exercise the
	// add-then-remove-a-profile-file path.
	gamingPath := filepath.Join(webDir, "templates", "clash", "gaming.yaml.tmpl")
	if err := os.WriteFile(gamingPath, []byte("proxies: []\n"), 0o644); err != nil {
		t.Fatalf("write gaming.yaml.tmpl: %v", err)
	}

	if err := Import(db, webDir); err != nil {
		t.Fatalf("first Import: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM templates WHERE format='clash' AND profile='gaming'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected gaming template seeded before removal, got count=%d", count)
	}

	if err := os.Remove(gamingPath); err != nil {
		t.Fatalf("remove gaming.yaml.tmpl: %v", err)
	}
	if err := Import(db, webDir); err != nil {
		t.Fatalf("second Import: %v", err)
	}

	if err := db.QueryRow(`SELECT COUNT(*) FROM templates WHERE format='clash' AND profile='gaming'`).Scan(&count); err != nil {
		t.Fatalf("count after removal: %v", err)
	}
	if count != 0 {
		t.Errorf("expected orphaned gaming template to be pruned after re-import, got count=%d", count)
	}
}

func TestImport_DoesNotPruneAdminEditedRow(t *testing.T) {
	db := openTestDB(t)
	webDir := copyWebDir(t)

	gamingPath := filepath.Join(webDir, "templates", "clash", "gaming.yaml.tmpl")
	if err := os.WriteFile(gamingPath, []byte("proxies: []\n"), 0o644); err != nil {
		t.Fatalf("write gaming.yaml.tmpl: %v", err)
	}

	if err := Import(db, webDir); err != nil {
		t.Fatalf("first Import: %v", err)
	}

	// Simulate an admin hand-editing this template via /admin after import.
	if _, err := db.Exec(`
		UPDATE templates SET content = 'admin-customized', updated_at = updated_at + 1
		WHERE format='clash' AND profile='gaming'`); err != nil {
		t.Fatalf("simulate admin edit: %v", err)
	}

	if err := os.Remove(gamingPath); err != nil {
		t.Fatalf("remove gaming.yaml.tmpl: %v", err)
	}
	if err := Import(db, webDir); err != nil {
		t.Fatalf("second Import: %v", err)
	}

	var content string
	if err := db.QueryRow(`SELECT content FROM templates WHERE format='clash' AND profile='gaming'`).Scan(&content); err != nil {
		t.Fatalf("expected admin-edited row to survive re-import: %v", err)
	}
	if content != "admin-customized" {
		t.Errorf("expected admin-edited content to survive re-import untouched, got %q", content)
	}
}

// TestImport_ReimportPreservesAdminEditedTemplate is the direct regression
// test for the reported bug: an admin edits a default template via
// /admin/templates, then the box is updated (which re-runs -import against
// the same bundled web/ tree) -- the admin's edit must survive untouched,
// not get silently reset back to the shipped default.
func TestImport_ReimportPreservesAdminEditedTemplate(t *testing.T) {
	db := openTestDB(t)
	webDir := copyWebDir(t)

	if err := Import(db, webDir); err != nil {
		t.Fatalf("first Import: %v", err)
	}

	if _, err := db.Exec(`
		UPDATE templates SET content = 'admin-customized-mihomo', updated_at = updated_at + 1
		WHERE format = 'mihomo' AND profile = 'default'`); err != nil {
		t.Fatalf("simulate admin edit: %v", err)
	}

	// The bundled template file is untouched on disk -- this mirrors
	// exactly what `install.sh --update` does: re-run -import against the
	// same web/ tree, file present, content unchanged from what shipped.
	if err := Import(db, webDir); err != nil {
		t.Fatalf("second Import (simulating --update): %v", err)
	}

	var content string
	if err := db.QueryRow(`SELECT content FROM templates WHERE format = 'mihomo' AND profile = 'default'`).Scan(&content); err != nil {
		t.Fatalf("query mihomo template: %v", err)
	}
	if content != "admin-customized-mihomo" {
		t.Errorf("expected admin-edited mihomo template to survive re-import, got %q", content)
	}

	// A third re-import must still preserve it -- not just the one
	// immediately after the edit.
	if err := Import(db, webDir); err != nil {
		t.Fatalf("third Import: %v", err)
	}
	if err := db.QueryRow(`SELECT content FROM templates WHERE format = 'mihomo' AND profile = 'default'`).Scan(&content); err != nil {
		t.Fatalf("query mihomo template (3rd): %v", err)
	}
	if content != "admin-customized-mihomo" {
		t.Errorf("expected admin-edited mihomo template to still survive a second re-import, got %q", content)
	}
}

// TestImport_ReimportRefreshesUntouchedTemplateOnUpstreamChange proves the
// preservation logic doesn't just freeze every template forever: a row the
// admin never touched must still pick up a real content change shipped in
// a newer release (e.g. a bug fix to the default template itself).
func TestImport_ReimportRefreshesUntouchedTemplateOnUpstreamChange(t *testing.T) {
	db := openTestDB(t)
	webDir := copyWebDir(t)

	if err := Import(db, webDir); err != nil {
		t.Fatalf("first Import: %v", err)
	}

	xrayJSONPath := filepath.Join(webDir, "templates", "xray_json", "default.tmpl")
	if err := os.WriteFile(xrayJSONPath, []byte(`{"updated":true}`), 0o644); err != nil {
		t.Fatalf("simulate upstream template change: %v", err)
	}

	if err := Import(db, webDir); err != nil {
		t.Fatalf("second Import: %v", err)
	}

	var content string
	if err := db.QueryRow(`SELECT content FROM templates WHERE format = 'xray_json' AND profile = 'default'`).Scan(&content); err != nil {
		t.Fatalf("query xray_json template: %v", err)
	}
	if content != `{"updated":true}` {
		t.Errorf("expected untouched xray_json template to pick up the new shipped content, got %q", content)
	}
}

// TestImport_LegacyRowWithNoManifestHistoryIsNeverAutoRefreshed covers
// upgrading from a pre-manifest binary version: a templates row exists
// (seeded by an older -import, or hand-edited) but has no import_manifest
// entry at all, since that table didn't exist yet. Such a row must be
// treated the same as an admin-edited one -- left alone, permanently,
// across any number of re-imports -- since there's no way to know whether
// it was ever customized.
func TestImport_LegacyRowWithNoManifestHistoryIsNeverAutoRefreshed(t *testing.T) {
	db := openTestDB(t)
	webDir := copyWebDir(t)

	// Seed a templates row directly, bypassing Import, with no
	// import_manifest entry -- simulates a DB from before this table
	// existed.
	if _, err := db.Exec(`
		INSERT INTO templates (format, profile, protocol, content, updated_at)
		VALUES ('mihomo', 'default', '', 'pre-existing-legacy-content', 12345)`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := Import(db, webDir); err != nil {
			t.Fatalf("Import (call %d): %v", i, err)
		}
		var content string
		if err := db.QueryRow(`SELECT content FROM templates WHERE format = 'mihomo' AND profile = 'default'`).Scan(&content); err != nil {
			t.Fatalf("query mihomo template (call %d): %v", i, err)
		}
		if content != "pre-existing-legacy-content" {
			t.Errorf("call %d: expected legacy row to be left untouched, got %q", i, content)
		}
	}
}
