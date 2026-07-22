package xui

import (
	"encoding/json"
	"testing"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

func sampleVlessInbound() Inbound {
	return Inbound{
		ID:             1,
		Enable:         true,
		Remark:         "vless-reality",
		Listen:         "",
		Port:           443,
		Protocol:       "vless",
		Settings:       []byte(`{"clients":[{"id":"11111111-1111-1111-1111-111111111111","email":"alice","subId":"tok-abc","flow":"xtls-rprx-vision","enable":true,"totalGB":0,"expiryTime":0}]}`),
		StreamSettings: []byte(`{"network":"tcp","security":"reality","realitySettings":{"serverNames":["example.com"],"shortIds":["abcd1234"],"settings":{"publicKey":"pubkey123","fingerprint":"chrome"}}}`),
		ClientStats: []ClientStat{
			{Email: "alice", Up: 1000, Down: 2000, Total: 0, ExpiryTime: 0},
		},
	}
}

func TestMatchedClientsBySubID_SettingsAsDoubleEncodedString(t *testing.T) {
	ib := sampleVlessInbound()
	// Simulate a panel that double-encodes settings/streamSettings as a JSON
	// string (vanilla 3x-ui's convention) rather than a nested object.
	settingsStr, _ := json.Marshal(string(ib.Settings))
	streamStr, _ := json.Marshal(string(ib.StreamSettings))
	ib.Settings = settingsStr
	ib.StreamSettings = streamStr

	matches, err := MatchedClientsBySubID([]Inbound{ib}, nil, "tok-abc", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match even with double-encoded settings, got %d", len(matches))
	}
	if matches[0].Stream.TLS.PublicKey != "pubkey123" {
		t.Fatalf("expected stream settings decoded through double-encoding, got %+v", matches[0].Stream)
	}
}

func TestMatchedClientsBySubID_Match(t *testing.T) {
	inbounds := []Inbound{sampleVlessInbound()}

	matches, err := MatchedClientsBySubID(inbounds, nil, "tok-abc", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}

	m := matches[0]
	if m.Server != "1.2.3.4" {
		t.Errorf("expected fallback host substituted for wildcard listen, got %q", m.Server)
	}
	if m.Port != 443 {
		t.Errorf("expected port 443, got %d", m.Port)
	}
	if m.Client.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("unexpected client id: %q", m.Client.ID)
	}
	if m.Stream.TLS.PublicKey != "pubkey123" || m.Stream.TLS.ShortID != "abcd1234" || m.Stream.TLS.SNI != "example.com" {
		t.Errorf("reality settings not decoded correctly: %+v", m.Stream.TLS)
	}
	if m.Client.Up != 1000 || m.Client.Down != 2000 {
		t.Errorf("expected traffic merged from clientStats, got up=%d down=%d", m.Client.Up, m.Client.Down)
	}
}

func TestMatchedClientsBySubID_NoMatch(t *testing.T) {
	inbounds := []Inbound{sampleVlessInbound()}

	matches, err := MatchedClientsBySubID(inbounds, nil, "does-not-exist", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches, got %d", len(matches))
	}
}

func TestMatchedClientsBySubID_DisabledInboundSkipped(t *testing.T) {
	ib := sampleVlessInbound()
	ib.Enable = false

	matches, err := MatchedClientsBySubID([]Inbound{ib}, nil, "tok-abc", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected disabled inbound to be skipped, got %d matches", len(matches))
	}
}

func TestConnectHost_UsesRealListenWhenNotWildcard(t *testing.T) {
	ib := sampleVlessInbound()
	ib.Listen = "10.0.0.5"

	matches, err := MatchedClientsBySubID([]Inbound{ib}, nil, "tok-abc", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Server != "10.0.0.5" {
		t.Fatalf("expected explicit listen address to be used, got %+v", matches)
	}
}

func TestSelectHostForInbound(t *testing.T) {
	groups := []HostGroup{
		{GroupID: "disabled", InboundIDs: []int{1}, IsDisabled: true, SortOrder: 0},
		{GroupID: "hidden", InboundIDs: []int{1}, IsHidden: true, SortOrder: 0},
		{GroupID: "other-inbound", InboundIDs: []int{2}, SortOrder: 0},
		{GroupID: "later", InboundIDs: []int{1}, SortOrder: 5},
		{GroupID: "winner", InboundIDs: []int{1}, SortOrder: 1},
	}

	got, ok := selectHostForInbound(groups, 1)
	if !ok || got.GroupID != "winner" {
		t.Fatalf("expected the lowest-sortOrder enabled/visible group for inbound 1, got %+v (ok=%v)", got, ok)
	}

	if _, ok := selectHostForInbound(groups, 99); ok {
		t.Fatal("expected no host group for an inbound none of them list")
	}
}

func TestParseHostAddress(t *testing.T) {
	cases := []struct {
		entry        string
		fallbackPort int
		wantAddr     string
		wantPort     int
	}{
		{"cdn.example.com:8443", 443, "cdn.example.com", 8443},
		{"cdn.example.com", 443, "cdn.example.com", 443},
		{"203.0.113.5:443", 8080, "203.0.113.5", 443},
	}
	for _, c := range cases {
		addr, port := parseHostAddress(c.entry, c.fallbackPort)
		if addr != c.wantAddr || port != c.wantPort {
			t.Errorf("parseHostAddress(%q, %d) = (%q, %d), want (%q, %d)", c.entry, c.fallbackPort, addr, port, c.wantAddr, c.wantPort)
		}
	}
}

func TestMatchedClientsBySubID_HostOverridesConnectionFields(t *testing.T) {
	ib := sampleVlessInbound()
	ib.StreamSettings = []byte(`{"network":"ws","security":"tls","tlsSettings":{"serverName":"inbound-sni.example","alpn":["h2"],"settings":{"fingerprint":"chrome"}},"wsSettings":{"path":"/inbound-path","headers":{"Host":"inbound-host.example"}}}`)

	host := HostGroup{
		GroupID:       "g1",
		InboundIDs:    []int{ib.ID},
		Hosts:         []string{"cdn.example.com:8443"},
		Security:      "tls",
		SNI:           "cdn-sni.example",
		ALPN:          []string{"h2", "http/1.1"},
		Fingerprint:   "firefox",
		Path:          "/cdn-path",
		HostHeader:    "cdn-host.example",
		AllowInsecure: true,
	}

	matches, err := MatchedClientsBySubID([]Inbound{ib}, []HostGroup{host}, "tok-abc", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}

	m := matches[0]
	if m.Server != "cdn.example.com" || m.Port != 8443 {
		t.Errorf("expected Host address/port to override the inbound's own, got server=%q port=%d", m.Server, m.Port)
	}
	if m.Stream.TLS.SNI != "cdn-sni.example" {
		t.Errorf("expected Host SNI to override, got %q", m.Stream.TLS.SNI)
	}
	if len(m.Stream.TLS.ALPN) != 2 || m.Stream.TLS.ALPN[0] != "h2" || m.Stream.TLS.ALPN[1] != "http/1.1" {
		t.Errorf("expected Host ALPN to override, got %+v", m.Stream.TLS.ALPN)
	}
	if m.Stream.TLS.Fingerprint != "firefox" {
		t.Errorf("expected Host fingerprint to override, got %q", m.Stream.TLS.Fingerprint)
	}
	if m.Stream.Transport.Path != "/cdn-path" {
		t.Errorf("expected Host path to override, got %q", m.Stream.Transport.Path)
	}
	if m.Stream.Transport.Host != "cdn-host.example" {
		t.Errorf("expected Host hostHeader to override, got %q", m.Stream.Transport.Host)
	}
	if !m.Stream.TLS.Insecure {
		t.Error("expected Host allowInsecure to set Stream.TLS.Insecure")
	}
}

func TestMatchedClientsBySubID_HostSecuritySameLeavesSecurityAlone(t *testing.T) {
	ib := sampleVlessInbound() // security: reality

	host := HostGroup{
		GroupID:    "g1",
		InboundIDs: []int{ib.ID},
		Hosts:      []string{"cdn.example.com:443"},
		Security:   "same",
	}

	matches, err := MatchedClientsBySubID([]Inbound{ib}, []HostGroup{host}, "tok-abc", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}

	m := matches[0]
	if m.Server != "cdn.example.com" || m.Port != 443 {
		t.Errorf("expected address/port to still be overridden, got server=%q port=%d", m.Server, m.Port)
	}
	if m.Stream.Security != domain.SecurityReality {
		t.Errorf(`expected security "same" to leave the inbound's own security (reality) untouched, got %q`, m.Stream.Security)
	}
	// Reality's own crypto fields are never part of a Host and must survive
	// unconditionally.
	if m.Stream.TLS.PublicKey != "pubkey123" || m.Stream.TLS.ShortID != "abcd1234" || m.Stream.TLS.SNI != "example.com" {
		t.Errorf("expected reality crypto fields to survive a Host override untouched, got %+v", m.Stream.TLS)
	}
}

func TestMatchedClientsBySubID_NoHostAppliesLeavesInboundDerivedFieldsUnchanged(t *testing.T) {
	ib := sampleVlessInbound()
	// A host exists, but for a different inbound.
	host := HostGroup{GroupID: "g1", InboundIDs: []int{999}, Hosts: []string{"cdn.example.com:443"}}

	matches, err := MatchedClientsBySubID([]Inbound{ib}, []HostGroup{host}, "tok-abc", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Server != "1.2.3.4" || matches[0].Port != 443 {
		t.Fatalf("expected inbound-derived connection info when no Host applies, got %+v", matches)
	}
}
