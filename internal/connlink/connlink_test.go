package connlink

import (
	"errors"
	"testing"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

type fakeLinkBuilder struct {
	fail map[int]bool
}

func (f fakeLinkBuilder) BuildLink(mc domain.MatchedClient, profile string) (string, error) {
	if f.fail[mc.InboundID] {
		return "", errors.New("unsupported protocol")
	}
	return string(mc.Protocol) + "://" + profile, nil
}

func TestBuild_OneViewPerClient(t *testing.T) {
	clients := []domain.MatchedClient{
		{InboundID: 1, Remark: "a", Protocol: domain.ProtocolVLESS},
		{InboundID: 2, Remark: "b", Protocol: domain.ProtocolTrojan},
	}

	views := Build("tok-abc", clients, "default", fakeLinkBuilder{}, nil)

	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}
	if views[0].Link != "vless://default" || views[0].Tag != "a" {
		t.Errorf("unexpected first view: %+v", views[0])
	}
	if views[0].QRPngURL != "/sub/tok-abc/link/1/qr.png" {
		t.Errorf("unexpected qr png url: %q", views[0].QRPngURL)
	}
	if views[0].ConfigURL != "/sub/tok-abc/link/1/config.json" {
		t.Errorf("unexpected config url: %q", views[0].ConfigURL)
	}
}

func TestBuild_SkipsFailingClientsViaOnError(t *testing.T) {
	clients := []domain.MatchedClient{
		{InboundID: 1, Protocol: domain.ProtocolVLESS},
		{InboundID: 2, Protocol: domain.ProtocolTrojan},
	}

	var failed []int
	views := Build("tok-abc", clients, "default", fakeLinkBuilder{fail: map[int]bool{1: true}}, func(mc domain.MatchedClient, err error) {
		failed = append(failed, mc.InboundID)
	})

	if len(views) != 1 {
		t.Fatalf("expected 1 surviving view, got %d: %+v", len(views), views)
	}
	if views[0].InboundID != 2 {
		t.Errorf("expected the non-failing client to survive, got inbound %d", views[0].InboundID)
	}
	if len(failed) != 1 || failed[0] != 1 {
		t.Errorf("expected onError called once for inbound 1, got %+v", failed)
	}
}

func TestBuild_NilOnErrorIsSafe(t *testing.T) {
	clients := []domain.MatchedClient{{InboundID: 1, Protocol: domain.ProtocolVLESS}}
	views := Build("tok-abc", clients, "default", fakeLinkBuilder{fail: map[int]bool{1: true}}, nil)
	if len(views) != 0 {
		t.Fatalf("expected 0 views, got %d", len(views))
	}
}
