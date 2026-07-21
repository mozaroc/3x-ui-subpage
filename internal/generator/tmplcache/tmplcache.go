// Package tmplcache provides hot-reloaded text/template loading from the
// shared "templates" table: each row is keyed by (format, profile,
// protocol), and a row missing for a requested profile falls back to the
// "default" profile — the mechanism behind per-user template assignment.
// Templates are cached per resolved key and only re-parsed when that row's
// updated_at advances, so a busy subscription endpoint doesn't reparse on
// every request.
package tmplcache

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"text/template"
)

// DefaultProfile is the profile every fallback resolves to.
const DefaultProfile = "default"

// ParseFunc parses a template row's content into a *template.Template. name
// is a unique association name for this template within its set (see
// text/template's New/Parse pattern) — callers typically pass something
// like "<format>/<profile>/<protocol>".
type ParseFunc func(name, content string) (*template.Template, error)

type entry struct {
	tmpl      *template.Template
	updatedAt int64
}

// Cache loads and caches templates from the "templates" table for one
// format (e.g. "clash", "xray_json", or one of the xray_link protocols).
type Cache struct {
	db     *sql.DB
	format string
	parse  ParseFunc

	mu      sync.Mutex
	entries map[string]*entry
}

// New builds a Cache for the given format, using db as the template row
// source and parse to compile a row's content into a *template.Template.
func New(db *sql.DB, format string, parse ParseFunc) *Cache {
	return &Cache{db: db, format: format, parse: parse, entries: make(map[string]*entry)}
}

// Get returns the compiled template for (format, profile, protocol),
// falling back to the "default" profile if no row exists for profile.
// protocol is "" for formats that don't have per-protocol templates
// (clash, mihomo, xray_json).
func (c *Cache) Get(profile, protocol string) (*template.Template, error) {
	content, updatedAt, resolvedProfile, err := c.fetch(profile, protocol)
	if err != nil {
		return nil, err
	}

	key := resolvedProfile + "|" + protocol

	c.mu.Lock()
	defer c.mu.Unlock()

	if e, ok := c.entries[key]; ok && e.updatedAt == updatedAt {
		return e.tmpl, nil
	}

	tmpl, err := c.parse(c.format+"/"+key, content)
	if err != nil {
		return nil, fmt.Errorf("tmplcache: parse %s/%s: %w", c.format, key, err)
	}

	c.entries[key] = &entry{tmpl: tmpl, updatedAt: updatedAt}
	return tmpl, nil
}

// fetch queries the requested (format, profile, protocol) row, falling back
// to the "default" profile if it doesn't exist. Returns the resolved
// profile name actually used, for cache-key purposes.
func (c *Cache) fetch(profile, protocol string) (content string, updatedAt int64, resolvedProfile string, err error) {
	content, updatedAt, err = c.queryRow(profile, protocol)
	if err == nil {
		return content, updatedAt, profile, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", err
	}
	if profile == DefaultProfile {
		return "", 0, "", fmt.Errorf("tmplcache: no %s template for profile %q (and it is already the default)", c.format, profile)
	}

	content, updatedAt, err = c.queryRow(DefaultProfile, protocol)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, "", fmt.Errorf("tmplcache: no %s template for profile %q or fallback %q", c.format, profile, DefaultProfile)
		}
		return "", 0, "", err
	}
	return content, updatedAt, DefaultProfile, nil
}

func (c *Cache) queryRow(profile, protocol string) (content string, updatedAt int64, err error) {
	err = c.db.QueryRow(
		`SELECT content, updated_at FROM templates WHERE format = ? AND profile = ? AND protocol = ?`,
		c.format, profile, protocol,
	).Scan(&content, &updatedAt)
	if err != nil {
		return "", 0, fmt.Errorf("query template (%s, %s, %s): %w", c.format, profile, protocol, err)
	}
	return content, updatedAt, nil
}
