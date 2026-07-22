package sync

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/irazin/3x-ui-subpage/internal/xui"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeWriter struct {
	getCalls    int32
	addCalls    int32
	attachCalls int32
	updateCalls int32
	detachCalls int32
	deleteCalls int32
	resetCalls  int32

	failAdd    bool
	failUpdate bool

	existingEmails map[string]bool // GetClient reports these as found

	lastInboundIDs []int
	lastClient     xui.ManagedClient
	lastEmail      string
}

func (f *fakeWriter) GetClient(ctx context.Context, email string) (xui.ManagedClient, bool, error) {
	atomic.AddInt32(&f.getCalls, 1)
	if f.existingEmails[email] {
		return xui.ManagedClient{Email: email}, true, nil
	}
	return xui.ManagedClient{}, false, nil
}

func (f *fakeWriter) AddClient(ctx context.Context, client xui.ManagedClient, inboundIDs []int) error {
	atomic.AddInt32(&f.addCalls, 1)
	f.lastClient = client
	f.lastInboundIDs = inboundIDs
	if f.failAdd {
		return errors.New("add failed")
	}
	return nil
}

func (f *fakeWriter) AttachClient(ctx context.Context, email string, inboundIDs []int) error {
	atomic.AddInt32(&f.attachCalls, 1)
	f.lastEmail = email
	f.lastInboundIDs = inboundIDs
	return nil
}

func (f *fakeWriter) UpdateClient(ctx context.Context, client xui.ManagedClient) error {
	atomic.AddInt32(&f.updateCalls, 1)
	f.lastClient = client
	if f.failUpdate {
		return errors.New("update failed")
	}
	return nil
}

func (f *fakeWriter) DetachClient(ctx context.Context, email string, inboundIDs []int) error {
	atomic.AddInt32(&f.detachCalls, 1)
	f.lastEmail = email
	f.lastInboundIDs = inboundIDs
	return nil
}

func (f *fakeWriter) DeleteClient(ctx context.Context, email string) error {
	atomic.AddInt32(&f.deleteCalls, 1)
	f.lastEmail = email
	return nil
}

func (f *fakeWriter) ResetClientTraffic(ctx context.Context, email string) error {
	atomic.AddInt32(&f.resetCalls, 1)
	f.lastEmail = email
	return nil
}

func TestWorker_AssignCreatesClientWhenNew(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{}

	id, err := s.Enqueue(1, 10, OpAssign, Payload{Email: "alice", SubID: "sub-1", UUID: "uuid-1", Enable: true})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var invalidated bool
	w := NewWorker(s, writer, func() { invalidated = true }, testLogger())
	w.Tick(t.Context())

	if atomic.LoadInt32(&writer.addCalls) != 1 {
		t.Fatalf("expected 1 AddClient call, got %d", writer.addCalls)
	}
	if writer.lastClient.Email != "alice" || writer.lastClient.SubID != "sub-1" {
		t.Fatalf("unexpected client pushed: %+v", writer.lastClient)
	}
	if len(writer.lastInboundIDs) != 1 || writer.lastInboundIDs[0] != 10 {
		t.Fatalf("expected inboundIDs=[10], got %v", writer.lastInboundIDs)
	}
	if !invalidated {
		t.Fatal("expected invalidate to be called on success")
	}

	jobs, err := s.ListForUser(1, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != id || jobs[0].Status != StatusSuccess {
		t.Fatalf("expected job %d marked success, got %+v", id, jobs)
	}
}

func TestWorker_AssignAttachesWhenClientAlreadyExists(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{existingEmails: map[string]bool{"alice": true}}

	if _, err := s.Enqueue(1, 11, OpAssign, Payload{Email: "alice", SubID: "sub-1", UUID: "uuid-1"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	w := NewWorker(s, writer, func() {}, testLogger())
	w.Tick(t.Context())

	if atomic.LoadInt32(&writer.addCalls) != 0 {
		t.Fatalf("expected AddClient not to be called, got %d", writer.addCalls)
	}
	if atomic.LoadInt32(&writer.attachCalls) != 1 {
		t.Fatalf("expected AttachClient for an already-existing client, got %d", writer.attachCalls)
	}
	if len(writer.lastInboundIDs) != 1 || writer.lastInboundIDs[0] != 11 {
		t.Fatalf("expected attach to inbound 11, got %v", writer.lastInboundIDs)
	}
}

func TestWorker_RetryThenSucceed(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{failAdd: true}

	id, _ := s.Enqueue(1, 10, OpAssign, Payload{Email: "alice"})

	w := NewWorker(s, writer, func() {}, testLogger())
	w.Tick(t.Context())

	jobs, _ := s.ListForUser(1, 10)
	if jobs[0].Status != StatusPending || jobs[0].Attempts != 1 {
		t.Fatalf("expected pending retry after 1 failure, got %+v", jobs[0])
	}

	// Make it due immediately and succeed this time.
	if _, err := db.Exec(`UPDATE sync_jobs SET next_attempt_at = 0 WHERE id = ?`, id); err != nil {
		t.Fatalf("force due: %v", err)
	}
	writer.failAdd = false
	w.Tick(t.Context())

	jobs, _ = s.ListForUser(1, 10)
	if jobs[0].Status != StatusSuccess {
		t.Fatalf("expected success on retry, got %+v", jobs[0])
	}
}

func TestWorker_FailsTerminalAfterMaxAttempts(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{failAdd: true}

	id, _ := s.Enqueue(1, 10, OpAssign, Payload{Email: "alice"})

	w := NewWorker(s, writer, func() {}, testLogger())
	w.maxAttempts = 2

	w.Tick(t.Context())
	if _, err := db.Exec(`UPDATE sync_jobs SET next_attempt_at = 0 WHERE id = ?`, id); err != nil {
		t.Fatalf("force due: %v", err)
	}
	w.Tick(t.Context())

	jobs, _ := s.ListForUser(1, 10)
	if jobs[0].Status != StatusFailed || jobs[0].Attempts != 2 {
		t.Fatalf("expected terminal failure after max attempts, got %+v", jobs[0])
	}
}

func TestWorker_UpdateUsesEmailNotInboundID(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{}

	if _, err := s.Enqueue(1, 10, OpUpdate, Payload{Email: "alice", TotalGB: 5000, Enable: true}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	w := NewWorker(s, writer, func() {}, testLogger())
	w.Tick(t.Context())

	if atomic.LoadInt32(&writer.updateCalls) != 1 {
		t.Fatalf("expected 1 UpdateClient call, got %d", writer.updateCalls)
	}
	if writer.lastClient.Email != "alice" || writer.lastClient.TotalGB != 5000 {
		t.Fatalf("unexpected client pushed: %+v", writer.lastClient)
	}
}

func TestWorker_UnassignAndResetTraffic(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{}

	if _, err := s.Enqueue(1, 10, OpUnassign, Payload{Email: "alice", UUID: "uuid-1"}); err != nil {
		t.Fatalf("Enqueue unassign: %v", err)
	}
	if _, err := s.Enqueue(1, 11, OpResetTraffic, Payload{Email: "bob"}); err != nil {
		t.Fatalf("Enqueue reset: %v", err)
	}

	w := NewWorker(s, writer, func() {}, testLogger())
	w.Tick(t.Context())

	if writer.lastEmail != "bob" {
		t.Fatalf("expected last call to be resetTraffic for bob, got %q", writer.lastEmail)
	}
	if atomic.LoadInt32(&writer.detachCalls) != 1 || atomic.LoadInt32(&writer.resetCalls) != 1 {
		t.Fatalf("expected 1 detach + 1 reset call, got detach=%d reset=%d", writer.detachCalls, writer.resetCalls)
	}
}

func TestWorker_Delete(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{}

	if _, err := s.Enqueue(1, 0, OpDelete, Payload{Email: "alice"}); err != nil {
		t.Fatalf("Enqueue delete: %v", err)
	}

	w := NewWorker(s, writer, func() {}, testLogger())
	w.Tick(t.Context())

	if atomic.LoadInt32(&writer.deleteCalls) != 1 {
		t.Fatalf("expected 1 DeleteClient call, got %d", writer.deleteCalls)
	}
	if writer.lastEmail != "alice" {
		t.Fatalf("expected DeleteClient for alice, got %q", writer.lastEmail)
	}
}

func TestWorker_UnknownOpFailsCleanly(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{}

	id, err := s.Enqueue(1, 10, "bogus", Payload{})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	w := NewWorker(s, writer, func() {}, testLogger())
	w.Tick(t.Context())

	jobs, _ := s.ListForUser(1, 10)
	if jobs[0].ID != id || jobs[0].Status != StatusPending {
		t.Fatalf("expected retry-pending for unknown op, got %+v", jobs[0])
	}
	if jobs[0].LastError == "" {
		t.Fatal("expected a recorded error for the unknown op")
	}
}
