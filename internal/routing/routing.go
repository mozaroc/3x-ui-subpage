// Package routing manages administrator-editable routing rules (table
// "routing_rules") — GEOIP/geosite/domain/CIDR/process/protocol/DNS/custom
// entries that per-profile config-generator templates (Happ, Incy, and any
// future format) can render into their own client's routing syntax. Go owns
// the structured rule data; the template owns the output bytes, same
// division of labor as every other generator in this project.
package routing

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DefaultProfile is the profile ForProfile falls back to when the
// requested profile has no rules of its own.
const DefaultProfile = "default"

// ErrNotFound is returned by Get for a missing rule id.
var ErrNotFound = errors.New("routing: not found")

// Rule is one routing_rules row.
type Rule struct {
	ID        int64
	Profile   string
	SortOrder int
	Type      string
	Value     string
	Outbound  string
	Enabled   bool
	UpdatedAt int64
}

// Store wraps the "routing_rules" table.
type Store struct {
	db *sql.DB
}

// New builds a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// ForProfile returns the enabled rules for profile, ordered by sort_order,
// falling back to DefaultProfile if profile has none of its own.
func (s *Store) ForProfile(profile string) ([]Rule, error) {
	rules, err := s.queryProfile(profile)
	if err != nil {
		return nil, err
	}
	if len(rules) > 0 || profile == DefaultProfile {
		return rules, nil
	}
	return s.queryProfile(DefaultProfile)
}

func (s *Store) queryProfile(profile string) ([]Rule, error) {
	rows, err := s.db.Query(`
		SELECT id, profile, sort_order, type, value, outbound, enabled, updated_at
		FROM routing_rules WHERE profile = ? AND enabled = 1 ORDER BY sort_order, id`, profile)
	if err != nil {
		return nil, fmt.Errorf("routing: query profile %q: %w", profile, err)
	}
	defer rows.Close()
	return scanRules(rows)
}

// List returns every rule, ordered for stable admin display.
func (s *Store) List() ([]Rule, error) {
	rows, err := s.db.Query(`
		SELECT id, profile, sort_order, type, value, outbound, enabled, updated_at
		FROM routing_rules ORDER BY profile, sort_order, id`)
	if err != nil {
		return nil, fmt.Errorf("routing: list: %w", err)
	}
	defer rows.Close()
	return scanRules(rows)
}

func scanRules(rows *sql.Rows) ([]Rule, error) {
	out := make([]Rule, 0)
	for rows.Next() {
		var r Rule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Profile, &r.SortOrder, &r.Type, &r.Value, &r.Outbound, &enabled, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("routing: scan: %w", err)
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns one rule by id, or ErrNotFound.
func (s *Store) Get(id int64) (Rule, error) {
	var r Rule
	var enabled int
	err := s.db.QueryRow(`
		SELECT id, profile, sort_order, type, value, outbound, enabled, updated_at
		FROM routing_rules WHERE id = ?`, id,
	).Scan(&r.ID, &r.Profile, &r.SortOrder, &r.Type, &r.Value, &r.Outbound, &enabled, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	if err != nil {
		return Rule{}, fmt.Errorf("routing: get %d: %w", id, err)
	}
	r.Enabled = enabled != 0
	return r, nil
}

// Create inserts a new rule and returns its id.
func (s *Store) Create(r Rule) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO routing_rules (profile, sort_order, type, value, outbound, enabled, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.Profile, r.SortOrder, r.Type, r.Value, r.Outbound, boolToInt(r.Enabled), time.Now().UnixNano())
	if err != nil {
		return 0, fmt.Errorf("routing: create: %w", err)
	}
	return res.LastInsertId()
}

// Update overwrites an existing rule.
func (s *Store) Update(id int64, r Rule) error {
	_, err := s.db.Exec(`
		UPDATE routing_rules
		SET profile = ?, sort_order = ?, type = ?, value = ?, outbound = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		r.Profile, r.SortOrder, r.Type, r.Value, r.Outbound, boolToInt(r.Enabled), time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("routing: update %d: %w", id, err)
	}
	return nil
}

// Delete removes a rule by id.
func (s *Store) Delete(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM routing_rules WHERE id = ?`, id); err != nil {
		return fmt.Errorf("routing: delete %d: %w", id, err)
	}
	return nil
}

// ListProfiles returns every distinct profile with at least one rule.
func (s *Store) ListProfiles() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT profile FROM routing_rules ORDER BY profile`)
	if err != nil {
		return nil, fmt.Errorf("routing: list profiles: %w", err)
	}
	defer rows.Close()

	out := make([]string, 0)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("routing: scan profile: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
