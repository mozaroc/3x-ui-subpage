package xui

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/irazin/3x-ui-subpage/internal/store"
)

func TestDynamicClient_ReloadsWhenSettingsChange(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeEnvelope(w, []Inbound{{ID: 1, Protocol: "vless", Enable: true}})
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	insertXUISetting(t, db, srv.URL, "key-one")
	insertRequiredSubscriptionSetting(t, db)

	d := NewDynamic(db, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := d.ListInbounds(t.Context()); err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if gotAuth != "Bearer key-one" {
		t.Fatalf("expected first key used, got %q", gotAuth)
	}

	// Simulate an admin editing Settings -> xui while the process is
	// running: update the row and bump updated_at, same as
	// config.SaveSetting does.
	insertXUISetting(t, db, srv.URL, "key-two")

	if _, err := d.ListInbounds(t.Context()); err != nil {
		t.Fatalf("ListInbounds after settings change: %v", err)
	}
	if gotAuth != "Bearer key-two" {
		t.Fatalf("expected reloaded key after settings change, got %q", gotAuth)
	}
}

func TestDynamicClient_NoReloadWhenSettingsUnchanged(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeEnvelope(w, []Inbound{{ID: 1}})
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	insertXUISetting(t, db, srv.URL, "same-key")
	insertRequiredSubscriptionSetting(t, db)

	d := NewDynamic(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < 3; i++ {
		if _, err := d.ListInbounds(t.Context()); err != nil {
			t.Fatalf("ListInbounds: %v", err)
		}
	}
	if calls != 3 {
		t.Fatalf("expected 3 upstream calls, got %d", calls)
	}
}

func insertXUISetting(t *testing.T, db *sql.DB, baseURL, apiKey string) {
	t.Helper()
	value := `{"base_url":"` + baseURL + `","api_key":"` + apiKey + `","timeout":5000000000,"retry":{"max_attempts":2,"backoff":1000000}}`
	_, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('xui', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		value, time.Now().UnixNano())
	if err != nil {
		t.Fatalf("insert xui setting: %v", err)
	}
}

func insertRequiredSubscriptionSetting(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('subscription', ?, ?)
		ON CONFLICT(key) DO NOTHING`,
		`{"public_url":"https://sub.example.com","server_host":"vpn.example.com"}`, time.Now().UnixNano())
	if err != nil {
		t.Fatalf("insert subscription setting: %v", err)
	}
}
