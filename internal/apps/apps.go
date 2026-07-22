// Package apps loads the administrator-configured application catalog
// (table "applications" in the shared SQLite database) that drives the
// subscription page's app list — install/download buttons and deep links
// are rendered entirely from these rows, with no application hardcoded in
// Go. The catalog is hot-reloaded whenever any row's updated_at advances.
package apps

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned by Get for a missing application id.
var ErrNotFound = errors.New("apps: not found")

// App is one entry in the catalog.
type App struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Icon         string   `json:"icon"`
	Description  string   `json:"description"`
	Platforms    []string `json:"platforms"`
	Download     string   `json:"download"`
	Deeplink     string   `json:"deeplink"`
	Instructions string   `json:"instructions"`
	Visible      bool     `json:"visible"`
	SortOrder    int      `json:"sortOrder"`
}

// Catalog loads and caches the app catalog, reloading from the database
// whenever the newest row's updated_at advances.
type Catalog struct {
	db *sql.DB

	mu       sync.Mutex
	apps     []App
	loadedAt int64
}

// New builds a Catalog reading from the "applications" table via db. The
// table is not queried until the first List/FilterByPlatform call.
func New(db *sql.DB) *Catalog {
	return &Catalog{db: db}
}

func (c *Catalog) reloadIfNeeded() error {
	var newest sql.NullInt64
	var count int
	if err := c.db.QueryRow(`SELECT COUNT(*), MAX(updated_at) FROM applications`).Scan(&count, &newest); err != nil {
		return fmt.Errorf("apps: query max updated_at: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Compare count too, not just MAX(updated_at): deleting a row can only
	// ever leave the remaining max the same or lower, never higher, so a
	// deletion that doesn't happen to remove the single most-recently-
	// touched row would otherwise go undetected — the stale, already-
	// deleted app would keep being served until some unrelated later
	// insert/update finally raised the max past the cached value.
	if c.apps != nil && newest.Int64 == c.loadedAt && count == len(c.apps) {
		return nil
	}

	rows, err := c.db.Query(`
		SELECT id, name, icon, description, platforms, download, deeplink, instructions, visible, sort_order
		FROM applications
		ORDER BY sort_order`)
	if err != nil {
		return fmt.Errorf("apps: query applications: %w", err)
	}
	defer rows.Close()

	apps := make([]App, 0)
	for rows.Next() {
		var a App
		var platformsJSON string
		var visible int

		if err := rows.Scan(&a.ID, &a.Name, &a.Icon, &a.Description, &platformsJSON, &a.Download, &a.Deeplink, &a.Instructions, &visible, &a.SortOrder); err != nil {
			return fmt.Errorf("apps: scan row: %w", err)
		}
		if err := json.Unmarshal([]byte(platformsJSON), &a.Platforms); err != nil {
			return fmt.Errorf("apps: decode platforms for %q: %w", a.Name, err)
		}
		a.Visible = visible != 0

		apps = append(apps, a)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("apps: iterate rows: %w", err)
	}

	c.apps = apps
	c.loadedAt = newest.Int64
	return nil
}

// List returns every visible app, sorted by SortOrder, reloading from the
// database first if the catalog changed.
func (c *Catalog) List() ([]App, error) {
	if err := c.reloadIfNeeded(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]App, 0, len(c.apps))
	for _, a := range c.apps {
		if a.Visible {
			out = append(out, a)
		}
	}
	return out, nil
}

// FilterByPlatform returns every visible app whose Platforms list contains
// platform (case-insensitive), sorted by SortOrder.
func (c *Catalog) FilterByPlatform(platform string) ([]App, error) {
	all, err := c.List()
	if err != nil {
		return nil, err
	}

	out := make([]App, 0, len(all))
	for _, a := range all {
		for _, p := range a.Platforms {
			if strings.EqualFold(p, platform) {
				out = append(out, a)
				break
			}
		}
	}
	return out, nil
}

// ListAll returns every app regardless of visibility, sorted by SortOrder —
// for the admin catalog view, where hidden apps still need to be manageable.
func (c *Catalog) ListAll() ([]App, error) {
	if err := c.reloadIfNeeded(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]App, len(c.apps))
	copy(out, c.apps)
	return out, nil
}

// Get returns one app by id, regardless of visibility.
func (c *Catalog) Get(id int64) (App, error) {
	all, err := c.ListAll()
	if err != nil {
		return App{}, err
	}
	for _, a := range all {
		if a.ID == id {
			return a, nil
		}
	}
	return App{}, ErrNotFound
}

// Create inserts a new app and returns its id.
func (c *Catalog) Create(a App) (int64, error) {
	platforms, err := json.Marshal(a.Platforms)
	if err != nil {
		return 0, fmt.Errorf("apps: marshal platforms: %w", err)
	}

	visible := 0
	if a.Visible {
		visible = 1
	}

	res, err := c.db.Exec(`
		INSERT INTO applications (name, icon, description, platforms, download, deeplink, instructions, visible, sort_order, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.Icon, a.Description, string(platforms), a.Download, a.Deeplink, a.Instructions, visible, a.SortOrder, time.Now().UnixNano())
	if err != nil {
		return 0, fmt.Errorf("apps: insert: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("apps: get last insert id: %w", err)
	}
	return id, nil
}

// Update overwrites an existing app's fields by id.
func (c *Catalog) Update(id int64, a App) error {
	platforms, err := json.Marshal(a.Platforms)
	if err != nil {
		return fmt.Errorf("apps: marshal platforms: %w", err)
	}

	visible := 0
	if a.Visible {
		visible = 1
	}

	res, err := c.db.Exec(`
		UPDATE applications SET name=?, icon=?, description=?, platforms=?, download=?, deeplink=?, instructions=?, visible=?, sort_order=?, updated_at=?
		WHERE id = ?`,
		a.Name, a.Icon, a.Description, string(platforms), a.Download, a.Deeplink, a.Instructions, visible, a.SortOrder, time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("apps: update %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("apps: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes an app by id.
func (c *Catalog) Delete(id int64) error {
	res, err := c.db.Exec(`DELETE FROM applications WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("apps: delete %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("apps: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RenderDeeplink substitutes the {subscription} and {profileTitle}
// placeholders in an app's deeplink template with real values.
func RenderDeeplink(tmpl, subscriptionURL, profileTitle string) string {
	replacer := strings.NewReplacer(
		"{subscription}", subscriptionURL,
		"{profileTitle}", profileTitle,
	)
	return replacer.Replace(tmpl)
}
