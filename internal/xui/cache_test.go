package xui

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type failingLister struct {
	calls int32
	err   error
}

func (f *failingLister) ListInbounds(ctx context.Context) ([]Inbound, error) {
	atomic.AddInt32(&f.calls, 1)
	return nil, f.err
}

func (f *failingLister) ListHosts(ctx context.Context) ([]HostGroup, error) {
	return nil, nil
}

// TestCachedLister_CachesFailuresTooWithinTTL guards against a real bug:
// only successes were cached, so a persistently unreachable/erroring panel
// meant every single ListInbounds call — even several in the same request,
// e.g. the admin Users list calling this once per row — paid the full
// underlying retry-with-backoff cost every time, for as long as the outage
// lasted. A page listing N users would pay that cost N times over.
func TestCachedLister_CachesFailuresTooWithinTTL(t *testing.T) {
	upstream := &failingLister{err: errors.New("connection refused")}
	cl := NewCachedLister(upstream, time.Hour)

	for i := 0; i < 5; i++ {
		if _, err := cl.ListInbounds(t.Context()); err == nil {
			t.Fatal("expected error to propagate")
		}
	}

	if got := atomic.LoadInt32(&upstream.calls); got != 1 {
		t.Fatalf("expected exactly 1 upstream call (rest served from the cached failure), got %d", got)
	}
}

func TestCachedLister_InvalidateClearsCachedFailure(t *testing.T) {
	upstream := &failingLister{err: errors.New("connection refused")}
	cl := NewCachedLister(upstream, time.Hour)

	if _, err := cl.ListInbounds(t.Context()); err == nil {
		t.Fatal("expected error")
	}
	if _, err := cl.ListInbounds(t.Context()); err == nil {
		t.Fatal("expected cached error")
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 1 {
		t.Fatalf("expected 1 call before Invalidate, got %d", got)
	}

	cl.Invalidate()
	upstream.err = nil
	upstream.calls = 0

	if _, err := cl.ListInbounds(t.Context()); err != nil {
		t.Fatalf("expected success after Invalidate + upstream recovery, got %v", err)
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 1 {
		t.Fatalf("expected exactly 1 fresh upstream call after Invalidate, got %d", got)
	}
}

type failingHostLister struct {
	inboundCalls int32
	hostCalls    int32
	hostErr      error
	hosts        []HostGroup
}

func (f *failingHostLister) ListInbounds(ctx context.Context) ([]Inbound, error) {
	atomic.AddInt32(&f.inboundCalls, 1)
	return nil, nil
}

func (f *failingHostLister) ListHosts(ctx context.Context) ([]HostGroup, error) {
	atomic.AddInt32(&f.hostCalls, 1)
	return f.hosts, f.hostErr
}

// TestCachedLister_HostsCachedIndependentlyFromInbounds guards against the
// two resources sharing one cache slot by accident after the ttlCache[T]
// extraction — a failure fetching hosts must not be replayed for
// ListInbounds, and vice versa; each resource gets its own TTL/failure
// state.
func TestCachedLister_HostsCachedIndependentlyFromInbounds(t *testing.T) {
	upstream := &failingHostLister{hostErr: errors.New("hosts unreachable")}
	cl := NewCachedLister(upstream, time.Hour)

	if _, err := cl.ListHosts(t.Context()); err == nil {
		t.Fatal("expected hosts error")
	}
	if _, err := cl.ListInbounds(t.Context()); err != nil {
		t.Fatalf("expected ListInbounds to succeed independently of the ListHosts failure, got %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := cl.ListHosts(t.Context()); err == nil {
			t.Fatal("expected cached hosts error to keep propagating")
		}
	}
	if got := atomic.LoadInt32(&upstream.hostCalls); got != 1 {
		t.Fatalf("expected exactly 1 upstream ListHosts call (rest cached), got %d", got)
	}
	if got := atomic.LoadInt32(&upstream.inboundCalls); got != 1 {
		t.Fatalf("expected exactly 1 upstream ListInbounds call, got %d", got)
	}
}

func TestCachedLister_FailureExpiresAfterTTL(t *testing.T) {
	upstream := &failingLister{err: errors.New("connection refused")}
	cl := NewCachedLister(upstream, 10*time.Millisecond)

	if _, err := cl.ListInbounds(t.Context()); err == nil {
		t.Fatal("expected error")
	}

	time.Sleep(20 * time.Millisecond)
	upstream.err = nil

	if _, err := cl.ListInbounds(t.Context()); err != nil {
		t.Fatalf("expected retry after TTL expiry to succeed, got %v", err)
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 2 {
		t.Fatalf("expected 2 upstream calls (initial failure + post-TTL retry), got %d", got)
	}
}
