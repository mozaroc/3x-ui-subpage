package xrayjson

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

// realShippedTemplate is this project's own default xray_json template
// (web/templates/xray/full-config.json.tmpl) — used to exercise the actual
// content administrators get out of the box, not just a minimal test
// fixture, since bugs in the shipped template itself (e.g. unescaped fields,
// wrong ALPN shape) wouldn't be caught by the synthetic fullConfigTmpl above.
func realShippedTemplate(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile("../../../web/templates/xray/full-config.json.tmpl")
	if err != nil {
		t.Fatalf("read shipped template: %v", err)
	}
	return string(content)
}

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

func TestBuild_ShippedTemplate_EscapesQuotesInRemarkAndPassword(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`
		INSERT INTO templates (format, profile, protocol, content, updated_at) VALUES ('xray_json', 'quoted', '', ?, 1)`,
		realShippedTemplate(t)); err != nil {
		t.Fatalf("seed shipped template: %v", err)
	}
	g := New(db)

	clients := []domain.MatchedClient{
		{
			Remark:   `evil " tag`,
			Protocol: domain.ProtocolTrojan,
			Server:   "vpn.example.com",
			Port:     8443,
			Client:   domain.ClientAccount{Password: `pa"ss\word`, Email: "alice"},
		},
	}

	out, err := g.Build(clients, "quoted")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var parsed struct {
		Outbounds []struct {
			Tag      string `json:"tag"`
			Settings struct {
				Servers []struct {
					Password string `json:"password"`
				} `json:"servers"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(parsed.Outbounds) != 1 {
		t.Fatalf("expected 1 outbound, got %d", len(parsed.Outbounds))
	}
	if want := "evil \" tag + alice"; parsed.Outbounds[0].Tag != want {
		t.Errorf("expected tag %q to round-trip with its quote intact, got %q", want, parsed.Outbounds[0].Tag)
	}
	if got := parsed.Outbounds[0].Settings.Servers[0].Password; got != `pa"ss\word` {
		t.Errorf("expected password to round-trip with its quote/backslash intact, got %q", got)
	}
}

func TestBuild_ShippedTemplate_ALPNRendersAsJSONArray(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`
		INSERT INTO templates (format, profile, protocol, content, updated_at) VALUES ('xray_json', 'alpn', '', ?, 1)`,
		realShippedTemplate(t)); err != nil {
		t.Fatalf("seed shipped template: %v", err)
	}
	g := New(db)

	clients := []domain.MatchedClient{
		{
			Remark:   "alpn-client",
			Protocol: domain.ProtocolVLESS,
			Server:   "vpn.example.com",
			Port:     443,
			Client:   domain.ClientAccount{ID: "uuid-1", Email: "alice"},
			Stream: domain.StreamSettings{
				Security: domain.SecurityTLS,
				TLS:      domain.TLSSettings{ALPN: []string{"h2", "http/1.1"}},
			},
		},
	}

	out, err := g.Build(clients, "alpn")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var parsed struct {
		Outbounds []struct {
			StreamSettings struct {
				TLSSettings struct {
					ALPN []string `json:"alpn"`
				} `json:"tlsSettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(parsed.Outbounds) != 1 {
		t.Fatalf("expected 1 outbound, got %d", len(parsed.Outbounds))
	}
	got := parsed.Outbounds[0].StreamSettings.TLSSettings.ALPN
	want := []string{"h2", "http/1.1"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("expected alpn array %v, got %v", want, got)
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
