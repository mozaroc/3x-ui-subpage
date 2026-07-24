package tmplctx

import (
	"encoding/base64"
	"testing"
)

// The following two links are the user's own pasted, panel-generated (3x-ui
// canonical) examples that exposed this project's self-built links as
// broken — one ws, one xhttp. Parsing them correctly, verbatim, is the
// concrete regression test proving the underlying bug is fixed.
const realPanelWSLink = `vless://70e04486-40d2-48b0-8ef2-467f9f79c1f3@13.140.19.4.cdn-one.org:443?alpn=h2%2Chttp%2F1.1&fp=firefox&host=13.140.19.4.cdn-one.org&path=%2F20933%2FWSgiGL8j2R&security=tls&type=ws#%F0%9F%87%B1%F0%9F%87%BB%20ws-teatdsdfas`

const realPanelXHTTPLink = `vless://70e04486-40d2-48b0-8ef2-467f9f79c1f3@13.140.19.4.cdn-one.org:443?alpn=h2%2Chttp%2F1.1&extra=%7B%22mode%22%3A%22packet-up%22%2C%22xPaddingBytes%22%3A%22100-1000%22%7D&fp=firefox&host=13.140.19.4.cdn-one.org&mode=packet-up&path=%2F0AfJZzQOK4&security=tls&type=xhttp&x_padding_bytes=100-1000#%F0%9F%87%B1%F0%9F%87%BB%20xhttp-teatdsdfas`

func TestParseShareLink_RealPanelWSLink(t *testing.T) {
	cc, err := ParseShareLink(realPanelWSLink)
	if err != nil {
		t.Fatalf("ParseShareLink: %v", err)
	}
	if cc.Protocol != "vless" {
		t.Errorf("expected vless, got %q", cc.Protocol)
	}
	if cc.UUID != "70e04486-40d2-48b0-8ef2-467f9f79c1f3" {
		t.Errorf("unexpected uuid: %q", cc.UUID)
	}
	if cc.Server != "13.140.19.4.cdn-one.org" || cc.Port != 443 {
		t.Errorf("unexpected server/port: %q:%d", cc.Server, cc.Port)
	}
	if cc.Network != "ws" {
		t.Errorf("expected network ws, got %q", cc.Network)
	}
	if cc.Security != "tls" {
		t.Errorf("expected security tls, got %q", cc.Security)
	}
	if cc.Host != "13.140.19.4.cdn-one.org" {
		t.Errorf("expected host param preserved (this exact field was missing in our old self-built link), got %q", cc.Host)
	}
	if cc.Path != "/20933/WSgiGL8j2R" {
		t.Errorf("unexpected path: %q", cc.Path)
	}
	if cc.Fingerprint != "firefox" {
		t.Errorf("unexpected fingerprint: %q", cc.Fingerprint)
	}
	if cc.ALPN != "h2,http/1.1" {
		t.Errorf("unexpected alpn: %q", cc.ALPN)
	}
	if cc.Remark != "\U0001F1F1\U0001F1FB ws-teatdsdfas" {
		t.Errorf("unexpected remark: %q", cc.Remark)
	}
	if cc.RawParams["host"] != "13.140.19.4.cdn-one.org" {
		t.Errorf("expected RawParams to also carry host, got %+v", cc.RawParams)
	}
}

func TestParseShareLink_RealPanelXHTTPLink(t *testing.T) {
	cc, err := ParseShareLink(realPanelXHTTPLink)
	if err != nil {
		t.Fatalf("ParseShareLink: %v", err)
	}
	if cc.Network != "xhttp" {
		t.Errorf("expected network xhttp, got %q", cc.Network)
	}
	if cc.Host != "13.140.19.4.cdn-one.org" {
		t.Errorf("unexpected host: %q", cc.Host)
	}
	if cc.Path != "/0AfJZzQOK4" {
		t.Errorf("unexpected path: %q", cc.Path)
	}
	// The whole point: xhttp's mode/extra/x_padding_bytes have no named
	// ClientContext field, but must still survive via RawParams so
	// xray-json generation (and anything else that cares) can use them --
	// this is exactly what our old template-based approach couldn't do.
	if cc.RawParams["mode"] != "packet-up" {
		t.Errorf("expected RawParams[mode]=packet-up, got %+v", cc.RawParams)
	}
	if cc.RawParams["x_padding_bytes"] != "100-1000" {
		t.Errorf("expected RawParams[x_padding_bytes]=100-1000, got %+v", cc.RawParams)
	}
	wantExtra := `{"mode":"packet-up","xPaddingBytes":"100-1000"}`
	if cc.RawParams["extra"] != wantExtra {
		t.Errorf("expected RawParams[extra]=%s, got %q", wantExtra, cc.RawParams["extra"])
	}
	if cc.Remark != "\U0001F1F1\U0001F1FB xhttp-teatdsdfas" {
		t.Errorf("unexpected remark: %q", cc.Remark)
	}
}

func TestParseShareLink_VlessReality(t *testing.T) {
	link := "vless://uuid-1@vpn.example.com:443?security=reality&type=grpc&flow=xtls-rprx-vision&pbk=pubkey123&sid=abcd&sni=example.com&fp=chrome&spx=%2F&serviceName=grpcsvc#My%20Server"
	cc, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("ParseShareLink: %v", err)
	}
	if cc.Security != "reality" || cc.Network != "grpc" {
		t.Fatalf("unexpected security/network: %q/%q", cc.Security, cc.Network)
	}
	if cc.Flow != "xtls-rprx-vision" {
		t.Errorf("unexpected flow: %q", cc.Flow)
	}
	if cc.PublicKey != "pubkey123" || cc.ShortID != "abcd" || cc.SpiderX != "/" {
		t.Errorf("unexpected reality fields: pbk=%q sid=%q spx=%q", cc.PublicKey, cc.ShortID, cc.SpiderX)
	}
	if cc.ServiceName != "grpcsvc" {
		t.Errorf("unexpected serviceName: %q", cc.ServiceName)
	}
	if cc.Remark != "My Server" {
		t.Errorf("unexpected remark: %q", cc.Remark)
	}
}

func TestParseShareLink_Trojan(t *testing.T) {
	link := "trojan://s3cr3t@vpn.example.com:443?security=tls&sni=example.com&fp=chrome#trojan-node"
	cc, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("ParseShareLink: %v", err)
	}
	if cc.Protocol != "trojan" || cc.Password != "s3cr3t" {
		t.Fatalf("unexpected protocol/password: %q/%q", cc.Protocol, cc.Password)
	}
	if cc.SNI != "example.com" {
		t.Errorf("unexpected sni: %q", cc.SNI)
	}
}

func TestParseShareLink_VMess(t *testing.T) {
	body := `{"v":"2","ps":"vmess-node","add":"vpn.example.com","port":"443","id":"uuid-vmess","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"example.com","alpn":"h2,http/1.1","fp":"chrome"}`
	link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(body))

	cc, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("ParseShareLink: %v", err)
	}
	if cc.Protocol != "vmess" || cc.UUID != "uuid-vmess" {
		t.Fatalf("unexpected protocol/uuid: %q/%q", cc.Protocol, cc.UUID)
	}
	if cc.Server != "vpn.example.com" || cc.Port != 443 {
		t.Errorf("unexpected server/port: %q:%d", cc.Server, cc.Port)
	}
	if cc.Network != "ws" || cc.Security != "tls" {
		t.Errorf("unexpected network/security: %q/%q", cc.Network, cc.Security)
	}
	if cc.Host != "cdn.example.com" || cc.Path != "/ws" {
		t.Errorf("unexpected host/path: %q/%q", cc.Host, cc.Path)
	}
	if cc.Remark != "vmess-node" {
		t.Errorf("unexpected remark: %q", cc.Remark)
	}
	if cc.RawParams["scy"] != "auto" {
		t.Errorf("expected RawParams passthrough for untyped fields, got %+v", cc.RawParams)
	}
}

func TestParseShareLink_ShadowsocksSIP002(t *testing.T) {
	userinfo := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:s3cr3t"))
	link := "ss://" + userinfo + "@vpn.example.com:8388#ss-node"

	cc, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("ParseShareLink: %v", err)
	}
	if cc.Protocol != "shadowsocks" || cc.Method != "chacha20-ietf-poly1305" || cc.Password != "s3cr3t" {
		t.Fatalf("unexpected fields: %+v", cc)
	}
	if cc.Server != "vpn.example.com" || cc.Port != 8388 {
		t.Errorf("unexpected server/port: %q:%d", cc.Server, cc.Port)
	}
	if cc.Remark != "ss-node" {
		t.Errorf("unexpected remark: %q", cc.Remark)
	}
}

func TestParseShareLink_ShadowsocksLegacyFullyEncoded(t *testing.T) {
	full := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:s3cr3t@vpn.example.com:8388"))
	link := "ss://" + full + "#legacy-node"

	cc, err := ParseShareLink(link)
	if err != nil {
		t.Fatalf("ParseShareLink: %v", err)
	}
	if cc.Method != "aes-256-gcm" || cc.Password != "s3cr3t" {
		t.Fatalf("unexpected method/password: %q/%q", cc.Method, cc.Password)
	}
	if cc.Server != "vpn.example.com" || cc.Port != 8388 {
		t.Errorf("unexpected server/port: %q:%d", cc.Server, cc.Port)
	}
}

func TestParseShareLink_UnknownSchemeErrors(t *testing.T) {
	if _, err := ParseShareLink("hysteria2://whatever@host:443"); err == nil {
		t.Fatal("expected an error for an unsupported scheme")
	}
}

func TestParseShareLink_MalformedLinkErrors(t *testing.T) {
	if _, err := ParseShareLink("not-a-link-at-all"); err == nil {
		t.Fatal("expected an error for a string with no scheme")
	}
}

func TestParseEntries_NeverDropsAnEntryEvenOnParseFailure(t *testing.T) {
	links := []string{realPanelWSLink, "totally-broken", realPanelXHTTPLink}
	entries := ParseEntries(links)

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (one per input link, regardless of parse success), got %d", len(entries))
	}
	if entries[0].ParseErr != nil || entries[2].ParseErr != nil {
		t.Fatalf("expected the two valid links to parse cleanly, got errs: %v / %v", entries[0].ParseErr, entries[2].ParseErr)
	}
	if entries[1].ParseErr == nil {
		t.Fatal("expected the malformed link to report a parse error")
	}
	if entries[1].Raw != "totally-broken" {
		t.Errorf("expected Raw to still be preserved for the unparseable entry, got %q", entries[1].Raw)
	}
}
