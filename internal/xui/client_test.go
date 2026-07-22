package xui

import (
	"context"
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

func TestClient_AddClient(t *testing.T) {
	var gotBody addClientRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "tok"})
			writeEnvelope(w, nil)
		case r.URL.Path == "/panel/api/inbounds/addClient" && r.Method == http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode request body: %v", err)
			}
			writeEnvelope(w, nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "admin", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	client := ClientPayload{ID: "uuid-1", Email: "alice", SubID: "sub-1", Enable: true}
	if err := c.AddClient(t.Context(), 5, client); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	if gotBody.ID != 5 {
		t.Fatalf("expected inbound id 5, got %d", gotBody.ID)
	}
	var settings clientSettingsBody
	if err := json.Unmarshal([]byte(gotBody.Settings), &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if len(settings.Clients) != 1 || settings.Clients[0].Email != "alice" {
		t.Fatalf("unexpected settings clients: %+v", settings.Clients)
	}
}

func TestClient_UpdateClient_UsesIDElsePassword(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "tok"})
			writeEnvelope(w, nil)
		case r.Method == http.MethodPost:
			gotPath = r.URL.Path
			writeEnvelope(w, nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "admin", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.UpdateClient(t.Context(), 5, ClientPayload{ID: "uuid-1", Password: "pw"}); err != nil {
		t.Fatalf("UpdateClient: %v", err)
	}
	if gotPath != "/panel/api/inbounds/updateClient/uuid-1" {
		t.Fatalf("expected id-based path, got %s", gotPath)
	}

	if err := c.UpdateClient(t.Context(), 5, ClientPayload{Password: "trojan-pw"}); err != nil {
		t.Fatalf("UpdateClient: %v", err)
	}
	if gotPath != "/panel/api/inbounds/updateClient/trojan-pw" {
		t.Fatalf("expected password-based path when id is empty, got %s", gotPath)
	}
}

func TestClient_DeleteClient(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "tok"})
			writeEnvelope(w, nil)
		case r.Method == http.MethodPost:
			gotPath = r.URL.Path
			writeEnvelope(w, nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "admin", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.DeleteClient(t.Context(), 5, "uuid-1"); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	if gotPath != "/panel/api/inbounds/5/delClient/uuid-1" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
}

func TestClient_ResetClientTraffic(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "tok"})
			writeEnvelope(w, nil)
		case r.Method == http.MethodPost:
			gotPath = r.URL.Path
			writeEnvelope(w, nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "admin", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.ResetClientTraffic(t.Context(), 5, "alice"); err != nil {
		t.Fatalf("ResetClientTraffic: %v", err)
	}
	if gotPath != "/panel/api/inbounds/5/resetClientTraffic/alice" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
}

func TestClient_WriteRetriesOn5xxThenSucceeds(t *testing.T) {
	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "tok"})
			writeEnvelope(w, nil)
		case r.URL.Path == "/panel/api/inbounds/addClient":
			n := atomic.AddInt32(&calls, 1)
			if n < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, "boom")
				return
			}
			writeEnvelope(w, nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "admin", 5*time.Second, 5, time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.AddClient(t.Context(), 1, ClientPayload{ID: "uuid-1"}); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestCachedLister_Invalidate(t *testing.T) {
	fake := &fakeInboundLister{inbounds: []Inbound{{ID: 1}}}
	cl := NewCachedLister(fake, time.Hour)

	if _, err := cl.ListInbounds(t.Context()); err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	fake.inbounds = []Inbound{{ID: 2}}

	got, err := cl.ListInbounds(t.Context())
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if got[0].ID != 1 {
		t.Fatalf("expected cached value before Invalidate, got %+v", got)
	}

	cl.Invalidate()

	got, err = cl.ListInbounds(t.Context())
	if err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if got[0].ID != 2 {
		t.Fatalf("expected fresh value after Invalidate, got %+v", got)
	}
}

type fakeInboundLister struct {
	inbounds []Inbound
}

func (f *fakeInboundLister) ListInbounds(ctx context.Context) ([]Inbound, error) {
	return f.inbounds, nil
}
