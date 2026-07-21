package xrayjson

import (
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

const fullConfigTmpl = `{
  "log": {"loglevel": "warning"},
  "outbounds": [
{{range $i, $c := .Clients}}{{if $i}},
{{end}}    {"tag": "proxy-{{$i}}", "protocol": "{{$c.Protocol}}", "settings": {}}
{{end}}
  ]
}`

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
	if _, err := db.Exec(`INSERT INTO templates (format, profile, protocol, content, updated_at) VALUES ('xray_json', 'default', '', ?, 1)`, fullConfigTmpl); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestBuild_ValidJSONWithMultipleClients(t *testing.T) {
	db := openTestDB(t)
	g := New(db)

	clients := []domain.MatchedClient{
		{Protocol: domain.ProtocolVLESS, Server: "vpn.example.com", Port: 443, Client: domain.ClientAccount{ID: "uuid-1", Email: "alice"}},
		{Protocol: domain.ProtocolTrojan, Server: "vpn.example.com", Port: 8443, Client: domain.ClientAccount{Password: "pw", Email: "bob"}},
	}

	out, err := g.Build(clients, "default")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	outbounds, ok := parsed["outbounds"].([]any)
	if !ok || len(outbounds) != 2 {
		t.Fatalf("expected 2 outbounds, got %v", parsed["outbounds"])
	}
}

func TestBuild_EmptyClients(t *testing.T) {
	db := openTestDB(t)
	g := New(db)

	out, err := g.Build(nil, "default")
	if err != nil {
		t.Fatalf("Build with no clients: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
}

func TestBuild_ProfileFallback(t *testing.T) {
	db := openTestDB(t)
	g := New(db)

	out, err := g.Build(nil, "nonexistent-profile")
	if err != nil {
		t.Fatalf("Build should fall back to default profile: %v", err)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("expected valid JSON, got: %s", out)
	}
}
