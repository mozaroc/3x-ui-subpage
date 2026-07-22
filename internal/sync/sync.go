// Package sync is the outbox/audit-log layer between internal/users (the
// subscription-service's canonical user data) and the 3x-ui panel. Every
// admin mutation that needs to reach the panel enqueues a Job here; a
// background Worker (worker.go) drains pending jobs asynchronously, with
// retries/backoff, and keeps every attempt (success or failure) as a row for
// the admin UI's synchronization status view.
//
// A Job snapshots everything the Worker needs to perform its op (email,
// subId, credentials, limits) at enqueue time, rather than looking the user
// back up when it runs — so a job started before a user is edited or
// deleted still completes correctly, and the log entry remains meaningful
// even after the user it was for no longer exists.
package sync

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Op identifies which xui.Client write method a Job maps to.
const (
	OpAssign       = "assign"        // addClient
	OpUpdate       = "update"        // updateClient
	OpUnassign     = "unassign"      // delClient
	OpResetTraffic = "reset_traffic" // resetClientTraffic
)

// Status is a Job's lifecycle state.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusSuccess    = "success"
	StatusFailed     = "failed"
)

// ErrNotFound is returned when a job id doesn't exist.
var ErrNotFound = errors.New("sync: not found")

// Payload snapshots the client fields a Job needs to push to the panel,
// independent of the users table's state at the time the job runs.
type Payload struct {
	Email    string `json:"email"`
	SubID    string `json:"subId"`
	UUID     string `json:"uuid"`
	Password string `json:"password"`
	Method   string `json:"method"`
	Flow     string `json:"flow"`
	Enable   bool   `json:"enable"`
	TotalGB  int64  `json:"totalGB"`
	ExpiryMs int64  `json:"expiryMs"`
}

// Job is one sync_jobs row.
type Job struct {
	ID            int64
	UserID        int64
	InboundID     int
	Op            string
	Status        string
	Attempts      int
	LastError     string
	NextAttemptAt int64
	Payload       Payload
	CreatedAt     int64
	UpdatedAt     int64
}

// Store wraps the "sync_jobs" table.
type Store struct {
	db *sql.DB
}

// NewStore builds a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Enqueue inserts a new pending job and returns its id.
func (s *Store) Enqueue(userID int64, inboundID int, op string, payload Payload) (int64, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("sync: marshal payload: %w", err)
	}
	now := time.Now().UnixNano()
	res, err := s.db.Exec(`
		INSERT INTO sync_jobs (user_id, inbound_id, op, status, attempts, last_error, next_attempt_at, payload, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, '', ?, ?, ?, ?)`,
		userID, inboundID, op, StatusPending, now, string(b), now, now)
	if err != nil {
		return 0, fmt.Errorf("sync: enqueue: %w", err)
	}
	return res.LastInsertId()
}

// ClaimBatch atomically selects up to limit pending-and-due jobs and marks
// them in_progress, so a slow worker tick and a fast one never double-claim
// the same row.
func (s *Store) ClaimBatch(limit int) ([]Job, error) {
	now := time.Now().UnixNano()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("sync: claim begin tx: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT id, user_id, inbound_id, op, status, attempts, last_error, next_attempt_at, payload, created_at, updated_at
		FROM sync_jobs WHERE status = ? AND next_attempt_at <= ? ORDER BY id LIMIT ?`, StatusPending, now, limit)
	if err != nil {
		return nil, fmt.Errorf("sync: claim query: %w", err)
	}
	jobs, err := scanJobs(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}

	for i := range jobs {
		if _, err := tx.Exec(`UPDATE sync_jobs SET status = ?, updated_at = ? WHERE id = ?`, StatusInProgress, time.Now().UnixNano(), jobs[i].ID); err != nil {
			return nil, fmt.Errorf("sync: claim update %d: %w", jobs[i].ID, err)
		}
		jobs[i].Status = StatusInProgress
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sync: claim commit: %w", err)
	}
	return jobs, nil
}

// MarkSuccess marks a job as successfully completed.
func (s *Store) MarkSuccess(id int64) error {
	_, err := s.db.Exec(`UPDATE sync_jobs SET status = ?, last_error = '', updated_at = ? WHERE id = ?`,
		StatusSuccess, time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("sync: mark success %d: %w", id, err)
	}
	return nil
}

// MarkRetry records a failed attempt and re-queues the job for nextAttemptAt.
func (s *Store) MarkRetry(id int64, attempts int, lastErr string, nextAttemptAt int64) error {
	_, err := s.db.Exec(`
		UPDATE sync_jobs SET status = ?, attempts = ?, last_error = ?, next_attempt_at = ?, updated_at = ?
		WHERE id = ?`, StatusPending, attempts, lastErr, nextAttemptAt, time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("sync: mark retry %d: %w", id, err)
	}
	return nil
}

// MarkFailedTerminal marks a job as permanently failed after exhausting
// retries. An admin can re-enqueue manually (Retry) to try again.
func (s *Store) MarkFailedTerminal(id int64, attempts int, lastErr string) error {
	_, err := s.db.Exec(`
		UPDATE sync_jobs SET status = ?, attempts = ?, last_error = ?, updated_at = ?
		WHERE id = ?`, StatusFailed, attempts, lastErr, time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("sync: mark failed %d: %w", id, err)
	}
	return nil
}

// Retry re-queues a failed job for immediate reprocessing (admin-triggered
// manual retry from the sync status UI).
func (s *Store) Retry(id int64) error {
	res, err := s.db.Exec(`
		UPDATE sync_jobs SET status = ?, next_attempt_at = 0, updated_at = ? WHERE id = ? AND status = ?`,
		StatusPending, time.Now().UnixNano(), id, StatusFailed)
	if err != nil {
		return fmt.Errorf("sync: retry %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sync: retry rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListForUser returns the most recent jobs for a user, newest first.
func (s *Store) ListForUser(userID int64, limit int) ([]Job, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, inbound_id, op, status, attempts, last_error, next_attempt_at, payload, created_at, updated_at
		FROM sync_jobs WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("sync: list for user %d: %w", userID, err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

// ListRecent returns the most recent jobs across every user, newest first —
// backs the admin-wide /admin/sync history view.
func (s *Store) ListRecent(limit int) ([]Job, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, inbound_id, op, status, attempts, last_error, next_attempt_at, payload, created_at, updated_at
		FROM sync_jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("sync: list recent: %w", err)
	}
	defer rows.Close()
	return scanJobs(rows)
}

// RollupStatusForUser summarizes a user's sync state from the latest job
// per assigned inbound: "error" if any latest job failed, "syncing" if any
// is pending/in_progress, "synced" if every latest job succeeded, "none" if
// the user has no sync history yet (e.g. no inbounds ever assigned).
func (s *Store) RollupStatusForUser(userID int64) (string, error) {
	rows, err := s.db.Query(`
		SELECT status FROM (
			SELECT status, ROW_NUMBER() OVER (PARTITION BY inbound_id ORDER BY id DESC) AS rn
			FROM sync_jobs WHERE user_id = ?
		) WHERE rn = 1`, userID)
	if err != nil {
		return "", fmt.Errorf("sync: rollup status %d: %w", userID, err)
	}
	defer rows.Close()

	var any bool
	var anyFailed, anyPending bool
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return "", fmt.Errorf("sync: scan rollup: %w", err)
		}
		any = true
		switch status {
		case StatusFailed:
			anyFailed = true
		case StatusPending, StatusInProgress:
			anyPending = true
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("sync: iterate rollup: %w", err)
	}

	switch {
	case !any:
		return "none", nil
	case anyFailed:
		return "error", nil
	case anyPending:
		return "syncing", nil
	default:
		return "synced", nil
	}
}

// Prune deletes successful jobs older than cutoff (a UnixNano timestamp) so
// the audit log doesn't grow unbounded. Failed/pending/in_progress jobs are
// always kept regardless of age.
func (s *Store) Prune(cutoff int64) error {
	if _, err := s.db.Exec(`DELETE FROM sync_jobs WHERE status = ? AND updated_at < ?`, StatusSuccess, cutoff); err != nil {
		return fmt.Errorf("sync: prune: %w", err)
	}
	return nil
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	out := make([]Job, 0)
	for rows.Next() {
		var j Job
		var payloadJSON string
		if err := rows.Scan(&j.ID, &j.UserID, &j.InboundID, &j.Op, &j.Status, &j.Attempts, &j.LastError, &j.NextAttemptAt, &payloadJSON, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, fmt.Errorf("sync: scan job: %w", err)
		}
		if err := json.Unmarshal([]byte(payloadJSON), &j.Payload); err != nil {
			return nil, fmt.Errorf("sync: decode payload for job %d: %w", j.ID, err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
