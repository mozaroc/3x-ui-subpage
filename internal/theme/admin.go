package theme

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by AdminStore methods for a missing theme slug or
// file path.
var ErrNotFound = errors.New("theme: not found")

// AdminStore provides write access to the themes/theme_files tables, kept
// separate from Engine (which is the read/render/cache path used by the
// public subscription page) so the two responsibilities don't blur.
type AdminStore struct {
	db *sql.DB
}

// NewAdminStore builds an AdminStore backed by db.
func NewAdminStore(db *sql.DB) *AdminStore {
	return &AdminStore{db: db}
}

// ListSlugs returns every theme slug.
func (a *AdminStore) ListSlugs() ([]string, error) {
	rows, err := a.db.Query(`SELECT slug FROM themes ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("theme: query slugs: %w", err)
	}
	defer rows.Close()

	slugs := make([]string, 0)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("theme: scan slug: %w", err)
		}
		slugs = append(slugs, s)
	}
	return slugs, rows.Err()
}

// GetMeta returns one theme's metadata.
func (a *AdminStore) GetMeta(slug string) (Meta, error) {
	var m Meta
	var colorsJSON, fontsJSON string

	err := a.db.QueryRow(`
		SELECT name, description, logo, favicon, colors, fonts FROM themes WHERE slug = ?`, slug,
	).Scan(&m.Name, &m.Description, &m.Logo, &m.Favicon, &colorsJSON, &fontsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Meta{}, ErrNotFound
	}
	if err != nil {
		return Meta{}, fmt.Errorf("theme: query %q: %w", slug, err)
	}

	if err := json.Unmarshal([]byte(colorsJSON), &m.Colors); err != nil {
		return Meta{}, fmt.Errorf("theme: decode colors: %w", err)
	}
	if err := json.Unmarshal([]byte(fontsJSON), &m.Fonts); err != nil {
		return Meta{}, fmt.Errorf("theme: decode fonts: %w", err)
	}
	m.Slug = slug
	return m, nil
}

// UpsertMeta creates a new theme slug or overwrites an existing one's
// metadata.
func (a *AdminStore) UpsertMeta(slug string, m Meta) error {
	colors, err := json.Marshal(m.Colors)
	if err != nil {
		return fmt.Errorf("theme: marshal colors: %w", err)
	}
	fonts, err := json.Marshal(m.Fonts)
	if err != nil {
		return fmt.Errorf("theme: marshal fonts: %w", err)
	}

	_, err = a.db.Exec(`
		INSERT INTO themes (slug, name, description, logo, favicon, colors, fonts, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			name = excluded.name, description = excluded.description,
			logo = excluded.logo, favicon = excluded.favicon,
			colors = excluded.colors, fonts = excluded.fonts, updated_at = excluded.updated_at`,
		slug, m.Name, m.Description, m.Logo, m.Favicon, string(colors), string(fonts), time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("theme: upsert %q: %w", slug, err)
	}
	return nil
}

// ListFiles returns every file path stored for slug.
func (a *AdminStore) ListFiles(slug string) ([]string, error) {
	rows, err := a.db.Query(`SELECT path FROM theme_files WHERE theme_slug = ? ORDER BY path`, slug)
	if err != nil {
		return nil, fmt.Errorf("theme: query files for %q: %w", slug, err)
	}
	defer rows.Close()

	paths := make([]string, 0)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("theme: scan path: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// GetFile returns one file's raw content.
func (a *AdminStore) GetFile(slug, path string) ([]byte, error) {
	var content []byte
	err := a.db.QueryRow(`SELECT content FROM theme_files WHERE theme_slug = ? AND path = ?`, slug, path).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("theme: query file %q/%q: %w", slug, path, err)
	}
	return content, nil
}

// PutFile creates or overwrites a file's content.
func (a *AdminStore) PutFile(slug, path string, content []byte) error {
	_, err := a.db.Exec(`
		INSERT INTO theme_files (theme_slug, path, content, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(theme_slug, path) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`,
		slug, path, content, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("theme: put file %q/%q: %w", slug, path, err)
	}
	return nil
}

// DeleteFile removes a file.
func (a *AdminStore) DeleteFile(slug, path string) error {
	res, err := a.db.Exec(`DELETE FROM theme_files WHERE theme_slug = ? AND path = ?`, slug, path)
	if err != nil {
		return fmt.Errorf("theme: delete file %q/%q: %w", slug, path, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("theme: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
