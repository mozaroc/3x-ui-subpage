package tmplctx

import (
	"testing"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

func TestFromMatchedClient_CombinesInboundAndClientName(t *testing.T) {
	mc := domain.MatchedClient{
		Remark: "🇱🇻 reality",
		Client: domain.ClientAccount{Email: "alice"},
	}
	ctx := FromMatchedClient(mc)
	if ctx.Remark != "🇱🇻 reality + alice" {
		t.Errorf(`expected "🇱🇻 reality + alice", got %q`, ctx.Remark)
	}
}

func TestFromMatchedClient_DegradesWhenClientNameMissing(t *testing.T) {
	mc := domain.MatchedClient{Remark: "🇱🇻 reality", Client: domain.ClientAccount{Email: ""}}
	ctx := FromMatchedClient(mc)
	if ctx.Remark != "🇱🇻 reality" {
		t.Errorf("expected just the inbound name, got %q", ctx.Remark)
	}
}

func TestFromMatchedClient_DegradesWhenInboundNameMissing(t *testing.T) {
	mc := domain.MatchedClient{Remark: "", Client: domain.ClientAccount{Email: "alice"}}
	ctx := FromMatchedClient(mc)
	if ctx.Remark != "alice" {
		t.Errorf("expected just the client name, got %q", ctx.Remark)
	}
}

func TestFromMatchedClient_DisambiguatesSameClientAcrossInbounds(t *testing.T) {
	a := FromMatchedClient(domain.MatchedClient{Remark: "reality", Client: domain.ClientAccount{Email: "alice"}})
	b := FromMatchedClient(domain.MatchedClient{Remark: "ws", Client: domain.ClientAccount{Email: "alice"}})
	if a.Remark == b.Remark {
		t.Fatalf("expected the same client on two different inbounds to render distinct names, got %q for both", a.Remark)
	}
}

func TestFromMatchedClient_InsecurePassthrough(t *testing.T) {
	mc := domain.MatchedClient{Stream: domain.StreamSettings{TLS: domain.TLSSettings{Insecure: true}}}
	if !FromMatchedClient(mc).Insecure {
		t.Error("expected Insecure to pass through from Stream.TLS.Insecure")
	}
}
