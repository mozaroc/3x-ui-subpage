// Package xui implements a client for the 3x-ui panel's official REST API.
// It handles cookie-based session auth with automatic re-login, retries with
// backoff, and decodes the panel's inbound list into domain types.
package xui

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client talks to a single 3x-ui panel instance.
type Client struct {
	baseURL  string
	username string
	password string

	httpClient *http.Client
	logger     *slog.Logger

	maxAttempts int
	backoff     time.Duration

	mu       sync.Mutex // guards loggedIn; serializes re-login attempts
	loggedIn bool
}

// Option configures a Client.
type Option func(*Client)

// WithLogger sets the structured logger used for request/auth diagnostics.
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
// "https://panel.example.com:2053/basepath"). username/password are the
// panel admin credentials. timeout bounds every individual HTTP call.
func New(baseURL, username, password string, timeout time.Duration, maxAttempts int, backoff time.Duration, opts ...Option) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("xui: create cookie jar: %w", err)
	}

	c := &Client{
		baseURL:  strings.TrimSuffix(baseURL, "/"),
		username: username,
		password: password,
		httpClient: &http.Client{
			Jar:     jar,
			Timeout: timeout,
		},
		logger:      slog.Default(),
		maxAttempts: maxAttempts,
		backoff:     backoff,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// login authenticates against POST {base}/login and stores the resulting
// session cookie in the client's cookie jar.
func (c *Client) login(ctx context.Context) error {
	form := url.Values{
		"username": {c.username},
		"password": {c.password},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("xui: build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("xui: login request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("xui: login failed: status %d: %s", resp.StatusCode, body)
	}

	var lr apiResponse[json.RawMessage]
	if err := json.Unmarshal(body, &lr); err != nil {
		return fmt.Errorf("xui: decode login response: %w", err)
	}
	if !lr.Success {
		return fmt.Errorf("xui: login rejected: %s", lr.Msg)
	}

	c.logger.Info("xui: logged in", "base_url", c.baseURL)
	return nil
}

// ensureLoggedIn performs an initial login exactly once (subsequent calls
// are no-ops); actual re-login on session expiry happens reactively in
// doWithRetry when a request comes back 401.
func (c *Client) ensureLoggedIn(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.loggedIn {
		return nil
	}
	if err := c.login(ctx); err != nil {
		return err
	}
	c.loggedIn = true
	return nil
}

// relogin forces a fresh session, guarded so concurrent callers collapse
// into a single login request.
func (c *Client) relogin(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.login(ctx); err != nil {
		c.loggedIn = false
		return err
	}
	c.loggedIn = true
	return nil
}

// doJSON performs an authenticated GET against path, retrying on transient
// network errors, 5xx, and — after one forced re-login — 401 responses.
// The response's "obj" field is decoded into out.
func (c *Client) doJSON(ctx context.Context, method, path string, out any) error {
	if err := c.ensureLoggedIn(ctx); err != nil {
		return err
	}

	var lastErr error
	relogged := false

	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
		if err != nil {
			return fmt.Errorf("xui: build request %s: %w", path, err)
		}

		start := time.Now()
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("xui: request %s: %w", path, err)
			c.logger.Debug("xui: request error", "path", path, "attempt", attempt, "err", err)
			c.sleepBackoff(ctx, attempt)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		c.logger.Debug("xui: request", "method", method, "path", path, "status", resp.StatusCode, "duration_ms", time.Since(start).Milliseconds())

		if readErr != nil {
			lastErr = fmt.Errorf("xui: read body %s: %w", path, readErr)
			c.sleepBackoff(ctx, attempt)
			continue
		}

		switch {
		case resp.StatusCode == http.StatusUnauthorized && !relogged:
			relogged = true
			if err := c.relogin(ctx); err != nil {
				return fmt.Errorf("xui: re-login after 401: %w", err)
			}
			continue // retry same attempt count, don't burn a backoff slot
		case resp.StatusCode >= 500:
			lastErr = fmt.Errorf("xui: %s returned %d: %s", path, resp.StatusCode, body)
			c.sleepBackoff(ctx, attempt)
			continue
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("xui: %s returned %d: %s", path, resp.StatusCode, body)
		}

		var envelope apiResponse[json.RawMessage]
		if err := json.Unmarshal(body, &envelope); err != nil {
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
