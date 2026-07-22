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
	addCalls    int32
	updateCalls int32
	deleteCalls int32
	resetCalls  int32

	failAdd    bool
	failUpdate bool

	lastInboundID int
	lastPayload   xui.ClientPayload
	lastIdent     string
	lastEmail     string
}

func (f *fakeWriter) AddClient(ctx context.Context, inboundID int, c xui.ClientPayload) error {
	atomic.AddInt32(&f.addCalls, 1)
	f.lastInboundID = inboundID
	f.lastPayload = c
	if f.failAdd {
		return errors.New("add failed")
	}
	return nil
}

func (f *fakeWriter) UpdateClient(ctx context.Context, inboundID int, c xui.ClientPayload) error {
	atomic.AddInt32(&f.updateCalls, 1)
	f.lastInboundID = inboundID
	f.lastPayload = c
	if f.failUpdate {
		return errors.New("update failed")
	}
	return nil
}

func (f *fakeWriter) DeleteClient(ctx context.Context, inboundID int, identifier string) error {
	atomic.AddInt32(&f.deleteCalls, 1)
	f.lastInboundID = inboundID
	f.lastIdent = identifier
	return nil
}

func (f *fakeWriter) ResetClientTraffic(ctx context.Context, inboundID int, email string) error {
	atomic.AddInt32(&f.resetCalls, 1)
	f.lastInboundID = inboundID
	f.lastEmail = email
	return nil
}

type fakeLister struct {
	inbounds []xui.Inbound
	err      error
}

func (f *fakeLister) ListInbounds(ctx context.Context) ([]xui.Inbound, error) {
	return f.inbounds, f.err
}

func TestWorker_AssignSucceeds(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{}

	id, err := s.Enqueue(1, 10, OpAssign, Payload{Email: "alice", SubID: "sub-1", UUID: "uuid-1", Enable: true})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var invalidated bool
	w := NewWorker(s, writer, nil, func() { invalidated = true }, testLogger())
	w.Tick(t.Context())

	if atomic.LoadInt32(&writer.addCalls) != 1 {
		t.Fatalf("expected 1 AddClient call, got %d", writer.addCalls)
	}
	if writer.lastPayload.Email != "alice" || writer.lastPayload.SubID != "sub-1" {
		t.Fatalf("unexpected payload pushed: %+v", writer.lastPayload)
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

func TestWorker_AssignConflictSelfHealsToUpdate(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{}
	lister := &fakeLister{inbounds: []xui.Inbound{
		{
			ID:       10,
			Protocol: "vless",
			Settings: `{"clients":[{"id":"existing-uuid","email":"alice","subId":"sub-1"}]}`,
		},
	}}

	if _, err := s.Enqueue(1, 10, OpAssign, Payload{Email: "alice", SubID: "sub-1", UUID: "uuid-1"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	w := NewWorker(s, writer, lister, func() {}, testLogger())
	w.Tick(t.Context())

	if atomic.LoadInt32(&writer.addCalls) != 0 {
		t.Fatalf("expected AddClient not to be called, got %d", writer.addCalls)
	}
	if atomic.LoadInt32(&writer.updateCalls) != 1 {
		t.Fatalf("expected UpdateClient to self-heal the conflict, got %d", writer.updateCalls)
	}
}

func TestWorker_RetryThenSucceed(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)
	writer := &fakeWriter{failAdd: true}

	id, _ := s.Enqueue(1, 10, OpAssign, Payload{Email: "alice"})

	w := NewWorker(s, writer, nil, func() {}, testLogger())
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

	w := NewWorker(s, writer, nil, func() {}, testLogger())
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

	w := NewWorker(s, writer, nil, func() {}, testLogger())
	w.Tick(t.Context())

	if writer.lastIdent != "uuid-1" {
		t.Fatalf("expected DeleteClient identifier uuid-1, got %q", writer.lastIdent)
	}
	if writer.lastEmail != "bob" {
		t.Fatalf("expected ResetClientTraffic email bob, got %q", writer.lastEmail)
	}
	if atomic.LoadInt32(&writer.deleteCalls) != 1 || atomic.LoadInt32(&writer.resetCalls) != 1 {
		t.Fatalf("expected 1 delete + 1 reset call, got delete=%d reset=%d", writer.deleteCalls, writer.resetCalls)
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

	w := NewWorker(s, writer, nil, func() {}, testLogger())
	w.Tick(t.Context())

	jobs, _ := s.ListForUser(1, 10)
	if jobs[0].ID != id || jobs[0].Status != StatusPending {
		t.Fatalf("expected retry-pending for unknown op, got %+v", jobs[0])
	}
	if jobs[0].LastError == "" {
		t.Fatal("expected a recorded error for the unknown op")
	}
}
