package linkgen

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

// readShippedTemplate loads this project's own default xray_link template
// for protocol (web/templates/xray/<protocol>.tmpl) — used to exercise the
// actual content administrators get out of the box, not just this test's
// synthetic fixtures above.
func readShippedTemplate(t *testing.T, protocol string) string {
	t.Helper()
	content, err := os.ReadFile("../../../web/templates/xray/" + protocol + ".tmpl")
	if err != nil {
		t.Fatalf("read shipped %s template: %v", protocol, err)
	}
	return string(content)
}

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

const vlessTmpl = `vless://{{.UUID}}@{{.Server}}:{{.Port}}?encryption=none&security={{.Security}}&type={{.Network}}{{if .Flow}}&flow={{urlquery .Flow}}{{end}}{{if eq .Security "reality"}}&pbk={{urlquery .PublicKey}}&sid={{.ShortID}}&sni={{urlquery .SNI}}&fp={{urlquery .Fingerprint}}{{end}}#{{urlquery .Remark}}`

const vmessTmpl = `{"v":"2","ps":"{{.Remark}}","add":"{{.Server}}","port":"{{.Port}}","id":"{{.UUID}}","aid":"0","net":"{{.Network}}","type":"{{.HeaderType}}","host":"{{.Host}}","path":"{{.Path}}","tls":"{{.Security}}"}`

const trojanTmpl = `trojan://{{urlquery .Password}}@{{.Server}}:{{.Port}}?security={{.Security}}&type={{.Network}}#{{urlquery .Remark}}`

const shadowsocksTmpl = `ss://{{b64 (print .Method ":" .Password)}}@{{.Server}}:{{.Port}}#{{urlquery .Remark}}`

func seedDefaultTemplates(t *testing.T, db *sql.DB) {
	t.Helper()
	insert(t, db, "default", "vless", vlessTmpl)
	insert(t, db, "default", "vmess", vmessTmpl)
	insert(t, db, "default", "trojan", trojanTmpl)
	insert(t, db, "default", "shadowsocks", shadowsocksTmpl)
}

func insert(t *testing.T, db *sql.DB, profile, protocol, content string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO templates (format, profile, protocol, content, updated_at) VALUES ('xray_link', ?, ?, ?, 1)
		ON CONFLICT(format, profile, protocol) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`,
		profile, protocol, content)
	if err != nil {
		t.Fatalf("insert template: %v", err)
	}
}

func vlessClient() domain.MatchedClient {
	return domain.MatchedClient{
		Protocol: domain.ProtocolVLESS,
		Server:   "vpn.example.com",
		Port:     443,
		Client: domain.ClientAccount{
			ID:    "11111111-1111-1111-1111-111111111111",
			Email: "alice",
			Flow:  "xtls-rprx-vision",
		},
		Stream: domain.StreamSettings{
			Network:  domain.NetworkTCP,
			Security: domain.SecurityReality,
			TLS: domain.TLSSettings{
				SNI:         "example.com",
				Fingerprint: "chrome",
				PublicKey:   "pubkey123",
				ShortID:     "abcd1234",
			},
		},
	}
}

func TestBuildLink_VLESS(t *testing.T) {
	db := openTestDB(t)
	seedDefaultTemplates(t, db)
	g := New(db)

	link, err := g.BuildLink(vlessClient(), "default")
	if err != nil {
		t.Fatalf("BuildLink: %v", err)
	}
	if !strings.HasPrefix(link, "vless://11111111-1111-1111-1111-111111111111@vpn.example.com:443?") {
		t.Fatalf("unexpected link prefix: %s", link)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link as URL: %v", err)
	}
	q := u.Query()
	if q.Get("security") != "reality" || q.Get("pbk") != "pubkey123" || q.Get("sid") != "abcd1234" {
		t.Errorf("reality params missing/wrong: %v", q)
	}
	if u.Fragment != "alice" {
		t.Errorf("expected remark fragment 'alice', got %q", u.Fragment)
	}
}

func TestBuildLink_ProfileFallbackToDefault(t *testing.T) {
	db := openTestDB(t)
	seedDefaultTemplates(t, db)
	g := New(db)

	link, err := g.BuildLink(vlessClient(), "nonexistent-profile")
	if err != nil {
		t.Fatalf("BuildLink with unknown profile should fall back to default: %v", err)
	}
	if !strings.HasPrefix(link, "vless://") {
		t.Fatalf("unexpected link: %s", link)
	}
}

func TestBuildLink_ProfileOverride(t *testing.T) {
	db := openTestDB(t)
	seedDefaultTemplates(t, db)
	insert(t, db, "gaming", "vless", `vless-gaming://{{.UUID}}@{{.Server}}:{{.Port}}`)
	g := New(db)

	link, err := g.BuildLink(vlessClient(), "gaming")
	if err != nil {
		t.Fatalf("BuildLink: %v", err)
	}
	if !strings.HasPrefix(link, "vless-gaming://") {
		t.Fatalf("expected gaming profile template to be used, got: %s", link)
	}
}

func TestBuildLink_VMess(t *testing.T) {
	db := openTestDB(t)
	seedDefaultTemplates(t, db)
	g := New(db)

	mc := domain.MatchedClient{
		Protocol: domain.ProtocolVMess,
		Server:   "vpn.example.com",
		Port:     8443,
		Client:   domain.ClientAccount{ID: "22222222-2222-2222-2222-222222222222", Email: "bob"},
		Stream: domain.StreamSettings{
			Network:  domain.NetworkWS,
			Security: domain.SecurityTLS,
			TLS:      domain.TLSSettings{SNI: "example.com"},
			Transport: domain.TransportSettings{
				Path: "/ws",
				Host: "example.com",
			},
		},
	}

	link, err := g.BuildLink(mc, "default")
	if err != nil {
		t.Fatalf("BuildLink: %v", err)
	}
	if !strings.HasPrefix(link, "vmess://") {
		t.Fatalf("expected vmess:// prefix, got %s", link)
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}

	var obj map[string]any
	if err := json.Unmarshal(decoded, &obj); err != nil {
		t.Fatalf("decoded payload is not valid JSON: %v\n%s", err, decoded)
	}
	if obj["id"] != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("unexpected id in vmess JSON: %v", obj["id"])
	}
	if obj["net"] != "ws" || obj["path"] != "/ws" {
		t.Errorf("unexpected transport fields: %v", obj)
	}
}

func TestBuildLink_ShippedVMessTemplate_EscapesQuoteInRemark(t *testing.T) {
	db := openTestDB(t)
	insert(t, db, "default", "vmess", readShippedTemplate(t, "vmess"))
	g := New(db)

	mc := domain.MatchedClient{
		Remark:   `evil " remark`,
		Protocol: domain.ProtocolVMess,
		Server:   "vpn.example.com",
		Port:     8443,
		Client:   domain.ClientAccount{ID: "22222222-2222-2222-2222-222222222222", Email: "bob"},
	}

	link, err := g.BuildLink(mc, "default")
	if err != nil {
		t.Fatalf("BuildLink: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if !json.Valid(decoded) {
		t.Fatalf("decoded vmess payload is not valid JSON: %s", decoded)
	}

	var obj map[string]any
	if err := json.Unmarshal(decoded, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := "evil \" remark + bob"; obj["ps"] != want {
		t.Errorf("expected ps %q with quote intact, got %v", want, obj["ps"])
	}
}

func TestBuildLink_ShippedVMessTemplate_MalformedOutputIsRejected(t *testing.T) {
	// A broken admin-edited template must surface an error, not silently
	// wrap invalid JSON into a base64 "share link" no client can use.
	db := openTestDB(t)
	insert(t, db, "default", "vmess", `{"ps": not valid json}`)
	g := New(db)

	mc := domain.MatchedClient{
		Protocol: domain.ProtocolVMess,
		Server:   "vpn.example.com",
		Port:     8443,
		Client:   domain.ClientAccount{ID: "uuid-1", Email: "bob"},
	}

	if _, err := g.BuildLink(mc, "default"); err == nil {
		t.Fatal("expected an error for a vmess template that renders invalid JSON")
	}
}

func TestBuildLink_ShippedVLESSTemplate_ShortIDIsURLEscaped(t *testing.T) {
	db := openTestDB(t)
	insert(t, db, "default", "vless", readShippedTemplate(t, "vless"))
	g := New(db)

	mc := domain.MatchedClient{
		Protocol: domain.ProtocolVLESS,
		Server:   "vpn.example.com",
		Port:     443,
		Client:   domain.ClientAccount{ID: "11111111-1111-1111-1111-111111111111", Email: "alice"},
		Stream: domain.StreamSettings{
			Security: domain.SecurityReality,
			TLS:      domain.TLSSettings{ShortID: "ab&cd", SNI: "example.com", Fingerprint: "chrome", PublicKey: "pk"},
		},
	}

	link, err := g.BuildLink(mc, "default")
	if err != nil {
		t.Fatalf("BuildLink: %v", err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link as URL: %v", err)
	}
	if got := u.Query().Get("sid"); got != "ab&cd" {
		t.Errorf("expected sid to round-trip as a single query param value %q, got %q", "ab&cd", got)
	}
}

func TestBuildLink_Trojan(t *testing.T) {
	db := openTestDB(t)
	seedDefaultTemplates(t, db)
	g := New(db)

	mc := domain.MatchedClient{
		Protocol: domain.ProtocolTrojan,
		Server:   "vpn.example.com",
		Port:     443,
		Client:   domain.ClientAccount{Password: "s3cr3t", Email: "carl"},
		Stream: domain.StreamSettings{
			Network:  domain.NetworkTCP,
			Security: domain.SecurityTLS,
			TLS:      domain.TLSSettings{SNI: "example.com", Fingerprint: "chrome"},
		},
	}
	link, err := g.BuildLink(mc, "default")
	if err != nil {
		t.Fatalf("BuildLink: %v", err)
	}
	if !strings.HasPrefix(link, "trojan://s3cr3t@vpn.example.com:443?") {
		t.Fatalf("unexpected trojan link: %s", link)
	}
}

func TestBuildLink_Shadowsocks(t *testing.T) {
	db := openTestDB(t)
	seedDefaultTemplates(t, db)
	g := New(db)

	mc := domain.MatchedClient{
		Protocol: domain.ProtocolShadowsocks,
		Server:   "vpn.example.com",
		Port:     8388,
		Client:   domain.ClientAccount{Method: "aes-256-gcm", Password: "pw123", Email: "dana"},
	}
	link, err := g.BuildLink(mc, "default")
	if err != nil {
		t.Fatalf("BuildLink: %v", err)
	}
	if !strings.HasPrefix(link, "ss://") {
		t.Fatalf("unexpected ss link: %s", link)
	}
	afterScheme := strings.TrimPrefix(link, "ss://")
	userinfo := strings.SplitN(afterScheme, "@", 2)[0]
	decoded, err := base64.StdEncoding.DecodeString(userinfo)
	if err != nil {
		t.Fatalf("decode ss userinfo: %v", err)
	}
	if string(decoded) != "aes-256-gcm:pw123" {
		t.Errorf("expected 'aes-256-gcm:pw123', got %q", decoded)
	}
}

func TestBuildSubscription_Base64JoinsAllLinks(t *testing.T) {
	db := openTestDB(t)
	seedDefaultTemplates(t, db)
	g := New(db)

	clients := []domain.MatchedClient{vlessClient()}

	body, err := g.BuildSubscription(clients, "default")
	if err != nil {
		t.Fatalf("BuildSubscription: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("decode subscription body: %v", err)
	}
	if !strings.HasPrefix(string(decoded), "vless://") {
		t.Fatalf("unexpected decoded body: %s", decoded)
	}
}

func TestBuildLink_UnsupportedProtocol(t *testing.T) {
	db := openTestDB(t)
	g := New(db)
	_, err := g.BuildLink(domain.MatchedClient{Protocol: "http"}, "default")
	if err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
}
