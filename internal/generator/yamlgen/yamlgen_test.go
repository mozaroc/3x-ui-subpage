package yamlgen

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"gopkg.in/yaml.v3"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

const clashTmpl = `proxies:
{{range $c := .Clients}}  - name: "{{$c.Remark}}"
    type: {{if eq $c.Protocol "shadowsocks"}}ss{{else}}{{$c.Protocol}}{{end}}
    server: {{$c.Server}}
    port: {{$c.Port}}
{{if eq $c.Security "reality"}}    reality-opts:
      public-key: {{$c.PublicKey}}
{{end}}{{end}}
proxy-groups:
  - name: PROXY
    type: select
    proxies:
{{range $c := .Clients}}      - "{{$c.Remark}}"
{{end}}      - DIRECT
`

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

func insertTemplate(t *testing.T, db *sql.DB, format, profile, content string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO templates (format, profile, protocol, content, updated_at) VALUES (?, ?, '', ?, 1)`,
		format, profile, content)
	if err != nil {
		t.Fatalf("insert template: %v", err)
	}
}

func multiProtoClients() []domain.MatchedClient {
	return []domain.MatchedClient{
		{
			Protocol: domain.ProtocolVLESS,
			Remark:   "vless-node",
			Server:   "vpn.example.com",
			Port:     443,
			Client:   domain.ClientAccount{ID: "uuid-1", Email: "vless-node", Flow: "xtls-rprx-vision"},
			Stream: domain.StreamSettings{
				Network:  domain.NetworkTCP,
				Security: domain.SecurityReality,
				TLS:      domain.TLSSettings{SNI: "example.com", Fingerprint: "chrome", PublicKey: "pub", ShortID: "sid"},
			},
		},
		{
			Protocol: domain.ProtocolShadowsocks,
			Remark:   "ss-node",
			Server:   "vpn.example.com",
			Port:     8388,
			Client:   domain.ClientAccount{Method: "aes-256-gcm", Password: "pw", Email: "ss-node"},
			Stream:   domain.StreamSettings{Network: domain.NetworkTCP, Security: domain.SecurityNone},
		},
	}
}

func TestBuild_ProducesValidYAML(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", clashTmpl)
	g := New(db, "clash")

	out, err := g.Build(multiProtoClients(), "default")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, out)
	}

	proxies, ok := parsed["proxies"].([]any)
	if !ok || len(proxies) != 2 {
		t.Fatalf("expected 2 proxies, got %v", parsed["proxies"])
	}
	if !strings.Contains(out, "public-key: pub") {
		t.Errorf("expected reality public-key in output:\n%s", out)
	}
}

func TestBuild_DifferentFormatsAreIndependent(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", clashTmpl)
	insertTemplate(t, db, "mihomo", "default", "tun:\n  enable: true\nproxies: []\n")

	clashGen := New(db, "clash")
	mihomoGen := New(db, "mihomo")

	clashOut, err := clashGen.Build(nil, "default")
	if err != nil {
		t.Fatalf("clash Build: %v", err)
	}
	mihomoOut, err := mihomoGen.Build(nil, "default")
	if err != nil {
		t.Fatalf("mihomo Build: %v", err)
	}
	if !strings.Contains(mihomoOut, "tun:") {
		t.Errorf("expected mihomo-specific content, got: %s", mihomoOut)
	}
	if strings.Contains(clashOut, "tun:") {
		t.Errorf("clash output should not contain mihomo content: %s", clashOut)
	}
}

func TestBuild_ProfileOverride(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", "proxies: []\n")
	insertTemplate(t, db, "clash", "gaming", "proxies: []\nextra: gaming-marker\n")
	g := New(db, "clash")

	out, err := g.Build(nil, "gaming")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "gaming-marker") {
		t.Errorf("expected gaming profile template to be used, got: %s", out)
	}
}

func TestBuild_InvalidYAMLFails(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", "{{.Clients}}: [unterminated")
	g := New(db, "clash")

	if _, err := g.Build(nil, "default"); err == nil {
		t.Fatal("expected error for invalid YAML output")
	}
}

func TestBuild_EmptyClients(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", clashTmpl)
	g := New(db, "clash")

	out, err := g.Build(nil, "default")
	if err != nil {
		t.Fatalf("Build with no clients: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, out)
	}
}
