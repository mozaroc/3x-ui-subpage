package rawgen

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

// readShippedTemplate loads this project's own default template for format
// (web/templates/<format>/default.tmpl) — exercises the actual content
// administrators get out of the box, not just a minimal test fixture.
func readShippedTemplate(t *testing.T, format string) string {
	t.Helper()
	content, err := os.ReadFile("../../../web/templates/" + format + "/default.tmpl")
	if err != nil {
		t.Fatalf("read shipped %s template: %v", format, err)
	}
	return string(content)
}

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

func TestBuild_ShippedHappTemplate_ProducesValidXrayCoreShapedJSON(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "happ", "default", readShippedTemplate(t, "happ"))
	insertRule(t, db, "default", "geoip", "CN", "direct")

	clients := []domain.MatchedClient{
		{
			Remark:   `evil " remark`,
			Protocol: domain.ProtocolVLESS,
			Server:   "vpn.example.com",
			Port:     443,
			Client:   domain.ClientAccount{ID: "uuid-1", Email: "alice", Flow: "xtls-rprx-vision"},
			Stream: domain.StreamSettings{
				Network:  domain.NetworkTCP,
				Security: domain.SecurityReality,
				TLS:      domain.TLSSettings{SNI: "example.com", Fingerprint: "chrome", PublicKey: "pk", ShortID: "sid"},
			},
		},
		{
			Protocol: domain.ProtocolTrojan,
			Server:   "vpn2.example.com",
			Port:     8443,
			Client:   domain.ClientAccount{Password: "pw", Email: "bob"},
		},
	}

	g := New(db, "happ")
	out, err := g.Build(clients, "default")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var parsed []struct {
		Remarks   string `json:"remarks"`
		Outbounds []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
			Settings struct {
				Vnext []struct {
					Address string `json:"address"`
					Port    int    `json:"port"`
					Users   []struct {
						ID   string `json:"id"`
						Flow string `json:"flow"`
					} `json:"users"`
				} `json:"vnext"`
			} `json:"settings"`
			StreamSettings struct {
				Network         string `json:"network"`
				Security        string `json:"security"`
				RealitySettings struct {
					ServerName string `json:"serverName"`
					PublicKey  string `json:"publicKey"`
				} `json:"realitySettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("shipped happ template did not produce a valid JSON array: %v\n%s", err, out)
	}
	if len(parsed) != 2 {
		t.Fatalf("expected one array element per client, got %d", len(parsed))
	}

	first := parsed[0]
	if want := "evil \" remark + alice"; first.Remarks != want {
		t.Errorf("expected remarks %q with quote intact, got %q", want, first.Remarks)
	}
	if len(first.Outbounds) < 1 || first.Outbounds[0].Protocol != "vless" {
		t.Fatalf("expected first outbound to be the real vless proxy outbound, got %+v", first.Outbounds)
	}
	proxy := first.Outbounds[0]
	if len(proxy.Settings.Vnext) != 1 || proxy.Settings.Vnext[0].Address != "vpn.example.com" || proxy.Settings.Vnext[0].Port != 443 {
		t.Errorf("expected valid xray-core vnext shape, got %+v", proxy.Settings)
	}
	if proxy.Settings.Vnext[0].Users[0].ID != "uuid-1" || proxy.Settings.Vnext[0].Users[0].Flow != "xtls-rprx-vision" {
		t.Errorf("expected uuid/flow to round-trip, got %+v", proxy.Settings.Vnext[0].Users[0])
	}
	if proxy.StreamSettings.RealitySettings.ServerName != "example.com" || proxy.StreamSettings.RealitySettings.PublicKey != "pk" {
		t.Errorf("expected realitySettings to round-trip, got %+v", proxy.StreamSettings.RealitySettings)
	}

	var hasDirect, hasBlock bool
	for _, ob := range first.Outbounds {
		if ob.Tag == "direct" {
			hasDirect = true
		}
		if ob.Tag == "block" {
			hasBlock = true
		}
	}
	if !hasDirect || !hasBlock {
		t.Errorf("expected direct+block fallback outbounds alongside the proxy, got %+v", first.Outbounds)
	}

	if len(first.Routing.Rules) != 1 || first.Routing.Rules[0]["geoip"] != "CN" || first.Routing.Rules[0]["outboundTag"] != "direct" {
		t.Errorf("expected the configured routing rule to render, got %+v", first.Routing.Rules)
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
