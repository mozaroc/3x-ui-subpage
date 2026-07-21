package resolver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/xui"
)

type fakeLister struct {
	inbounds []xui.Inbound
	err      error
}

func (f fakeLister) ListInbounds(ctx context.Context) ([]xui.Inbound, error) {
	return f.inbounds, f.err
}

func inboundWithClient(id int, subID, email string, enable bool, totalGB, expiryMs, up, down int64) xui.Inbound {
	return xui.Inbound{
		ID:             id,
		Enable:         true,
		Protocol:       "vless",
		Port:           443,
		Settings:       `{"clients":[{"id":"uuid-` + email + `","email":"` + email + `","subId":"` + subID + `","enable":` + boolStr(enable) + `,"totalGB":` + itoa(totalGB) + `,"expiryTime":` + itoa(expiryMs) + `}]}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
		ClientStats: []xui.ClientStat{
			{Email: email, Up: up, Down: down},
		},
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestResolve_ActiveSubscription(t *testing.T) {
	lister := fakeLister{inbounds: []xui.Inbound{
		inboundWithClient(1, "tok", "alice", true, 10_000_000_000, 0, 1000, 2000),
	}}
	r := New(lister, "1.2.3.4")

	sub, err := r.Resolve(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sub.Username != "alice" {
		t.Errorf("expected username alice, got %q", sub.Username)
	}
	if sub.Status != domain.StatusActive {
		t.Errorf("expected active status, got %q", sub.Status)
	}
	if sub.Traffic.Used() != 3000 {
		t.Errorf("expected used=3000, got %d", sub.Traffic.Used())
	}
	if len(sub.Clients) != 1 {
		t.Errorf("expected 1 matched client, got %d", len(sub.Clients))
	}
}

func TestResolve_NotFound(t *testing.T) {
	lister := fakeLister{inbounds: []xui.Inbound{
		inboundWithClient(1, "tok", "alice", true, 0, 0, 0, 0),
	}}
	r := New(lister, "1.2.3.4")

	_, err := r.Resolve(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestResolve_ExpiredStatus(t *testing.T) {
	pastMs := time.Now().Add(-time.Hour).UnixMilli()
	lister := fakeLister{inbounds: []xui.Inbound{
		inboundWithClient(1, "tok", "bob", true, 0, pastMs, 0, 0),
	}}
	r := New(lister, "1.2.3.4")

	sub, err := r.Resolve(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sub.Status != domain.StatusExpired {
		t.Errorf("expected expired status, got %q", sub.Status)
	}
}

func TestResolve_DepletedStatus(t *testing.T) {
	lister := fakeLister{inbounds: []xui.Inbound{
		inboundWithClient(1, "tok", "carl", true, 1000, 0, 600, 500),
	}}
	r := New(lister, "1.2.3.4")

	sub, err := r.Resolve(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sub.Status != domain.StatusDepleted {
		t.Errorf("expected depleted status, got %q", sub.Status)
	}
}

func TestResolve_DisabledStatus(t *testing.T) {
	lister := fakeLister{inbounds: []xui.Inbound{
		inboundWithClient(1, "tok", "dana", false, 0, 0, 0, 0),
	}}
	r := New(lister, "1.2.3.4")

	sub, err := r.Resolve(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sub.Status != domain.StatusDisabled {
		t.Errorf("expected disabled status, got %q", sub.Status)
	}
}

func TestResolve_ListerError(t *testing.T) {
	lister := fakeLister{err: errors.New("boom")}
	r := New(lister, "1.2.3.4")

	if _, err := r.Resolve(context.Background(), "tok"); err == nil {
		t.Fatal("expected error propagated from lister")
	}
}
