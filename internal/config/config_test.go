package config

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertSetting(t *testing.T, db *sql.DB, key, value string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, 1)`, key, value); err != nil {
		t.Fatalf("insert setting %s: %v", key, err)
	}
}

func TestLoadFromDB_DefaultsWhenNoRows(t *testing.T) {
	db := openTestDB(t)
	insertSetting(t, db, "xui", `{"base_url":"http://127.0.0.1:2053","username":"admin","password":"admin"}`)
	insertSetting(t, db, "subscription", `{"public_url":"https://sub.example.com","server_host":"vpn.example.com"}`)

	cfg, err := LoadFromDB(db)
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	if cfg.Server.Listen != "0.0.0.0:8080" {
		t.Errorf("expected default listen addr, got %q", cfg.Server.Listen)
	}
	if cfg.Theme.Active != "default" {
		t.Errorf("expected default theme active, got %q", cfg.Theme.Active)
	}
	if cfg.QR.Size != 256 {
		t.Errorf("expected default qr size 256, got %d", cfg.QR.Size)
	}
}

func TestLoadFromDB_OverlaysStoredSections(t *testing.T) {
	db := openTestDB(t)
	insertSetting(t, db, "xui", `{"base_url":"http://127.0.0.1:2053","username":"admin","password":"admin"}`)
	insertSetting(t, db, "subscription", `{"public_url":"https://sub.example.com","server_host":"vpn.example.com"}`)
	insertSetting(t, db, "server", `{"listen":"127.0.0.1:9999"}`)
	insertSetting(t, db, "theme", `{"active":"gaming"}`)

	cfg, err := LoadFromDB(db)
	if err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:9999" {
		t.Errorf("expected overridden listen addr, got %q", cfg.Server.Listen)
	}
	if cfg.Theme.Active != "gaming" {
		t.Errorf("expected overridden theme active, got %q", cfg.Theme.Active)
	}
	if cfg.XUI.BaseURL != "http://127.0.0.1:2053" {
		t.Errorf("expected xui base_url set, got %q", cfg.XUI.BaseURL)
	}
}

func TestLoadFromDB_MissingRequiredFieldsFails(t *testing.T) {
	db := openTestDB(t)
	// no xui/subscription rows at all
	if _, err := LoadFromDB(db); err == nil {
		t.Fatal("expected validation error when xui/subscription settings are missing")
	}
}

func TestLoadFromDB_UnknownKeyIgnored(t *testing.T) {
	db := openTestDB(t)
	insertSetting(t, db, "xui", `{"base_url":"http://127.0.0.1:2053","username":"admin","password":"admin"}`)
	insertSetting(t, db, "subscription", `{"public_url":"https://sub.example.com","server_host":"vpn.example.com"}`)
	insertSetting(t, db, "some_future_section", `{"whatever":"value"}`)

	if _, err := LoadFromDB(db); err != nil {
		t.Fatalf("expected unknown settings key to be ignored, got error: %v", err)
	}
}

func TestLoadBootstrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.yaml")
	if err := os.WriteFile(path, []byte("database:\n  path: /var/lib/subscription-service/data.db\n"), 0o644); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}

	dbPath, err := LoadBootstrap(path)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if dbPath != "/var/lib/subscription-service/data.db" {
		t.Errorf("unexpected db path: %q", dbPath)
	}
}

func TestLoadBootstrap_MissingPathFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.yaml")
	if err := os.WriteFile(path, []byte("database:\n  path: \"\"\n"), 0o644); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}

	if _, err := LoadBootstrap(path); err == nil {
		t.Fatal("expected error for empty database.path")
	}
}

func TestLoadBootstrap_FileNotFound(t *testing.T) {
	if _, err := LoadBootstrap("/nonexistent/bootstrap.yaml"); err == nil {
		t.Fatal("expected error for missing bootstrap file")
	}
}

func TestSaveSetting_AndGetSetting(t *testing.T) {
	db := openTestDB(t)

	if err := SaveSetting(db, "support", []byte(`{"telegram":"https://t.me/example"}`)); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}

	value, err := GetSetting(db, "support")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if value != `{"telegram":"https://t.me/example"}` {
		t.Errorf("unexpected value: %q", value)
	}
}

func TestSaveSetting_OverwritesExisting(t *testing.T) {
	db := openTestDB(t)

	if err := SaveSetting(db, "support", []byte(`{"telegram":"v1"}`)); err != nil {
		t.Fatalf("SaveSetting v1: %v", err)
	}
	if err := SaveSetting(db, "support", []byte(`{"telegram":"v2"}`)); err != nil {
		t.Fatalf("SaveSetting v2: %v", err)
	}

	value, err := GetSetting(db, "support")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if value != `{"telegram":"v2"}` {
		t.Errorf("expected overwritten value, got %q", value)
	}
}

func TestSaveSetting_InvalidJSONRejected(t *testing.T) {
	db := openTestDB(t)
	if err := SaveSetting(db, "support", []byte(`{not valid json`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSaveSetting_KnownKeySchemaMismatchRejected(t *testing.T) {
	db := openTestDB(t)
	// "logging" is a known key; its value must decode onto LoggingConfig.
	if err := SaveSetting(db, "logging", []byte(`["not", "an", "object"]`)); err == nil {
		t.Fatal("expected error for value that doesn't match the logging schema")
	}
}

func TestSaveSetting_UnknownKeyStillAcceptedIfValidJSON(t *testing.T) {
	db := openTestDB(t)
	if err := SaveSetting(db, "some_future_section", []byte(`{"whatever":true}`)); err != nil {
		t.Fatalf("expected unknown key with valid JSON to be accepted, got: %v", err)
	}
}

func TestGetSetting_NotFound(t *testing.T) {
	db := openTestDB(t)
	if _, err := GetSetting(db, "does-not-exist"); err != ErrSettingNotFound {
		t.Errorf("expected ErrSettingNotFound, got %v", err)
	}
}

func TestListSettings(t *testing.T) {
	db := openTestDB(t)
	SaveSetting(db, "support", []byte(`{"telegram":"x"}`))
	SaveSetting(db, "qr", []byte(`{"size":256}`))

	all, err := ListSettings(db)
	if err != nil {
		t.Fatalf("ListSettings: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 settings, got %d", len(all))
	}
	if all["support"] != `{"telegram":"x"}` {
		t.Errorf("unexpected support value: %q", all["support"])
	}
}
