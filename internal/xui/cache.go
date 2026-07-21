package xui

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// InboundLister is the subset of *Client that CachedLister needs — kept
// as an interface so tests can supply a fake without spinning up an HTTP
// server.
type InboundLister interface {
	ListInbounds(ctx context.Context) ([]Inbound, error)
}

// CachedLister wraps an InboundLister with a short TTL cache and
// singleflight de-duplication, so many concurrent subscription requests
// collapse into a single upstream panel call.
type CachedLister struct {
	upstream InboundLister
	ttl      time.Duration

	group singleflight.Group

	mu        sync.RWMutex
	cached    []Inbound
	fetchedAt time.Time
}

// NewCachedLister wraps upstream with a cache of the given TTL. A TTL of 0
// disables caching (every call hits upstream, still de-duplicated).
func NewCachedLister(upstream InboundLister, ttl time.Duration) *CachedLister {
	return &CachedLister{upstream: upstream, ttl: ttl}
}

// ListInbounds returns the cached inbound list, refreshing it from upstream
// if the TTL has elapsed.
func (l *CachedLister) ListInbounds(ctx context.Context) ([]Inbound, error) {
	l.mu.RLock()
	fresh := l.ttl > 0 && time.Since(l.fetchedAt) < l.ttl
	cached := l.cached
	l.mu.RUnlock()

	if fresh {
		return cached, nil
	}

	v, err, _ := l.group.Do("list", func() (any, error) {
		// Re-check freshness: another goroutine may have refreshed while we
		// waited for the singleflight lock.
		l.mu.RLock()
		if l.ttl > 0 && time.Since(l.fetchedAt) < l.ttl {
			cached := l.cached
			l.mu.RUnlock()
			return cached, nil
		}
		l.mu.RUnlock()

		inbounds, err := l.upstream.ListInbounds(ctx)
		if err != nil {
			return nil, err
		}

		l.mu.Lock()
		l.cached = inbounds
		l.fetchedAt = time.Now()
		l.mu.Unlock()

		return inbounds, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]Inbound), nil
}
