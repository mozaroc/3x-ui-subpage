package xui

import "testing"

func sampleVlessInbound() Inbound {
	return Inbound{
		ID:             1,
		Enable:         true,
		Remark:         "vless-reality",
		Listen:         "",
		Port:           443,
		Protocol:       "vless",
		Settings:       `{"clients":[{"id":"11111111-1111-1111-1111-111111111111","email":"alice","subId":"tok-abc","flow":"xtls-rprx-vision","enable":true,"totalGB":0,"expiryTime":0}]}`,
		StreamSettings: `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["example.com"],"shortIds":["abcd1234"],"settings":{"publicKey":"pubkey123","fingerprint":"chrome"}}}`,
		ClientStats: []ClientStat{
			{Email: "alice", Up: 1000, Down: 2000, Total: 0, ExpiryTime: 0},
		},
	}
}

func TestMatchedClientsBySubID_Match(t *testing.T) {
	inbounds := []Inbound{sampleVlessInbound()}

	matches, err := MatchedClientsBySubID(inbounds, "tok-abc", "1.2.3.4")
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

	matches, err := MatchedClientsBySubID(inbounds, "does-not-exist", "1.2.3.4")
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

	matches, err := MatchedClientsBySubID([]Inbound{ib}, "tok-abc", "1.2.3.4")
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

	matches, err := MatchedClientsBySubID([]Inbound{ib}, "tok-abc", "1.2.3.4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 || matches[0].Server != "10.0.0.5" {
		t.Fatalf("expected explicit listen address to be used, got %+v", matches)
	}
}
