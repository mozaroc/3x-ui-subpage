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
	GetClient(ctx context.Context, email string) (xui.ManagedClient, bool, error)
	AddClient(ctx context.Context, client xui.ManagedClient, inboundIDs []int) error
	AttachClient(ctx context.Context, email string, inboundIDs []int) error
	UpdateClient(ctx context.Context, client xui.ManagedClient) error
	DetachClient(ctx context.Context, email string, inboundIDs []int) error
	DeleteClient(ctx context.Context, email string) error
	ResetClientTraffic(ctx context.Context, email string) error
}

const (
	defaultPollInterval = 3 * time.Second
	defaultBatchSize    = 20
	defaultMaxAttempts  = 8
	maxBackoff          = 60 * time.Second
	pruneAfter          = 7 * 24 * time.Hour

	// staleInProgressAfter bounds how long a job may sit in_progress before
	// ReapStale assumes the worker that claimed it died (or crashed) mid-job
	// and re-queues it. Comfortably longer than one dispatch can legitimately
	// take: doJSONBody's own worst case is maxAttempts retries each waiting
	// up to its backoff, still well under a minute for typical xui timeouts.
	staleInProgressAfter = 5 * time.Minute
)

// Worker drains pending sync_jobs, pushing each to the panel via Writer,
// with retry/backoff and a self-healing conflict check on assign.
type Worker struct {
	jobs       *Store
	writer     Writer
	invalidate func() // called after every successful write
	logger     *slog.Logger

	pollInterval time.Duration
	batchSize    int
	maxAttempts  int
}

// NewWorker builds a Worker. invalidate may be nil (disables cache
// invalidation) but is expected to be set in production —
// cmd/subscription-service wires it to the same xui.CachedLister the rest
// of the service already uses.
func NewWorker(jobs *Store, writer Writer, invalidate func(), logger *slog.Logger) *Worker {
	return &Worker{
		jobs:         jobs,
		writer:       writer,
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
	if n, err := w.jobs.ReapStale(time.Now().Add(-staleInProgressAfter).UnixNano()); err != nil {
		w.logger.Warn("sync: reap stale failed", "err", err)
	} else if n > 0 {
		w.logger.Warn("sync: reclaimed stale in_progress jobs", "count", n)
	}

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

// dispatch maps a job to the panel's client-management API
// (/panel/api/clients/*, confirmed against a live 3x-ui 3.5.0 instance) —
// every op is keyed by the client's email, the one universal identifier
// that API uses throughout.
func (w *Worker) dispatch(ctx context.Context, j Job) error {
	switch j.Op {
	case OpAssign:
		return w.doAssign(ctx, j)
	case OpUpdate:
		return w.writer.UpdateClient(ctx, toManagedClient(j.Payload))
	case OpUnassign:
		return w.writer.DetachClient(ctx, j.Payload.Email, []int{j.InboundID})
	case OpDelete:
		return w.writer.DeleteClient(ctx, j.Payload.Email)
	case OpResetTraffic:
		return w.writer.ResetClientTraffic(ctx, j.Payload.Email)
	default:
		return fmt.Errorf("sync: unknown op %q", j.Op)
	}
}

// doAssign attaches the client to the job's inbound, creating it first if
// this is its first-ever assignment. It checks whether the client already
// exists on the panel (drift from a previous partial failure, or the same
// user being assigned a second/third inbound) — in which case it attaches
// instead of erroring out on a duplicate-create.
func (w *Worker) doAssign(ctx context.Context, j Job) error {
	_, exists, err := w.writer.GetClient(ctx, j.Payload.Email)
	if err != nil {
		return fmt.Errorf("check existing client: %w", err)
	}
	if exists {
		return w.writer.AttachClient(ctx, j.Payload.Email, []int{j.InboundID})
	}
	return w.writer.AddClient(ctx, toManagedClient(j.Payload), []int{j.InboundID})
}

func toManagedClient(p Payload) xui.ManagedClient {
	return xui.ManagedClient{
		Email:      p.Email,
		SubID:      p.SubID,
		UUID:       p.UUID,
		Password:   p.Password,
		Flow:       p.Flow,
		Enable:     p.Enable,
		TotalGB:    p.TotalGB,
		ExpiryTime: p.ExpiryMs,
	}
}
