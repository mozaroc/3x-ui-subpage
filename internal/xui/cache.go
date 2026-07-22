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
// collapse into a single upstream panel call. Failures are cached for the
// same TTL too (not just successes) — without that, a panel that's down or
// unreachable turns every single call site (the admin Users list calls this
// once per row) into a full paid-in-full retry-with-backoff cycle, on every
// call, for as long as the outage lasts. A page rendering N users would pay
// that cost N times over, compounding into exactly the "increasingly slow"
// symptom this is fixed for.
type CachedLister struct {
	upstream InboundLister
	ttl      time.Duration

	group singleflight.Group

	mu        sync.RWMutex
	cached    []Inbound
	fetchedAt time.Time
	lastErr   error
	erroredAt time.Time
}

// NewCachedLister wraps upstream with a cache of the given TTL. A TTL of 0
// disables caching (every call hits upstream, still de-duplicated).
func NewCachedLister(upstream InboundLister, ttl time.Duration) *CachedLister {
	return &CachedLister{upstream: upstream, ttl: ttl}
}

// ListInbounds returns the cached inbound list, refreshing it from upstream
// if the TTL has elapsed. If the last upstream call failed within the TTL
// window, that error is replayed immediately instead of retrying upstream.
func (l *CachedLister) ListInbounds(ctx context.Context) ([]Inbound, error) {
	l.mu.RLock()
	fresh := l.ttl > 0 && time.Since(l.fetchedAt) < l.ttl
	cached := l.cached
	freshErr := l.ttl > 0 && l.lastErr != nil && time.Since(l.erroredAt) < l.ttl
	cachedErr := l.lastErr
	l.mu.RUnlock()

	if fresh {
		return cached, nil
	}
	if freshErr {
		return nil, cachedErr
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
		if l.ttl > 0 && l.lastErr != nil && time.Since(l.erroredAt) < l.ttl {
			err := l.lastErr
			l.mu.RUnlock()
			return nil, err
		}
		l.mu.RUnlock()

		inbounds, err := l.upstream.ListInbounds(ctx)

		l.mu.Lock()
		if err != nil {
			l.lastErr = err
			l.erroredAt = time.Now()
		} else {
			l.cached = inbounds
			l.fetchedAt = time.Now()
			l.lastErr = nil
		}
		l.mu.Unlock()

		if err != nil {
			return nil, err
		}
		return inbounds, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]Inbound), nil
}

// Invalidate forces the next ListInbounds call to hit upstream instead of
// returning the cached value (success or failure), regardless of TTL.
// Called by the sync worker after a successful write so readers
// (subscription resolution, the admin Users list) see the change
// immediately.
func (l *CachedLister) Invalidate() {
	l.mu.Lock()
	l.fetchedAt = time.Time{}
	l.lastErr = nil
	l.erroredAt = time.Time{}
	l.mu.Unlock()
}
