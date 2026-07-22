package sync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/irazin/3x-ui-subpage/internal/xui"
)

// Writer is the subset of *xui.Client the Worker needs — kept as an
// interface so tests use a fake instead of an HTTP server.
type Writer interface {
	AddClient(ctx context.Context, inboundID int, client xui.ClientPayload) error
	UpdateClient(ctx context.Context, inboundID int, client xui.ClientPayload) error
	DeleteClient(ctx context.Context, inboundID int, identifier string) error
	ResetClientTraffic(ctx context.Context, inboundID int, email string) error
}

const (
	defaultPollInterval = 3 * time.Second
	defaultBatchSize    = 20
	defaultMaxAttempts  = 8
	maxBackoff          = 60 * time.Second
	pruneAfter          = 7 * 24 * time.Hour
)

// Worker drains pending sync_jobs, pushing each to the panel via Writer,
// with retry/backoff and a self-healing conflict check on assign.
type Worker struct {
	jobs       *Store
	writer     Writer
	lister     xui.InboundLister // used for the assign-conflict pre-check; may be nil
	invalidate func()            // called after every successful write
	logger     *slog.Logger

	pollInterval time.Duration
	batchSize    int
	maxAttempts  int
}

// NewWorker builds a Worker. lister and invalidate may be nil (disables the
// conflict pre-check / cache invalidation, respectively) but are expected to
// be set in production — cmd/subscription-service wires both to the same
// xui.CachedLister the rest of the service already uses.
func NewWorker(jobs *Store, writer Writer, lister xui.InboundLister, invalidate func(), logger *slog.Logger) *Worker {
	return &Worker{
		jobs:         jobs,
		writer:       writer,
		lister:       lister,
		invalidate:   invalidate,
		logger:       logger,
		pollInterval: defaultPollInterval,
		batchSize:    defaultBatchSize,
		maxAttempts:  defaultMaxAttempts,
	}
}

// Run ticks every pollInterval until ctx is cancelled, claiming and
// processing due jobs each tick. Intended to be started as `go w.Run(ctx)`.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.Tick(ctx)
		}
	}
}

// Tick claims and processes one batch of due jobs, then prunes old
// successful jobs. Exported so tests can drive it deterministically instead
// of waiting on the ticker.
func (w *Worker) Tick(ctx context.Context) {
	jobs, err := w.jobs.ClaimBatch(w.batchSize)
	if err != nil {
		w.logger.Error("sync: claim batch failed", "err", err)
		return
	}
	for _, j := range jobs {
		w.process(ctx, j)
	}

	if err := w.jobs.Prune(time.Now().Add(-pruneAfter).UnixNano()); err != nil {
		w.logger.Warn("sync: prune failed", "err", err)
	}
}

func (w *Worker) process(ctx context.Context, j Job) {
	err := w.dispatch(ctx, j)

	if err == nil {
		if err := w.jobs.MarkSuccess(j.ID); err != nil {
			w.logger.Error("sync: mark success failed", "job_id", j.ID, "err", err)
		}
		if w.invalidate != nil {
			w.invalidate()
		}
		w.logger.Info("sync: job succeeded", "job_id", j.ID, "op", j.Op, "user_id", j.UserID, "inbound_id", j.InboundID)
		return
	}

	attempts := j.Attempts + 1
	w.logger.Warn("sync: job failed", "job_id", j.ID, "op", j.Op, "user_id", j.UserID, "inbound_id", j.InboundID, "attempt", attempts, "err", err)

	if attempts >= w.maxAttempts {
		if err := w.jobs.MarkFailedTerminal(j.ID, attempts, err.Error()); err != nil {
			w.logger.Error("sync: mark failed failed", "job_id", j.ID, "err", err)
		}
		return
	}

	backoff := time.Duration(attempts*attempts) * time.Second
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	next := time.Now().Add(backoff).UnixNano()
	if err := w.jobs.MarkRetry(j.ID, attempts, err.Error(), next); err != nil {
		w.logger.Error("sync: mark retry failed", "job_id", j.ID, "err", err)
	}
}

func (w *Worker) dispatch(ctx context.Context, j Job) error {
	switch j.Op {
	case OpAssign:
		return w.doAssign(ctx, j)
	case OpUpdate:
		return w.writer.UpdateClient(ctx, j.InboundID, toClientPayload(j.Payload))
	case OpUnassign:
		return w.writer.DeleteClient(ctx, j.InboundID, j.Payload.Identifier())
	case OpResetTraffic:
		return w.writer.ResetClientTraffic(ctx, j.InboundID, j.Payload.Email)
	default:
		return fmt.Errorf("sync: unknown op %q", j.Op)
	}
}

// doAssign creates the client on the panel, unless one with the same email
// already exists in that inbound (drift from a previous partial failure, or
// a manual edit on the panel side) — in which case it self-heals by
// updating instead of erroring out on a duplicate-add.
func (w *Worker) doAssign(ctx context.Context, j Job) error {
	payload := toClientPayload(j.Payload)

	if w.lister != nil {
		if inbounds, err := w.lister.ListInbounds(ctx); err == nil {
			if _, exists, err := xui.FindClient(inbounds, j.InboundID, j.Payload.Email); err == nil && exists {
				w.logger.Warn("sync: client already exists on panel, updating instead of adding",
					"user_id", j.UserID, "inbound_id", j.InboundID, "email", j.Payload.Email)
				return w.writer.UpdateClient(ctx, j.InboundID, payload)
			}
		}
	}

	return w.writer.AddClient(ctx, j.InboundID, payload)
}

func toClientPayload(p Payload) xui.ClientPayload {
	return xui.ClientPayload{
		ID:         p.UUID,
		Password:   p.Password,
		Method:     p.Method,
		Flow:       p.Flow,
		Email:      p.Email,
		SubID:      p.SubID,
		Enable:     p.Enable,
		TotalGB:    p.TotalGB,
		ExpiryTime: p.ExpiryMs,
	}
}
