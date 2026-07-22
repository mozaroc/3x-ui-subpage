package sync

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/irazin/3x-ui-subpage/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEnqueueAndClaimBatch(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)

	id, err := s.Enqueue(1, 10, OpAssign, Payload{Email: "alice", SubID: "sub-1"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	claimed, err := s.ClaimBatch(10)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != id {
		t.Fatalf("expected 1 claimed job with id %d, got %+v", id, claimed)
	}
	if claimed[0].Status != StatusInProgress {
		t.Fatalf("expected in_progress after claim, got %s", claimed[0].Status)
	}
	if claimed[0].Payload.Email != "alice" {
		t.Fatalf("expected payload round trip, got %+v", claimed[0].Payload)
	}

	// A second claim shouldn't pick up the same (now in_progress) row.
	again, err := s.ClaimBatch(10)
	if err != nil {
		t.Fatalf("ClaimBatch again: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("expected no jobs on second claim, got %+v", again)
	}
}

func TestMarkSuccessAndRetryAndFailedTerminal(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)

	id1, _ := s.Enqueue(1, 10, OpAssign, Payload{Email: "a"})
	id2, _ := s.Enqueue(1, 11, OpAssign, Payload{Email: "b"})

	if _, err := s.ClaimBatch(10); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}

	if err := s.MarkSuccess(id1); err != nil {
		t.Fatalf("MarkSuccess: %v", err)
	}
	if err := s.MarkRetry(id2, 1, "boom", 0); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}

	jobs, err := s.ListForUser(1, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	byID := map[int64]Job{}
	for _, j := range jobs {
		byID[j.ID] = j
	}
	if byID[id1].Status != StatusSuccess {
		t.Fatalf("expected success, got %s", byID[id1].Status)
	}
	if byID[id2].Status != StatusPending || byID[id2].Attempts != 1 || byID[id2].LastError != "boom" {
		t.Fatalf("unexpected retry state: %+v", byID[id2])
	}

	// id2 is due again (next_attempt_at=0) — claim and terminally fail it.
	claimed, err := s.ClaimBatch(10)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != id2 {
		t.Fatalf("expected to reclaim id2, got %+v", claimed)
	}
	if err := s.MarkFailedTerminal(id2, 8, "gave up"); err != nil {
		t.Fatalf("MarkFailedTerminal: %v", err)
	}

	final, err := s.ListForUser(1, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	for _, j := range final {
		if j.ID == id2 && j.Status != StatusFailed {
			t.Fatalf("expected failed, got %s", j.Status)
		}
	}
}

func TestRetry_ReQueuesFailedJob(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)

	id, _ := s.Enqueue(1, 10, OpAssign, Payload{})
	if _, err := s.ClaimBatch(10); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if err := s.MarkFailedTerminal(id, 8, "gave up"); err != nil {
		t.Fatalf("MarkFailedTerminal: %v", err)
	}

	if err := s.Retry(id); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	claimed, err := s.ClaimBatch(10)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != id {
		t.Fatalf("expected retried job to be claimable, got %+v", claimed)
	}
}

func TestRollupStatusForUser(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)

	if status, err := s.RollupStatusForUser(1); err != nil || status != "none" {
		t.Fatalf("expected none for a user with no jobs, got %q (err %v)", status, err)
	}

	id, _ := s.Enqueue(1, 10, OpAssign, Payload{})
	if _, err := s.ClaimBatch(10); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if status, err := s.RollupStatusForUser(1); err != nil || status != "syncing" {
		t.Fatalf("expected syncing while in_progress, got %q (err %v)", status, err)
	}

	if err := s.MarkSuccess(id); err != nil {
		t.Fatalf("MarkSuccess: %v", err)
	}
	if status, err := s.RollupStatusForUser(1); err != nil || status != "synced" {
		t.Fatalf("expected synced, got %q (err %v)", status, err)
	}

	// A later job on a *different* inbound fails — rollup should flip to error
	// even though the first inbound's latest job is still success.
	id2, _ := s.Enqueue(1, 11, OpAssign, Payload{})
	if _, err := s.ClaimBatch(10); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if err := s.MarkFailedTerminal(id2, 8, "boom"); err != nil {
		t.Fatalf("MarkFailedTerminal: %v", err)
	}
	if status, err := s.RollupStatusForUser(1); err != nil || status != "error" {
		t.Fatalf("expected error, got %q (err %v)", status, err)
	}

	// A superseding successful job on inbound 10 shouldn't matter for
	// inbound 11's still-failed latest job.
	id3, _ := s.Enqueue(1, 10, OpUpdate, Payload{})
	if _, err := s.ClaimBatch(10); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if err := s.MarkSuccess(id3); err != nil {
		t.Fatalf("MarkSuccess: %v", err)
	}
	if status, err := s.RollupStatusForUser(1); err != nil || status != "error" {
		t.Fatalf("expected still error due to inbound 11, got %q (err %v)", status, err)
	}
}

func TestPrune_RemovesOldSuccessOnly(t *testing.T) {
	db := openTestDB(t)
	s := NewStore(db)

	idOld, _ := s.Enqueue(1, 10, OpAssign, Payload{})
	idRecent, _ := s.Enqueue(1, 11, OpAssign, Payload{})
	idFailed, _ := s.Enqueue(1, 12, OpAssign, Payload{})

	if _, err := s.ClaimBatch(10); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if err := s.MarkSuccess(idOld); err != nil {
		t.Fatalf("MarkSuccess: %v", err)
	}
	if err := s.MarkSuccess(idRecent); err != nil {
		t.Fatalf("MarkSuccess: %v", err)
	}
	if err := s.MarkFailedTerminal(idFailed, 8, "boom"); err != nil {
		t.Fatalf("MarkFailedTerminal: %v", err)
	}

	// Backdate idOld's updated_at so it looks old enough to prune.
	if _, err := db.Exec(`UPDATE sync_jobs SET updated_at = 1 WHERE id = ?`, idOld); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	if err := s.Prune(1000); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	remaining, err := s.ListForUser(1, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	ids := map[int64]bool{}
	for _, j := range remaining {
		ids[j.ID] = true
	}
	if ids[idOld] {
		t.Fatal("expected old success job to be pruned")
	}
	if !ids[idRecent] {
		t.Fatal("expected recent success job to survive")
	}
	if !ids[idFailed] {
		t.Fatal("expected failed job to survive prune regardless of age")
	}
}
