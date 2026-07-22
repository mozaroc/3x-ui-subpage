// Package theme implements the HTML theme engine: layouts, partials, static
// assets, and per-theme metadata (colors/logo/fonts), all loaded from the
// "themes"/"theme_files" tables in the shared SQLite database and
// hot-reloaded whenever any relevant row's updated_at advances, so
// administrators can restyle the subscription page without recompiling —
// or even restarting.
package theme

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
)

// Meta is a theme's metadata (table "themes"), merged into every page's
// render data.
type Meta struct {
	Name        string
	Description string
	Logo        string
	Favicon     string
	Colors      map[string]string
	Fonts       map[string]string

	// Slug is the active theme's identifier, injected by Engine rather than
	// read from the row — it's how templates build asset URLs like
	// /assets/{{.Theme.Slug}}/css/style.css.
	Slug string
}

// PageData is what every theme template renders against: theme metadata
// plus whatever page-specific data the caller supplies.
type PageData struct {
	Theme Meta
	Data  any
}

// Engine loads and caches one active theme's templates and static assets,
// reloading them whenever any relevant row's updated_at changes.
type Engine struct {
	db   *sql.DB
	slug string

	mu       sync.Mutex
	tmpl     *template.Template
	meta     Meta
	static   map[string][]byte
	newestAt int64
}

// New builds an Engine for the theme named slug, backed by db. The theme is
// not loaded until the first Render/ServeStatic call, so a missing/broken
// theme fails at request time with a clear error rather than at startup.
func New(db *sql.DB, slug string) *Engine {
	return &Engine{db: db, slug: slug}
}

// Render executes the theme's "layout" template, wrapping data in PageData
// alongside the theme's metadata. Reloads from the database first if
// anything about the theme changed since the last render.
func (e *Engine) Render(w io.Writer, data any) error {
	if err := e.reloadIfNeeded(); err != nil {
		return err
	}

	e.mu.Lock()
	tmpl, meta := e.tmpl, e.meta
	e.mu.Unlock()

	return tmpl.ExecuteTemplate(w, "layout", PageData{Theme: meta, Data: data})
}

// ServeStatic writes the theme's static asset at path (e.g.
// "css/style.css") to w with an inferred content type. Returns false if no
// such asset exists (caller should respond 404).
func (e *Engine) ServeStatic(w http.ResponseWriter, r *http.Request, path string) (bool, error) {
	if err := e.reloadIfNeeded(); err != nil {
		return false, err
	}

	e.mu.Lock()
	content, ok := e.static[path]
	e.mu.Unlock()
	if !ok {
		return false, nil
	}

	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	_, err := w.Write(content)
	return true, err
}

func (e *Engine) reloadIfNeeded() error {
	var newest sql.NullInt64
	err := e.db.QueryRow(`
		SELECT MAX(x) FROM (
			SELECT updated_at AS x FROM themes WHERE slug = ?
			UNION ALL
			SELECT updated_at FROM theme_files WHERE theme_slug = ?
		)`, e.slug, e.slug).Scan(&newest)
	if err != nil {
		return fmt.Errorf("theme: query max updated_at: %w", err)
	}

	// Also track the file count: deleting a theme_files row can only ever
	// leave the remaining max updated_at the same or lower, never higher,
	// so a deletion that doesn't remove the single most-recently-touched
	// file would otherwise go undetected by the MAX(updated_at) check alone.
	var fileCount int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM theme_files WHERE theme_slug = ?`, e.slug).Scan(&fileCount); err != nil {
		return fmt.Errorf("theme: count theme_files: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.tmpl != nil && newest.Int64 == e.newestAt && fileCount == len(e.static) {
		return nil
	}

	meta, err := e.loadMeta()
	if err != nil {
		return fmt.Errorf("theme: load theme %q: %w", e.slug, err)
	}

	tmpl, static, err := e.loadFiles()
	if err != nil {
		return fmt.Errorf("theme: load files for %q: %w", e.slug, err)
	}

	e.meta = meta
	e.tmpl = tmpl
	e.static = static
	e.newestAt = newest.Int64
	return nil
}

func (e *Engine) loadMeta() (Meta, error) {
	var m Meta
	var colorsJSON, fontsJSON string

	err := e.db.QueryRow(`
		SELECT name, description, logo, favicon, colors, fonts
		FROM themes WHERE slug = ?`, e.slug,
	).Scan(&m.Name, &m.Description, &m.Logo, &m.Favicon, &colorsJSON, &fontsJSON)
	if err != nil {
		return Meta{}, fmt.Errorf("query themes row: %w", err)
	}

	if err := json.Unmarshal([]byte(colorsJSON), &m.Colors); err != nil {
		return Meta{}, fmt.Errorf("decode colors: %w", err)
	}
	if err := json.Unmarshal([]byte(fontsJSON), &m.Fonts); err != nil {
		return Meta{}, fmt.Errorf("decode fonts: %w", err)
	}
	m.Slug = e.slug

	return m, nil
}

// loadFiles reads every theme_files row for the active theme, parsing
// "*.html" paths into the returned template set and keeping everything else
// (static/**) as raw bytes for ServeStatic.
func (e *Engine) loadFiles() (*template.Template, map[string][]byte, error) {
	rows, err := e.db.Query(`SELECT path, content FROM theme_files WHERE theme_slug = ?`, e.slug)
	if err != nil {
		return nil, nil, fmt.Errorf("query theme_files: %w", err)
	}
	defer rows.Close()

	tmpl := template.New("theme").Funcs(FuncMap())
	static := make(map[string][]byte)

	for rows.Next() {
		var path string
		var content []byte
		if err := rows.Scan(&path, &content); err != nil {
			return nil, nil, fmt.Errorf("scan theme_files row: %w", err)
		}

		if strings.HasSuffix(path, ".html") {
			if _, err := tmpl.New(path).Parse(string(content)); err != nil {
				return nil, nil, fmt.Errorf("parse %s: %w", path, err)
			}
			continue
		}

		staticPath := strings.TrimPrefix(path, "static/")
		static[staticPath] = content
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate theme_files: %w", err)
	}

	return tmpl, static, nil
}
