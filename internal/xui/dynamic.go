package xui

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"

	"github.com/irazin/3x-ui-subpage/internal/config"
)

// DynamicClient wraps a *Client that's rebuilt whenever the "xui" settings
// row's updated_at advances, so editing Settings -> xui through the admin
// UI takes effect on the very next call — the same hot-reload convention
// every other admin-editable surface in this project already follows (app
// catalog, themes, templates, assignments). Without this, a *Client built
// once at startup from whatever "xui" settings existed at that moment would
// keep using stale/placeholder credentials forever, silently failing every
// sync job and inbound listing until the process was restarted — the exact
// bug this type exists to close.
type DynamicClient struct {
	db     *sql.DB
	logger *slog.Logger

	mu       sync.RWMutex
	current  *Client
	loadedAt int64
}

// NewDynamic builds a DynamicClient backed by db. Nothing is loaded until
// the first call, so a missing/invalid "xui" settings row fails at request
// time with a clear error rather than at startup.
func NewDynamic(db *sql.DB, logger *slog.Logger) *DynamicClient {
	return &DynamicClient{db: db, logger: logger}
}

// client returns the current *Client, rebuilding it first if the "xui"
// settings row has changed since it was last built.
func (d *DynamicClient) client(ctx context.Context) (*Client, error) {
	var updatedAt int64
	err := d.db.QueryRowContext(ctx, `SELECT updated_at FROM settings WHERE key = 'xui'`).Scan(&updatedAt)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("xui: query settings updated_at: %w", err)
	}

	d.mu.RLock()
	cur, curLoadedAt := d.current, d.loadedAt
	d.mu.RUnlock()
	if cur != nil && updatedAt == curLoadedAt {
		return cur, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	// Re-check: another goroutine may have already refreshed while we
	// waited for the write lock.
	if d.current != nil && updatedAt == d.loadedAt {
		return d.current, nil
	}

	cfg, err := config.LoadFromDB(d.db)
	if err != nil {
		return nil, fmt.Errorf("xui: reload settings: %w", err)
	}

	c, err := New(
		cfg.XUI.BaseURL, cfg.XUI.APIKey,
		cfg.XUI.Timeout, cfg.XUI.Retry.MaxAttempts, cfg.XUI.Retry.Backoff,
		WithLogger(d.logger),
		WithInsecureSkipVerify(cfg.XUI.InsecureSkipVerify),
	)
	if err != nil {
		return nil, fmt.Errorf("xui: build client: %w", err)
	}

	d.logger.Info("xui: reloaded client from updated settings", "base_url", cfg.XUI.BaseURL)
	d.current = c
	d.loadedAt = updatedAt
	return c, nil
}

func (d *DynamicClient) ListInbounds(ctx context.Context) ([]Inbound, error) {
	c, err := d.client(ctx)
	if err != nil {
		return nil, err
	}
	return c.ListInbounds(ctx)
}

func (d *DynamicClient) GetClient(ctx context.Context, email string) (ManagedClient, bool, error) {
	c, err := d.client(ctx)
	if err != nil {
		return ManagedClient{}, false, err
	}
	return c.GetClient(ctx, email)
}

func (d *DynamicClient) AddClient(ctx context.Context, client ManagedClient, inboundIDs []int) error {
	c, err := d.client(ctx)
	if err != nil {
		return err
	}
	return c.AddClient(ctx, client, inboundIDs)
}

func (d *DynamicClient) AttachClient(ctx context.Context, email string, inboundIDs []int) error {
	c, err := d.client(ctx)
	if err != nil {
		return err
	}
	return c.AttachClient(ctx, email, inboundIDs)
}

func (d *DynamicClient) UpdateClient(ctx context.Context, client ManagedClient) error {
	c, err := d.client(ctx)
	if err != nil {
		return err
	}
	return c.UpdateClient(ctx, client)
}

func (d *DynamicClient) DetachClient(ctx context.Context, email string, inboundIDs []int) error {
	c, err := d.client(ctx)
	if err != nil {
		return err
	}
	return c.DetachClient(ctx, email, inboundIDs)
}

func (d *DynamicClient) DeleteClient(ctx context.Context, email string) error {
	c, err := d.client(ctx)
	if err != nil {
		return err
	}
	return c.DeleteClient(ctx, email)
}

func (d *DynamicClient) ResetClientTraffic(ctx context.Context, email string) error {
	c, err := d.client(ctx)
	if err != nil {
		return err
	}
	return c.ResetClientTraffic(ctx, email)
}
