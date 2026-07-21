package xui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func writeEnvelope(w http.ResponseWriter, obj any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apiResponse[any]{Success: true, Obj: obj})
}

func TestClient_LoginAndListInbounds(t *testing.T) {
	var loginCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			atomic.AddInt32(&loginCalls, 1)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "tok"})
			writeEnvelope(w, nil)
		case "/panel/api/inbounds/list":
			writeEnvelope(w, []Inbound{{ID: 1, Protocol: "vless", Enable: true}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "admin", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	inbounds, err := c.ListInbounds(t.Context())
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) != 1 || inbounds[0].ID != 1 {
		t.Fatalf("unexpected inbounds: %+v", inbounds)
	}
	if atomic.LoadInt32(&loginCalls) != 1 {
		t.Fatalf("expected exactly 1 login call, got %d", loginCalls)
	}
}

func TestClient_ReloginOn401(t *testing.T) {
	var loginCalls int32
	var listCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			atomic.AddInt32(&loginCalls, 1)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "tok"})
			writeEnvelope(w, nil)
		case "/panel/api/inbounds/list":
			n := atomic.AddInt32(&listCalls, 1)
			if n == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeEnvelope(w, []Inbound{{ID: 42, Protocol: "vless", Enable: true}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "admin", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	inbounds, err := c.ListInbounds(t.Context())
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) != 1 || inbounds[0].ID != 42 {
		t.Fatalf("unexpected inbounds after relogin: %+v", inbounds)
	}
	if atomic.LoadInt32(&loginCalls) != 2 {
		t.Fatalf("expected 2 login calls (initial + relogin), got %d", loginCalls)
	}
}

func TestClient_RetriesOn5xxThenSucceeds(t *testing.T) {
	var listCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "tok"})
			writeEnvelope(w, nil)
		case "/panel/api/inbounds/list":
			n := atomic.AddInt32(&listCalls, 1)
			if n < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, "boom")
				return
			}
			writeEnvelope(w, []Inbound{{ID: 7, Protocol: "vless", Enable: true}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "admin", 5*time.Second, 5, time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	inbounds, err := c.ListInbounds(t.Context())
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if len(inbounds) != 1 || inbounds[0].ID != 7 {
		t.Fatalf("unexpected inbounds: %+v", inbounds)
	}
	if atomic.LoadInt32(&listCalls) != 3 {
		t.Fatalf("expected 3 attempts, got %d", listCalls)
	}
}

func TestClient_FailsAfterMaxAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "tok"})
			writeEnvelope(w, nil)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "admin", 5*time.Second, 2, time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.ListInbounds(t.Context()); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
}
