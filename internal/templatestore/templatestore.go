// Package templatestore is the admin-write counterpart to
// generator/tmplcache's hot-reloaded, profile-falling-back reads: plain
// CRUD directly over the "templates" table, used by the admin UI to list,
// create, edit, and delete (format, profile, protocol) template rows.
package templatestore

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by Get for a missing (format, profile, protocol)
// row.
var ErrNotFound = errors.New("templatestore: not found")

// Row is one (format, profile, protocol) template.
type Row struct {
	Format    string
	Profile   string
	Protocol  string
	Content   string
	UpdatedAt int64
}

// Store wraps the "templates" table.
type Store struct {
	db *sql.DB
}

// New builds a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// List returns every template row, ordered for stable display (format,
// then profile, then protocol).
func (s *Store) List() ([]Row, error) {
	rows, err := s.db.Query(`
		SELECT format, profile, protocol, content, updated_at
		FROM templates
		ORDER BY format, profile, protocol`)
	if err != nil {
		return nil, fmt.Errorf("templatestore: query: %w", err)
	}
	defer rows.Close()

	out := make([]Row, 0)
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.Format, &r.Profile, &r.Protocol, &r.Content, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("templatestore: scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("templatestore: iterate: %w", err)
	}
	return out, nil
}

// Get returns one template row, or ErrNotFound.
func (s *Store) Get(format, profile, protocol string) (Row, error) {
	r := Row{Format: format, Profile: profile, Protocol: protocol}
	err := s.db.QueryRow(`
		SELECT content, updated_at FROM templates WHERE format = ? AND profile = ? AND protocol = ?`,
		format, profile, protocol,
	).Scan(&r.Content, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Row{}, ErrNotFound
	}
	if err != nil {
		return Row{}, fmt.Errorf("templatestore: query: %w", err)
	}
	return r, nil
}

// Put creates or overwrites a template row.
func (s *Store) Put(format, profile, protocol, content string) error {
	_, err := s.db.Exec(`
		INSERT INTO templates (format, profile, protocol, content, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(format, profile, protocol) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`,
		format, profile, protocol, content, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("templatestore: put (%s,%s,%s): %w", format, profile, protocol, err)
	}
	return nil
}

// Delete removes a template row.
func (s *Store) Delete(format, profile, protocol string) error {
	_, err := s.db.Exec(`DELETE FROM templates WHERE format = ? AND profile = ? AND protocol = ?`, format, profile, protocol)
	if err != nil {
		return fmt.Errorf("templatestore: delete (%s,%s,%s): %w", format, profile, protocol, err)
	}
	return nil
}
