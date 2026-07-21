package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllow_WithinBurstSucceeds(t *testing.T) {
	l := New(60, 3)
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("expected request %d within burst to be allowed", i)
		}
	}
}

func TestAllow_ExceedsBurstFails(t *testing.T) {
	l := New(60, 1)
	if !l.Allow("1.2.3.4") {
		t.Fatal("expected first request to be allowed")
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("expected second immediate request to exceed burst=1")
	}
}

func TestAllow_KeysAreIndependent(t *testing.T) {
	l := New(60, 1)
	if !l.Allow("1.2.3.4") {
		t.Fatal("expected first key's request to be allowed")
	}
	if !l.Allow("5.6.7.8") {
		t.Fatal("expected a different key to have its own bucket")
	}
}

func TestClientIP_StripsPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:54321"
	if got := ClientIP(req); got != "10.0.0.1" {
		t.Errorf("ClientIP() = %q, want %q", got, "10.0.0.1")
	}
}

func TestClientIP_NoPortFallsBackToRaw(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "not-a-valid-host-port"
	if got := ClientIP(req); got != "not-a-valid-host-port" {
		t.Errorf("ClientIP() = %q, want raw fallback", got)
	}
}

type warnLogger struct{ warned bool }

func (w *warnLogger) Warn(msg string, args ...any) { w.warned = true }

func TestMiddleware_Returns429WhenExceeded(t *testing.T) {
	l := New(60, 1)
	logger := &warnLogger{}
	handler := l.Middleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1"

	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected first request OK, got %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on second request, got %d", rec2.Code)
	}
	if !logger.warned {
		t.Error("expected a warning to be logged on rate limit exceeded")
	}
}
