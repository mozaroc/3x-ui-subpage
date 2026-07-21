// Package assignment resolves which template "profile" a subscriber should
// get (table "assignments", sub_id -> profile). Subscribers with no row
// use the "default" profile — the same fallback every generator applies
// when a profile is missing a template for a given format/protocol.
package assignment

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DefaultProfile is returned for any subscriber with no assignment row.
const DefaultProfile = "default"

// Assignment is one subscriber -> profile mapping.
type Assignment struct {
	SubID   string
	Profile string
}

// Store resolves subscription-token -> profile-name assignments.
type Store struct {
	db *sql.DB
}

// New builds a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Resolve returns the profile assigned to subID, or DefaultProfile if no
// assignment exists.
func (s *Store) Resolve(subID string) (string, error) {
	var profile string
	err := s.db.QueryRow(`SELECT profile FROM assignments WHERE sub_id = ?`, subID).Scan(&profile)
	switch {
	case err == nil:
		return profile, nil
	case errors.Is(err, sql.ErrNoRows):
		return DefaultProfile, nil
	default:
		return "", fmt.Errorf("assignment: query %s: %w", subID, err)
	}
}

// Set assigns subID to profile, creating or overwriting its row.
func (s *Store) Set(subID, profile string) error {
	_, err := s.db.Exec(`
		INSERT INTO assignments (sub_id, profile, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(sub_id) DO UPDATE SET profile = excluded.profile, updated_at = excluded.updated_at`,
		subID, profile, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("assignment: set %s: %w", subID, err)
	}
	return nil
}

// Delete removes subID's assignment (it falls back to DefaultProfile).
func (s *Store) Delete(subID string) error {
	if _, err := s.db.Exec(`DELETE FROM assignments WHERE sub_id = ?`, subID); err != nil {
		return fmt.Errorf("assignment: delete %s: %w", subID, err)
	}
	return nil
}

// List returns every explicit assignment (subscribers with no row, using
// DefaultProfile implicitly, are not listed).
func (s *Store) List() ([]Assignment, error) {
	rows, err := s.db.Query(`SELECT sub_id, profile FROM assignments ORDER BY sub_id`)
	if err != nil {
		return nil, fmt.Errorf("assignment: query: %w", err)
	}
	defer rows.Close()

	out := make([]Assignment, 0)
	for rows.Next() {
		var a Assignment
		if err := rows.Scan(&a.SubID, &a.Profile); err != nil {
			return nil, fmt.Errorf("assignment: scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
