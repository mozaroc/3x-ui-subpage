package yamlgen

import (
	"database/sql"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"gopkg.in/yaml.v3"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

// baseTmpl is a plain, static admin template with no template programming:
// a single auto-populated select group plus one manual "DIRECT" entry.
// No "proxies" key — it's generated and injected.
const baseTmpl = `mixed-port: 7890
proxy-groups:
  - name: PROXY
    type: select
    proxies:
      - DIRECT
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

func buildAndParse(t *testing.T, tmpl string, clients []domain.MatchedClient) map[string]any {
	t.Helper()
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", tmpl)
	g := New(db, "clash")

	out, err := g.Build(clients, "default")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, out)
	}
	return parsed
}

func proxyByName(t *testing.T, parsed map[string]any, name string) map[string]any {
	t.Helper()
	proxies, _ := parsed["proxies"].([]any)
	for _, p := range proxies {
		m, ok := p.(map[string]any)
		if ok && m["name"] == name {
			return m
		}
	}
	t.Fatalf("proxy %q not found in %v", name, parsed["proxies"])
	return nil
}

func groupProxies(t *testing.T, parsed map[string]any, groupName string) []string {
	t.Helper()
	groups, _ := parsed["proxy-groups"].([]any)
	for _, g := range groups {
		m, ok := g.(map[string]any)
		if !ok || m["name"] != groupName {
			continue
		}
		items, _ := m["proxies"].([]any)
		out := make([]string, len(items))
		for i, it := range items {
			out[i] = it.(string)
		}
		return out
	}
	t.Fatalf("group %q not found", groupName)
	return nil
}

func vlessRealityClient() domain.MatchedClient {
	return domain.MatchedClient{
		Protocol: domain.ProtocolVLESS,
		Remark:   "vless-reality",
		Server:   "vpn.example.com",
		Port:     443,
		Client:   domain.ClientAccount{ID: "uuid-1", Flow: "xtls-rprx-vision"},
		Stream: domain.StreamSettings{
			Network:  domain.NetworkTCP,
			Security: domain.SecurityReality,
			TLS:      domain.TLSSettings{SNI: "example.com", Fingerprint: "chrome", PublicKey: "pub", ShortID: "sid"},
		},
	}
}

func vlessTLSWSClient() domain.MatchedClient {
	return domain.MatchedClient{
		Protocol: domain.ProtocolVLESS,
		Remark:   "vless-tls-ws",
		Server:   "vpn.example.com",
		Port:     8443,
		Client:   domain.ClientAccount{ID: "uuid-2"},
		Stream: domain.StreamSettings{
			Network:   domain.NetworkWS,
			Security:  domain.SecurityTLS,
			TLS:       domain.TLSSettings{SNI: "ws.example.com", Fingerprint: "chrome", Insecure: true},
			Transport: domain.TransportSettings{Path: "/ws", Host: "ws.example.com"},
		},
	}
}

func vmessGRPCClient() domain.MatchedClient {
	return domain.MatchedClient{
		Protocol: domain.ProtocolVMess,
		Remark:   "vmess-grpc",
		Server:   "vpn.example.com",
		Port:     2053,
		Client:   domain.ClientAccount{ID: "uuid-3"},
		Stream: domain.StreamSettings{
			Network:   domain.NetworkGRPC,
			Security:  domain.SecurityTLS,
			TLS:       domain.TLSSettings{SNI: "grpc.example.com", Fingerprint: "chrome"},
			Transport: domain.TransportSettings{ServiceName: "grpc-svc"},
		},
	}
}

func trojanClient() domain.MatchedClient {
	return domain.MatchedClient{
		Protocol: domain.ProtocolTrojan,
		Remark:   "trojan-node",
		Server:   "vpn.example.com",
		Port:     443,
		Client:   domain.ClientAccount{Password: "secret-pass"},
		Stream: domain.StreamSettings{
			Network:  domain.NetworkTCP,
			Security: domain.SecurityTLS,
			TLS:      domain.TLSSettings{SNI: "trojan.example.com", Insecure: true},
		},
	}
}

func shadowsocksClient() domain.MatchedClient {
	return domain.MatchedClient{
		Protocol: domain.ProtocolShadowsocks,
		Remark:   "ss-node",
		Server:   "vpn.example.com",
		Port:     8388,
		Client:   domain.ClientAccount{Method: "aes-256-gcm", Password: "sspw"},
		Stream:   domain.StreamSettings{Network: domain.NetworkTCP, Security: domain.SecurityNone},
	}
}

func TestBuildProxies_VLESSReality(t *testing.T) {
	parsed := buildAndParse(t, baseTmpl, []domain.MatchedClient{vlessRealityClient()})
	p := proxyByName(t, parsed, "vless-reality")

	if p["type"] != "vless" || p["server"] != "vpn.example.com" || p["port"] != 443 {
		t.Fatalf("unexpected base fields: %v", p)
	}
	if p["flow"] != "xtls-rprx-vision" {
		t.Errorf("expected flow, got %v", p["flow"])
	}
	realityOpts, ok := p["reality-opts"].(map[string]any)
	if !ok {
		t.Fatalf("expected reality-opts, got %v", p)
	}
	if realityOpts["public-key"] != "pub" || realityOpts["short-id"] != "sid" {
		t.Errorf("unexpected reality-opts: %v", realityOpts)
	}
	if p["servername"] != "example.com" || p["client-fingerprint"] != "chrome" {
		t.Errorf("unexpected sni/fingerprint: %v", p)
	}
	if _, ok := p["skip-cert-verify"]; ok {
		t.Errorf("did not expect skip-cert-verify: %v", p)
	}
}

func TestBuildProxies_VLESSTLSWSInsecure(t *testing.T) {
	parsed := buildAndParse(t, baseTmpl, []domain.MatchedClient{vlessTLSWSClient()})
	p := proxyByName(t, parsed, "vless-tls-ws")

	if p["tls"] != true || p["skip-cert-verify"] != true {
		t.Errorf("expected tls+skip-cert-verify: %v", p)
	}
	wsOpts, ok := p["ws-opts"].(map[string]any)
	if !ok {
		t.Fatalf("expected ws-opts, got %v", p)
	}
	if wsOpts["path"] != "/ws" {
		t.Errorf("unexpected ws-opts: %v", wsOpts)
	}
	headers, ok := wsOpts["headers"].(map[string]any)
	if !ok || headers["Host"] != "ws.example.com" {
		t.Errorf("unexpected ws headers: %v", wsOpts)
	}
}

func TestBuildProxies_VMessGRPC(t *testing.T) {
	parsed := buildAndParse(t, baseTmpl, []domain.MatchedClient{vmessGRPCClient()})
	p := proxyByName(t, parsed, "vmess-grpc")

	if p["type"] != "vmess" || p["alterId"] != 0 || p["cipher"] != "auto" {
		t.Errorf("unexpected vmess base fields: %v", p)
	}
	grpcOpts, ok := p["grpc-opts"].(map[string]any)
	if !ok || grpcOpts["grpc-service-name"] != "grpc-svc" {
		t.Errorf("unexpected grpc-opts: %v", p)
	}
}

func TestBuildProxies_Trojan(t *testing.T) {
	parsed := buildAndParse(t, baseTmpl, []domain.MatchedClient{trojanClient()})
	p := proxyByName(t, parsed, "trojan-node")

	if p["password"] != "secret-pass" || p["sni"] != "trojan.example.com" || p["skip-cert-verify"] != true {
		t.Errorf("unexpected trojan fields: %v", p)
	}
}

func TestBuildProxies_Shadowsocks(t *testing.T) {
	parsed := buildAndParse(t, baseTmpl, []domain.MatchedClient{shadowsocksClient()})
	p := proxyByName(t, parsed, "ss-node")

	if p["type"] != "ss" || p["cipher"] != "aes-256-gcm" || p["password"] != "sspw" {
		t.Errorf("unexpected shadowsocks fields: %v", p)
	}
}

func TestGroups_DefaultAutoPopulate(t *testing.T) {
	clients := []domain.MatchedClient{vlessRealityClient(), trojanClient()}
	parsed := buildAndParse(t, baseTmpl, clients)

	got := groupProxies(t, parsed, "PROXY")
	want := []string{"DIRECT", "vless-reality", "trojan-node"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("expected %v, got %v", want, got)
			break
		}
	}
}

func TestGroups_IncludeProxiesFalse(t *testing.T) {
	tmpl := `proxy-groups:
  - name: MANUAL
    type: select
    remnawave:
      include-proxies: false
    proxies:
      - DIRECT
`
	clients := []domain.MatchedClient{vlessRealityClient()}
	parsed := buildAndParse(t, tmpl, clients)

	got := groupProxies(t, parsed, "MANUAL")
	if len(got) != 1 || got[0] != "DIRECT" {
		t.Errorf("expected group untouched ([DIRECT]), got %v", got)
	}
	// remnawave: block itself must not leak into rendered output.
	groups, _ := parsed["proxy-groups"].([]any)
	m := groups[0].(map[string]any)
	if _, ok := m["remnawave"]; ok {
		t.Errorf("remnawave key should have been stripped: %v", m)
	}
}

func TestGroups_SelectRandomProxy(t *testing.T) {
	tmpl := `proxy-groups:
  - name: RANDOM
    type: select
    remnawave:
      select-random-proxy: true
    proxies:
      - DIRECT
`
	clients := []domain.MatchedClient{vlessRealityClient(), trojanClient(), shadowsocksClient()}
	parsed := buildAndParse(t, tmpl, clients)

	got := groupProxies(t, parsed, "RANDOM")
	if len(got) != 2 {
		t.Fatalf("expected DIRECT + exactly one random proxy, got %v", got)
	}
	if got[0] != "DIRECT" {
		t.Errorf("expected manual entry preserved first, got %v", got)
	}
	valid := map[string]bool{"vless-reality": true, "trojan-node": true, "ss-node": true}
	if !valid[got[1]] {
		t.Errorf("random pick %q is not one of the client names", got[1])
	}
}

func TestGroups_ShuffleProxiesOrder(t *testing.T) {
	tmpl := `proxy-groups:
  - name: SHUFFLED
    type: fallback
    remnawave:
      shuffle-proxies-order: true
    proxies:
      - DIRECT
`
	clients := []domain.MatchedClient{vlessRealityClient(), trojanClient(), shadowsocksClient()}
	parsed := buildAndParse(t, tmpl, clients)

	got := groupProxies(t, parsed, "SHUFFLED")
	want := []string{"DIRECT", "vless-reality", "trojan-node", "ss-node"}
	sortedGot := append([]string(nil), got...)
	sortedWant := append([]string(nil), want...)
	sort.Strings(sortedGot)
	sort.Strings(sortedWant)
	if len(sortedGot) != len(sortedWant) {
		t.Fatalf("expected same members as %v, got %v", want, got)
	}
	for i := range sortedWant {
		if sortedGot[i] != sortedWant[i] {
			t.Fatalf("expected same members as %v, got %v", want, got)
		}
	}
}

func TestGroups_FilterAndExcludeFilter(t *testing.T) {
	tmpl := `proxy-groups:
  - name: FILTERED
    type: select
    filter: 'vless|trojan'
    exclude-filter: 'trojan'
    proxies: []
`
	clients := []domain.MatchedClient{vlessRealityClient(), trojanClient(), shadowsocksClient()}
	parsed := buildAndParse(t, tmpl, clients)

	got := groupProxies(t, parsed, "FILTERED")
	if len(got) != 1 || got[0] != "vless-reality" {
		t.Errorf("expected only vless-reality to survive filter+exclude-filter, got %v", got)
	}
}

func TestBuild_MissingProxyGroups(t *testing.T) {
	tmpl := "mixed-port: 7890\n"
	parsed := buildAndParse(t, tmpl, []domain.MatchedClient{vlessRealityClient()})

	proxies, _ := parsed["proxies"].([]any)
	if len(proxies) != 1 {
		t.Fatalf("expected proxies to still be injected, got %v", parsed)
	}
}

func TestBuild_ProxiesKeyCreatedWhenAbsent(t *testing.T) {
	parsed := buildAndParse(t, baseTmpl, []domain.MatchedClient{vlessRealityClient()})
	if _, ok := parsed["proxies"]; !ok {
		t.Fatalf("expected proxies key to be created, got %v", parsed)
	}
}

func TestBuild_ProxyProvidersRemnawaveUnsupported(t *testing.T) {
	tmpl := `proxy-providers:
  chain1:
    type: inline
    remnawave:
      include-proxies: true
    payload: []
`
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", tmpl)
	g := New(db, "clash")

	if _, err := g.Build(nil, "default"); err == nil {
		t.Fatal("expected error for unsupported proxy-providers remnawave block")
	}
}

func TestBuild_DifferentFormatsAreIndependent(t *testing.T) {
	db := openTestDB(t)
	insertTemplate(t, db, "clash", "default", baseTmpl)
	insertTemplate(t, db, "mihomo", "default", "tun:\n  enable: true\n")

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
	insertTemplate(t, db, "clash", "default", "mixed-port: 1\n")
	insertTemplate(t, db, "clash", "gaming", "mixed-port: 1\nextra: gaming-marker\n")
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
	insertTemplate(t, db, "clash", "default", "not: [valid")
	g := New(db, "clash")

	if _, err := g.Build(nil, "default"); err == nil {
		t.Fatal("expected error for invalid YAML template")
	}
}

func TestBuild_EmptyClients(t *testing.T) {
	parsed := buildAndParse(t, baseTmpl, nil)

	proxies, ok := parsed["proxies"].([]any)
	if !ok || len(proxies) != 0 {
		t.Fatalf("expected empty proxies list, got %v", parsed["proxies"])
	}
	if got := groupProxies(t, parsed, "PROXY"); len(got) != 1 || got[0] != "DIRECT" {
		t.Errorf("expected group to keep only manual entries, got %v", got)
	}
}
