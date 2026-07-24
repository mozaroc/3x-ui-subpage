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

func writeFailure(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apiResponse[any]{Success: false, Msg: msg})
}

func TestClient_SendsBearerToken(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeEnvelope(w, []Inbound{{ID: 1, Protocol: "vless", Enable: true}})
	}))
	defer srv.Close()

	c, err := New(srv.URL, "test-api-key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.ListInbounds(t.Context()); err != nil {
		t.Fatalf("ListInbounds: %v", err)
	}
	if gotAuth != "Bearer test-api-key" {
		t.Fatalf("expected bearer auth header, got %q", gotAuth)
	}
}

func TestClient_401IsTerminalNotRetried(t *testing.T) {
	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "wrong-key", 5*time.Second, 3, time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.ListInbounds(t.Context()); err == nil {
		t.Fatal("expected error for 401")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected exactly 1 call (no retry on auth failure), got %d", calls)
	}
}

func TestClient_RetriesOn5xxThenSucceeds(t *testing.T) {
	var calls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "boom")
			return
		}
		writeEnvelope(w, []Inbound{{ID: 7, Protocol: "vless", Enable: true}})
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 5, time.Millisecond)
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
	if atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestClient_FailsAfterMaxAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 2, time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.ListInbounds(t.Context()); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
}

func TestClient_ListHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panel/api/hosts/list" {
			http.NotFound(w, r)
			return
		}
		writeEnvelope(w, []HostGroup{
			{GroupID: "g1", InboundIDs: []int{1}, Hosts: []string{"cdn.example.com:443"}, Security: "same"},
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	hosts, err := c.ListHosts(t.Context())
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(hosts) != 1 || hosts[0].GroupID != "g1" || hosts[0].Hosts[0] != "cdn.example.com:443" {
		t.Fatalf("unexpected hosts: %+v", hosts)
	}
}

func TestClient_AddClient(t *testing.T) {
	var gotBody addClientRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/panel/api/clients/add" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		writeEnvelope(w, nil)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	client := ManagedClient{Email: "alice", SubID: "sub-1", UUID: "uuid-1", Enable: true}
	if err := c.AddClient(t.Context(), client, []int{3, 5}); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	if gotBody.Client.Email != "alice" || gotBody.Client.SubID != "sub-1" {
		t.Fatalf("unexpected client in request: %+v", gotBody.Client)
	}
	if len(gotBody.InboundIDs) != 2 || gotBody.InboundIDs[0] != 3 || gotBody.InboundIDs[1] != 5 {
		t.Fatalf("unexpected inboundIds: %+v", gotBody.InboundIDs)
	}
}

func TestClient_UpdateClient(t *testing.T) {
	var gotPath string
	var gotBody ManagedClient

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		writeEnvelope(w, nil)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.UpdateClient(t.Context(), ManagedClient{Email: "alice", TotalGB: 5000, Enable: true}); err != nil {
		t.Fatalf("UpdateClient: %v", err)
	}
	if gotPath != "/panel/api/clients/update/alice" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotBody.Email != "alice" || gotBody.TotalGB != 5000 {
		t.Fatalf("unexpected body: %+v", gotBody)
	}
}

func TestClient_AttachAndDetachClient(t *testing.T) {
	var gotPath string
	var gotBody attachDetachRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		writeEnvelope(w, nil)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.AttachClient(t.Context(), "alice", []int{9}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	if gotPath != "/panel/api/clients/alice/attach" || len(gotBody.InboundIDs) != 1 || gotBody.InboundIDs[0] != 9 {
		t.Fatalf("unexpected attach request: path=%s body=%+v", gotPath, gotBody)
	}

	if err := c.DetachClient(t.Context(), "alice", []int{9}); err != nil {
		t.Fatalf("DetachClient: %v", err)
	}
	if gotPath != "/panel/api/clients/alice/detach" {
		t.Fatalf("unexpected detach path: %s", gotPath)
	}
}

func TestClient_DeleteClient(t *testing.T) {
	var gotURL string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		writeEnvelope(w, nil)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.DeleteClient(t.Context(), "alice"); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	if gotURL != "/panel/api/clients/del/alice?keepTraffic=0" {
		t.Fatalf("unexpected url: %s", gotURL)
	}
}

func TestClient_ResetClientTraffic(t *testing.T) {
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		writeEnvelope(w, nil)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.ResetClientTraffic(t.Context(), "alice"); err != nil {
		t.Fatalf("ResetClientTraffic: %v", err)
	}
	if gotPath != "/panel/api/clients/resetTraffic/alice" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
}

func TestClient_GetClient_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, map[string]any{
			"client": ManagedClient{Email: "alice", UUID: "uuid-1", Enable: true},
		})
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	client, found, err := c.GetClient(t.Context(), "alice")
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}
	if !found || client.Email != "alice" || client.UUID != "uuid-1" {
		t.Fatalf("unexpected result: client=%+v found=%v", client, found)
	}
}

func TestClient_GetClient_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFailure(w, " (record not found)")
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, found, err := c.GetClient(t.Context(), "ghost")
	if err != nil {
		t.Fatalf("expected no error for not-found, got %v", err)
	}
	if found {
		t.Fatal("expected found=false")
	}
}

func TestClient_GetClient_OtherFailurePropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFailure(w, "internal database error")
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, _, err := c.GetClient(t.Context(), "alice"); err == nil {
		t.Fatal("expected error to propagate for a non-not-found failure")
	}
}

func TestClient_DetachClient_RecordNotFoundIsIdempotentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFailure(w, "record not found")
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// A retry landing after the detach already took effect (response lost,
	// not the operation) must not be reported as a failed sync.
	if err := c.DetachClient(t.Context(), "ghost", []int{1}); err != nil {
		t.Fatalf("expected record-not-found to be treated as success, got %v", err)
	}
}

func TestClient_DeleteClient_RecordNotFoundIsIdempotentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFailure(w, "record not found")
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.DeleteClient(t.Context(), "ghost"); err != nil {
		t.Fatalf("expected record-not-found to be treated as success, got %v", err)
	}
}

func TestClient_ResetClientTraffic_RecordNotFoundIsIdempotentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFailure(w, "record not found")
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.ResetClientTraffic(t.Context(), "ghost"); err != nil {
		t.Fatalf("expected record-not-found to be treated as success, got %v", err)
	}
}

func TestClient_DeleteClient_OtherFailurePropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFailure(w, "internal database error")
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.DeleteClient(t.Context(), "alice"); err == nil {
		t.Fatal("expected a non-not-found failure to still propagate as an error")
	}
}

func TestClient_StopsRetryingImmediatelyOnContextCancellation(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "key", 5*time.Second, 5, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.ListInbounds(ctx); err == nil {
		t.Fatal("expected an error for an already-cancelled context")
	}
	if got := atomic.LoadInt32(&attempts); got > 1 {
		t.Fatalf("expected at most 1 request against an already-cancelled context, got %d", got)
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

func (f *fakeInboundLister) ListHosts(ctx context.Context) ([]HostGroup, error) {
	return nil, nil
}
