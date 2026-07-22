// Package xui implements a client for the 3x-ui panel's REST API.
// Authenticates with a static API token (Settings → Security → API Token in
// the panel's own UI), sent as "Authorization: Bearer <token>" on every
// request — no session/cookie login flow. Retries with backoff on transient
// errors, and decodes the panel's inbound list into domain types.
package xui

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a single 3x-ui panel instance.
type Client struct {
	baseURL string
	apiKey  string

	httpClient *http.Client
	logger     *slog.Logger

	maxAttempts int
	backoff     time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithLogger sets the structured logger used for request diagnostics.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) { c.logger = l }
}

// WithInsecureSkipVerify disables TLS certificate verification — only for
// panels behind self-signed certs the operator has already vetted.
func WithInsecureSkipVerify(skip bool) Option {
	return func(c *Client) {
		if skip {
			c.httpClient.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // operator opt-in
			}
		}
	}
}

// New builds a Client for the given panel base URL (e.g.
// "https://panel.example.com:2053/basepath"). apiKey is the panel's API
// token (Settings → Security → API Token). timeout bounds every individual
// HTTP call.
func New(baseURL, apiKey string, timeout time.Duration, maxAttempts int, backoff time.Duration, opts ...Option) (*Client, error) {
	c := &Client{
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		apiKey:      apiKey,
		httpClient:  &http.Client{Timeout: timeout},
		logger:      slog.Default(),
		maxAttempts: maxAttempts,
		backoff:     backoff,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// doJSON performs a bearer-authenticated GET against path, retrying on
// transient network errors and 5xx responses. The response's "obj" field is
// decoded into out.
func (c *Client) doJSON(ctx context.Context, method, path string, out any) error {
	return c.doJSONBody(ctx, method, path, nil, out)
}

// doJSONBody performs a bearer-authenticated request carrying a JSON-encoded
// body (nil for none), retrying on transient network errors and 5xx
// responses. A 401/403 is treated as a terminal auth failure — retrying
// with the same key won't help. The response's "obj" field is decoded into
// out if non-nil.
func (c *Client) doJSONBody(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("xui: marshal request body %s: %w", path, err)
		}
		payload = b
	}

	var lastErr error

	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		var bodyReader io.Reader
		if payload != nil {
			bodyReader = strings.NewReader(string(payload))
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
		if err != nil {
			return fmt.Errorf("xui: build request %s: %w", path, err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		start := time.Now()
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("xui: request %s: %w", path, err)
			c.logger.Debug("xui: request error", "path", path, "attempt", attempt, "err", err)
			c.sleepBackoff(ctx, attempt)
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		c.logger.Debug("xui: request", "method", method, "path", path, "status", resp.StatusCode, "duration_ms", time.Since(start).Milliseconds())

		if readErr != nil {
			lastErr = fmt.Errorf("xui: read body %s: %w", path, readErr)
			c.sleepBackoff(ctx, attempt)
			continue
		}

		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return fmt.Errorf("xui: %s rejected the API key (status %d): %s", path, resp.StatusCode, respBody)
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("xui: %s returned %d: %s", path, resp.StatusCode, respBody)
			c.sleepBackoff(ctx, attempt)
			continue
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("xui: %s returned %d: %s", path, resp.StatusCode, respBody)
		}

		var envelope apiResponse[json.RawMessage]
		if err := json.Unmarshal(respBody, &envelope); err != nil {
			return fmt.Errorf("xui: decode envelope %s: %w", path, err)
		}
		if !envelope.Success {
			return fmt.Errorf("xui: %s reported failure: %s", path, envelope.Msg)
		}
		if out != nil {
			if err := json.Unmarshal(envelope.Obj, out); err != nil {
				return fmt.Errorf("xui: decode obj %s: %w", path, err)
			}
		}
		return nil
	}

	return fmt.Errorf("xui: %s failed after %d attempts: %w", path, c.maxAttempts, lastErr)
}

func (c *Client) sleepBackoff(ctx context.Context, attempt int) {
	delay := c.backoff * time.Duration(attempt)
	select {
	case <-time.After(delay):
	case <-ctx.Done():
	}
}

// ListInbounds fetches every inbound configured on the panel, including
// embedded client lists and live traffic stats.
func (c *Client) ListInbounds(ctx context.Context) ([]Inbound, error) {
	var inbounds []Inbound
	if err := c.doJSON(ctx, http.MethodGet, "/panel/api/inbounds/list", &inbounds); err != nil {
		return nil, err
	}
	return inbounds, nil
}

// ListHosts fetches every configured Host group across every inbound in one
// call (confirmed against a live 3.5.0 instance).
func (c *Client) ListHosts(ctx context.Context) ([]HostGroup, error) {
	var hosts []HostGroup
	if err := c.doJSON(ctx, http.MethodGet, "/panel/api/hosts/list", &hosts); err != nil {
		return nil, err
	}
	return hosts, nil
}

// addClientRequest is the POST body for /panel/api/clients/add.
type addClientRequest struct {
	Client     ManagedClient `json:"client"`
	InboundIDs []int         `json:"inboundIds"`
}

// AddClient creates a new client and attaches it to every listed inbound in
// one call. Per-protocol secrets (uuid, password) are generated server-side
// if left empty on client, but this project always generates them locally
// (internal/users) so every field the panel might need is already set.
func (c *Client) AddClient(ctx context.Context, client ManagedClient, inboundIDs []int) error {
	body := addClientRequest{Client: client, InboundIDs: inboundIDs}
	return c.doJSONBody(ctx, http.MethodPost, "/panel/api/clients/add", body, nil)
}

// UpdateClient overwrites the client identified by client.Email with the
// given field values. The panel treats this as a full replace, not a patch,
// and the change applies to every inbound the client is already attached to
// — there is no per-inbound client state to update separately.
func (c *Client) UpdateClient(ctx context.Context, client ManagedClient) error {
	path := "/panel/api/clients/update/" + url.PathEscape(client.Email)
	return c.doJSONBody(ctx, http.MethodPost, path, client, nil)
}

// attachDetachRequest is the POST body for the attach/detach endpoints.
type attachDetachRequest struct {
	InboundIDs []int `json:"inboundIds"`
}

// AttachClient attaches an already-existing client (by email) to additional
// inbounds, without touching its other fields.
func (c *Client) AttachClient(ctx context.Context, email string, inboundIDs []int) error {
	path := "/panel/api/clients/" + url.PathEscape(email) + "/attach"
	return c.doJSONBody(ctx, http.MethodPost, path, attachDetachRequest{InboundIDs: inboundIDs}, nil)
}

// DetachClient detaches a client (by email) from the given inbounds without
// deleting the client outright — though the panel deletes the client
// automatically once it has no inbounds left attached.
func (c *Client) DetachClient(ctx context.Context, email string, inboundIDs []int) error {
	path := "/panel/api/clients/" + url.PathEscape(email) + "/detach"
	return c.doJSONBody(ctx, http.MethodPost, path, attachDetachRequest{InboundIDs: inboundIDs}, nil)
}

// DeleteClient removes a client (by email) from every inbound it's attached
// to and drops its traffic record.
func (c *Client) DeleteClient(ctx context.Context, email string) error {
	path := "/panel/api/clients/del/" + url.PathEscape(email) + "?keepTraffic=0"
	return c.doJSONBody(ctx, http.MethodPost, path, nil, nil)
}

// ResetClientTraffic zeroes a client's accumulated up/down counters (by
// email) across every inbound it's attached to, and re-enables it.
func (c *Client) ResetClientTraffic(ctx context.Context, email string) error {
	path := "/panel/api/clients/resetTraffic/" + url.PathEscape(email)
	return c.doJSONBody(ctx, http.MethodPost, path, nil, nil)
}

// notFoundMsgFragment is the distinguishing substring of the panel's "record
// not found" response body for GetClient — the panel signals this as
// success:false with HTTP 200, not a distinct status code, so there is no
// cleaner way to detect it than matching the message text.
const notFoundMsgFragment = "record not found"

// GetClient fetches one client by email. Returns found=false (no error) if
// the panel reports the client doesn't exist.
func (c *Client) GetClient(ctx context.Context, email string) (client ManagedClient, found bool, err error) {
	var out struct {
		Client ManagedClient `json:"client"`
	}
	path := "/panel/api/clients/get/" + url.PathEscape(email)
	if err := c.doJSON(ctx, http.MethodGet, path, &out); err != nil {
		if strings.Contains(err.Error(), notFoundMsgFragment) {
			return ManagedClient{}, false, nil
		}
		return ManagedClient{}, false, err
	}
	return out.Client, true, nil
}
