// Package assignment resolves which template "profile" a subscriber should
// get, per client type (table "template_assignments", (sub_id, client_type)
// -> profile). Subscribers with no row for a given client type use the
// "default" profile — the same fallback every generator applies when a
// profile is missing a template for a given format/protocol.
package assignment

import (
	"database/sql"
	"fmt"
	"time"
)

// DefaultProfile is returned for any (subscriber, client type) with no
// assignment row.
const DefaultProfile = "default"

// ClientType is one admin-facing template-assignment selector. Formats
// lists which "templates" table format(s) this client type's profile
// applies to.
type ClientType struct {
	Key     string
	Label   string
	Formats []string
}

// ClientTypes are every known client type, in display order. Xray's
// share-link format has no template/profile of its own — this project
// fetches 3x-ui's own canonical share links rather than templating them
// (see internal/generator/tmplctx) — so "xray" governs only the full
// xray-core JSON config format.
var ClientTypes = []ClientType{
	{Key: "xray", Label: "Xray", Formats: []string{"xray_json"}},
	{Key: "clash", Label: "Clash", Formats: []string{"clash"}},
	{Key: "mihomo", Label: "Mihomo", Formats: []string{"mihomo"}},
	{Key: "happ", Label: "Happ", Formats: []string{"happ"}},
	{Key: "incy", Label: "Incy", Formats: []string{"incy"}},
}

// formatToClientType maps a "templates" table format string back to the
// client type key that governs its profile.
var formatToClientType = func() map[string]string {
	m := make(map[string]string)
	for _, ct := range ClientTypes {
		for _, f := range ct.Formats {
			m[f] = ct.Key
		}
	}
	return m
}()

// Store resolves subscription-token -> profile-name assignments, per
// client type.
type Store struct {
	db *sql.DB
}

// New builds a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Resolve returns the profile assigned to subID for the given "templates"
// format, or DefaultProfile if no assignment exists. format must be one of
// the strings listed in some ClientType.Formats.
func (s *Store) Resolve(subID, format string) (string, error) {
	clientType, ok := formatToClientType[format]
	if !ok {
		return "", fmt.Errorf("assignment: unknown format %q", format)
	}

	var profile string
	err := s.db.QueryRow(
		`SELECT profile FROM template_assignments WHERE sub_id = ? AND client_type = ?`,
		subID, clientType,
	).Scan(&profile)
	switch {
	case err == nil:
		return profile, nil
	case err == sql.ErrNoRows:
		return DefaultProfile, nil
	default:
		return "", fmt.Errorf("assignment: query %s/%s: %w", subID, clientType, err)
	}
}

// Set assigns subID's clientType to profile, creating or overwriting its
// row.
func (s *Store) Set(subID, clientType, profile string) error {
	_, err := s.db.Exec(`
		INSERT INTO template_assignments (sub_id, client_type, profile, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(sub_id, client_type) DO UPDATE SET profile = excluded.profile, updated_at = excluded.updated_at`,
		subID, clientType, profile, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("assignment: set %s/%s: %w", subID, clientType, err)
	}
	return nil
}

// ForSubID returns every client type's assigned profile for subID, filling
// in DefaultProfile for any client type with no explicit row.
func (s *Store) ForSubID(subID string) (map[string]string, error) {
	out := make(map[string]string, len(ClientTypes))
	for _, ct := range ClientTypes {
		out[ct.Key] = DefaultProfile
	}

	rows, err := s.db.Query(`SELECT client_type, profile FROM template_assignments WHERE sub_id = ?`, subID)
	if err != nil {
		return nil, fmt.Errorf("assignment: query %s: %w", subID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var clientType, profile string
		if err := rows.Scan(&clientType, &profile); err != nil {
			return nil, fmt.Errorf("assignment: scan %s: %w", subID, err)
		}
		out[clientType] = profile
	}
	return out, rows.Err()
}

// DeleteAll removes every assignment row for subID (every client type).
func (s *Store) DeleteAll(subID string) error {
	if _, err := s.db.Exec(`DELETE FROM template_assignments WHERE sub_id = ?`, subID); err != nil {
		return fmt.Errorf("assignment: delete all %s: %w", subID, err)
	}
	return nil
}
