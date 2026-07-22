package xui

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// InboundLister is the subset of *Client that lists inbounds — kept as an
// interface so tests/consumers that only need this (e.g. the admin
// inbound-picker) don't have to also implement ListHosts.
type InboundLister interface {
	ListInbounds(ctx context.Context) ([]Inbound, error)
}

// HostLister is the subset of *Client that lists Host groups.
type HostLister interface {
	ListHosts(ctx context.Context) ([]HostGroup, error)
}

// upstreamLister is what CachedLister needs from its backing client — both
// inbounds and hosts, fetched and cached independently. *Client and
// *DynamicClient both satisfy this already.
type upstreamLister interface {
	InboundLister
	HostLister
}

// ttlCache caches one upstream fetch's result — success *or* failure — for
// ttl, with singleflight de-duplication so many concurrent callers collapse
// into one upstream call. Caching failures too (not just successes) matters:
// without it, a panel that's down turns every single call site (the admin
// Users list calls this once per row) into a full paid-in-full
// retry-with-backoff cycle, on every call, for as long as the outage lasts —
// a page rendering N rows would pay that cost N times over.
type ttlCache[T any] struct {
	ttl time.Duration

	group singleflight.Group

	mu        sync.RWMutex
	value     T
	fetchedAt time.Time
	lastErr   error
	erroredAt time.Time
}

// get returns the cached value, calling fetch to refresh it if the TTL has
// elapsed. If the last fetch failed within the TTL window, that error is
// replayed immediately instead of calling fetch again. key namespaces the
// singleflight call (each ttlCache instance only ever uses one key, but the
// group itself is shared plumbing so this keeps call sites self-documenting).
func (c *ttlCache[T]) get(ctx context.Context, key string, fetch func(context.Context) (T, error)) (T, error) {
	c.mu.RLock()
	fresh := c.ttl > 0 && time.Since(c.fetchedAt) < c.ttl
	cached := c.value
	freshErr := c.ttl > 0 && c.lastErr != nil && time.Since(c.erroredAt) < c.ttl
	cachedErr := c.lastErr
	c.mu.RUnlock()

	if fresh {
		return cached, nil
	}
	if freshErr {
		var zero T
		return zero, cachedErr
	}

	v, err, _ := c.group.Do(key, func() (any, error) {
		// Re-check freshness: another goroutine may have refreshed while we
		// waited for the singleflight lock.
		c.mu.RLock()
		if c.ttl > 0 && time.Since(c.fetchedAt) < c.ttl {
			cached := c.value
			c.mu.RUnlock()
			return cached, nil
		}
		if c.ttl > 0 && c.lastErr != nil && time.Since(c.erroredAt) < c.ttl {
			err := c.lastErr
			c.mu.RUnlock()
			return nil, err
		}
		c.mu.RUnlock()

		value, err := fetch(ctx)

		c.mu.Lock()
		if err != nil {
			c.lastErr = err
			c.erroredAt = time.Now()
		} else {
			c.value = value
			c.fetchedAt = time.Now()
			c.lastErr = nil
		}
		c.mu.Unlock()

		if err != nil {
			return nil, err
		}
		return value, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return v.(T), nil
}

// invalidate clears both the cached value and any cached failure,
// regardless of TTL.
func (c *ttlCache[T]) invalidate() {
	c.mu.Lock()
	var zero T
	c.value = zero
	c.fetchedAt = time.Time{}
	c.lastErr = nil
	c.erroredAt = time.Time{}
	c.mu.Unlock()
}

// CachedLister wraps an upstreamLister with a short TTL cache (per
// resource — inbounds and Host groups are cached independently) so many
// concurrent subscription requests collapse into a single upstream call.
type CachedLister struct {
	upstream upstreamLister

	inbounds ttlCache[[]Inbound]
	hosts    ttlCache[[]HostGroup]
}

// NewCachedLister wraps upstream with a cache of the given TTL. A TTL of 0
// disables caching (every call hits upstream, still de-duplicated).
func NewCachedLister(upstream upstreamLister, ttl time.Duration) *CachedLister {
	l := &CachedLister{upstream: upstream}
	l.inbounds.ttl = ttl
	l.hosts.ttl = ttl
	return l
}

// ListInbounds returns the cached inbound list, refreshing it from upstream
// if the TTL has elapsed (see ttlCache.get for the failure-caching behavior).
func (l *CachedLister) ListInbounds(ctx context.Context) ([]Inbound, error) {
	return l.inbounds.get(ctx, "inbounds", l.upstream.ListInbounds)
}

// ListHosts returns the cached Host group list, refreshing it from upstream
// if the TTL has elapsed.
func (l *CachedLister) ListHosts(ctx context.Context) ([]HostGroup, error) {
	return l.hosts.get(ctx, "hosts", l.upstream.ListHosts)
}

// Invalidate forces the next ListInbounds/ListHosts call to hit upstream
// instead of returning the cached value (success or failure), regardless of
// TTL. Called by the sync worker after every successful write so readers
// (subscription resolution, the admin Users list) see the change
// immediately.
func (l *CachedLister) Invalidate() {
	l.inbounds.invalidate()
	l.hosts.invalidate()
}
