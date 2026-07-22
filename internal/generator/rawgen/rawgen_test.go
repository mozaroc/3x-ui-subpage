package rawgen

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

const happTmpl = `{"servers":[{{range $i, $c := .Clients}}{{if $i}},{{end}}{"name":"{{$c.Remark}}","server":"{{$c.Server}}"}{{end}}],"rules":[{{range $i, $r := .Rules}}{{if $i}},{{end}}{"type":"{{$r.Type}}","value":"{{$r.Value}}","outbound":"{{$r.Outbound}}"}{{end}}]}`

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
		t.Fatalf("create templates table: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE routing_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			profile TEXT NOT NULL, sort_order INTEGER NOT NULL DEFAULT 0,
			type TEXT NOT NULL, value TEXT NOT NULL, outbound TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1, updated_at INTEGER NOT NULL
		)`); err != nil {
		t.Fatalf("create routing_rules table: %v", err)
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

func insertRule(t *testing.T, db *sql.DB, profile, typ, value, outbound string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO routing_rules (profile, sort_order, type, value, outbound, enabled, updated_at) VALUES (?, 0, ?, ?, ?, 1, 1)`,
		profile, typ, value, outbound)
	if err != nil {
		t.Fatalf("insert rule: %v", err)
	}
}

func oneClient() []domain.MatchedClient {
	return []domain.MatchedClient{
		{Protocol: domain.ProtocolVLESS, Remark: "node-1", Server: "vpn.example.com", Port: 443, Client: domain.ClientAccount{ID: "uuid-1", Email: "node-1"}},
	}
}

func TestBuild_IncludesClientsAndRules(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "happ", "default", happTmpl)
	insertRule(t, db, "default", "geoip", "CN", "direct")

	g := New(db, "happ")
	out, err := g.Build(oneClient(), "default")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, `"name":"node-1 + node-1"`) {
		t.Errorf("expected combined inbound+client name in output, got: %s", out)
	}
	if !strings.Contains(out, `"type":"geoip"`) {
		t.Errorf("expected routing rule in output, got: %s", out)
	}
}

func TestBuild_NoOutputValidation(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "happ", "default", "not valid json or yaml {{.Clients}}")

	g := New(db, "happ")
	out, err := g.Build(nil, "default")
	if err != nil {
		t.Fatalf("Build should not validate output format, got error: %v", err)
	}
	if !strings.Contains(out, "not valid json or yaml") {
		t.Errorf("expected raw template output preserved, got: %s", out)
	}
}

func TestBuild_ProfileFallbackIndependentForTemplateAndRules(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "happ", "default", happTmpl)
	insertRule(t, db, "default", "geoip", "CN", "direct")
	// "gaming" has no template row and no rule row of its own -- both should
	// fall back to "default" independently.

	g := New(db, "happ")
	out, err := g.Build(oneClient(), "gaming")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, `"type":"geoip"`) {
		t.Errorf("expected rules to fall back to default profile, got: %s", out)
	}
}

func TestBuild_FormatsAreIndependent(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "happ", "default", `{"happ":true}`)
	insertTemplate(t, db, "incy", "default", `{"incy":true}`)

	happOut, err := New(db, "happ").Build(nil, "default")
	if err != nil {
		t.Fatalf("happ Build: %v", err)
	}
	incyOut, err := New(db, "incy").Build(nil, "default")
	if err != nil {
		t.Fatalf("incy Build: %v", err)
	}
	if !strings.Contains(happOut, "happ") || strings.Contains(happOut, "incy") {
		t.Errorf("happ output crossed with incy: %s", happOut)
	}
	if !strings.Contains(incyOut, "incy") || strings.Contains(incyOut, "happ") {
		t.Errorf("incy output crossed with happ: %s", incyOut)
	}
}
