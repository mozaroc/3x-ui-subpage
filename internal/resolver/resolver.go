// Package resolver turns a subscription token (subId) into a fully resolved
// domain.Subscription by scanning the panel's cached inbound list.
package resolver

import (
	"context"
	"errors"
	"time"

	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/xui"
)

// ErrNotFound is returned when no client across any inbound carries the
// given subId.
var ErrNotFound = errors.New("resolver: subscription not found")

// Lister is the subset of xui.CachedLister (or xui.Client) resolver needs.
type Lister interface {
	ListInbounds(ctx context.Context) ([]xui.Inbound, error)
	ListHosts(ctx context.Context) ([]xui.HostGroup, error)
}

// Resolver resolves subscription tokens against a panel's inbound list.
type Resolver struct {
	lister       Lister
	fallbackHost string
}

// New builds a Resolver. fallbackHost is substituted for inbounds whose
// "listen" address is empty or a wildcard.
func New(lister Lister, fallbackHost string) *Resolver {
	return &Resolver{lister: lister, fallbackHost: fallbackHost}
}

// Resolve looks up every (inbound, client) pair matching subID and folds
// them into a single domain.Subscription. Returns ErrNotFound if subID
// matches nothing.
func (r *Resolver) Resolve(ctx context.Context, subID string) (domain.Subscription, error) {
	inbounds, err := r.lister.ListInbounds(ctx)
	if err != nil {
		return domain.Subscription{}, err
	}

	// Hosts are an enhancement (3x-ui's own per-inbound connection-address/
	// TLS overrides), not required — a fetch failure degrades to
	// inbound-derived connection info rather than failing the whole
	// subscription.
	hosts, _ := r.lister.ListHosts(ctx)

	matches, err := xui.MatchedClientsBySubID(inbounds, hosts, subID, r.fallbackHost)
	if err != nil {
		return domain.Subscription{}, err
	}
	if len(matches) == 0 {
		return domain.Subscription{}, ErrNotFound
	}

	sub := domain.Subscription{
		SubID:   subID,
		Clients: matches,
	}

	var anyEnabled bool
	var minExpiryMs int64
	for _, m := range matches {
		if sub.Username == "" && m.Client.Email != "" {
			sub.Username = m.Client.Email
		}
		if m.Client.Enable {
			anyEnabled = true
		}

		sub.Traffic.Up += m.Client.Up
		sub.Traffic.Down += m.Client.Down
		if m.Client.TotalGB > sub.Traffic.Total {
			sub.Traffic.Total = m.Client.TotalGB
		}

		if m.Client.ExpiryMs > 0 && (minExpiryMs == 0 || m.Client.ExpiryMs < minExpiryMs) {
			minExpiryMs = m.Client.ExpiryMs
		}
	}

	if minExpiryMs > 0 {
		t := time.UnixMilli(minExpiryMs)
		sub.ExpiresAt = &t
	}

	sub.Status = computeStatus(anyEnabled, sub.ExpiresAt, sub.Traffic)

	return sub, nil
}

func computeStatus(anyEnabled bool, expiresAt *time.Time, traffic domain.TrafficStats) domain.Status {
	if !anyEnabled {
		return domain.StatusDisabled
	}
	if expiresAt != nil && expiresAt.Before(time.Now()) {
		return domain.StatusExpired
	}
	if traffic.Total > 0 && traffic.Used() >= traffic.Total {
		return domain.StatusDepleted
	}
	return domain.StatusActive
}
