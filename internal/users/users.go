// Package users is the subscription-service's canonical source of truth for
// subscriber accounts (table "users") and their inbound assignments (table
// "user_inbounds"). It owns local bookkeeping only — pushing changes out to
// the connected 3x-ui panel is internal/sync's job. A User's uuid/password/
// method/flow are applied uniformly to every inbound it's assigned to,
// regardless of that inbound's protocol; unused fields are simply ignored by
// protocols that don't need them.
package users

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned by Get/Update/Delete for a missing user id.
var ErrNotFound = errors.New("users: not found")

// ErrDuplicate is returned by Create/Update when username or sub_id already
// belongs to another user.
var ErrDuplicate = errors.New("users: username or sub_id already in use")

// User is one users row.
type User struct {
	ID        int64
	Username  string
	SubID     string
	UUID      string
	Password  string
	Method    string
	Flow      string
	Enabled   bool
	TotalGB   int64
	ExpiryMs  int64
	Notes     string
	CreatedAt int64
	UpdatedAt int64
}

// UserInbound is one user_inbounds row: a single 3x-ui inbound a User is
// assigned to.
type UserInbound struct {
	ID         int64
	UserID     int64
	InboundID  int
	InboundTag string
	Protocol   string
	CreatedAt  int64
	UpdatedAt  int64
}

// ListFilter narrows/orders/paginates Store.List.
type ListFilter struct {
	Query   string // matches username or sub_id, substring, case-insensitive
	Status  string // "enabled", "disabled", or "" for all
	SortBy  string // "username", "sub_id", "created_at", "expiry_ms", "total_gb" — default "username"
	SortDir string // "asc" or "desc" — default "asc"
	Limit   int    // 0 = no limit
	Offset  int
}

// Store wraps the "users" and "user_inbounds" tables.
type Store struct {
	db *sql.DB
}

// New builds a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

const defaultMethod = "chacha20-ietf-poly1305"

// Create inserts a new user, generating a uuid/password if not already set
// by the caller, and returns its id.
func (s *Store) Create(u User) (int64, error) {
	if u.UUID == "" {
		u.UUID = uuid.NewString()
	}
	if u.Password == "" {
		pw, err := randomToken()
		if err != nil {
			return 0, err
		}
		u.Password = pw
	}
	if u.Method == "" {
		u.Method = defaultMethod
	}

	now := time.Now().UnixNano()
	res, err := s.db.Exec(`
		INSERT INTO users (username, sub_id, uuid, password, method, flow, enabled, total_gb, expiry_ms, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Username, u.SubID, u.UUID, u.Password, u.Method, u.Flow, boolToInt(u.Enabled), u.TotalGB, u.ExpiryMs, u.Notes, now, now)
	if err != nil {
		if isUniqueConstraint(err) {
			return 0, ErrDuplicate
		}
		return 0, fmt.Errorf("users: create: %w", err)
	}
	return res.LastInsertId()
}

// Get returns one user by id, or ErrNotFound.
func (s *Store) Get(id int64) (User, error) {
	return s.scanOne(s.db.QueryRow(`
		SELECT id, username, sub_id, uuid, password, method, flow, enabled, total_gb, expiry_ms, notes, created_at, updated_at
		FROM users WHERE id = ?`, id))
}

// Update overwrites username/sub_id/flow/method/total_gb/expiry_ms/notes for
// an existing user. It does not touch uuid/password (see
// RegenerateCredentials) or enabled (see SetEnabled).
func (s *Store) Update(id int64, u User) error {
	res, err := s.db.Exec(`
		UPDATE users SET username = ?, sub_id = ?, flow = ?, method = ?, total_gb = ?, expiry_ms = ?, notes = ?, updated_at = ?
		WHERE id = ?`,
		u.Username, u.SubID, u.Flow, u.Method, u.TotalGB, u.ExpiryMs, u.Notes, time.Now().UnixNano(), id)
	if err != nil {
		if isUniqueConstraint(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("users: update %d: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// SetEnabled flips a user's enabled flag (also used for suspend/reactivate —
// 3x-ui has no separate state to distinguish the two by).
func (s *Store) SetEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE users SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolToInt(enabled), time.Now().UnixNano(), id)
	if err != nil {
		return fmt.Errorf("users: set enabled %d: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// RegenerateCredentials rotates a user's uuid and password and returns the
// updated user.
func (s *Store) RegenerateCredentials(id int64) (User, error) {
	pw, err := randomToken()
	if err != nil {
		return User{}, err
	}
	res, err := s.db.Exec(`UPDATE users SET uuid = ?, password = ?, updated_at = ? WHERE id = ?`,
		uuid.NewString(), pw, time.Now().UnixNano(), id)
	if err != nil {
		return User{}, fmt.Errorf("users: regenerate credentials %d: %w", id, err)
	}
	if err := checkRowsAffected(res, id); err != nil {
		return User{}, err
	}
	return s.Get(id)
}

// Delete removes a user. Callers are responsible for enqueuing "unassign"
// sync jobs for every existing assignment before calling this — the
// ON DELETE CASCADE on user_inbounds removes assignment rows locally, but
// does not by itself tell the panel to delete the corresponding clients.
func (s *Store) Delete(id int64) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("users: delete %d: %w", id, err)
	}
	return checkRowsAffected(res, id)
}

// List returns a page of users matching filter, plus the total count of
// matching rows (ignoring Limit/Offset) for pagination.
func (s *Store) List(filter ListFilter) ([]User, int, error) {
	where := "WHERE 1=1"
	var args []any

	if filter.Query != "" {
		where += " AND (LOWER(username) LIKE ? OR LOWER(sub_id) LIKE ?)"
		q := "%" + strings.ToLower(filter.Query) + "%"
		args = append(args, q, q)
	}
	switch filter.Status {
	case "enabled":
		where += " AND enabled = 1"
	case "disabled":
		where += " AND enabled = 0"
	}

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("users: count: %w", err)
	}

	sortCol := "username"
	switch filter.SortBy {
	case "sub_id", "created_at", "expiry_ms", "total_gb":
		sortCol = filter.SortBy
	}
	sortDir := "ASC"
	if strings.EqualFold(filter.SortDir, "desc") {
		sortDir = "DESC"
	}

	query := fmt.Sprintf(`
		SELECT id, username, sub_id, uuid, password, method, flow, enabled, total_gb, expiry_ms, notes, created_at, updated_at
		FROM users %s ORDER BY %s %s, id`, where, sortCol, sortDir)
	queryArgs := append([]any{}, args...)
	if filter.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		queryArgs = append(queryArgs, filter.Limit, filter.Offset)
	}

	rows, err := s.db.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("users: list: %w", err)
	}
	defer rows.Close()

	out := make([]User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("users: iterate: %w", err)
	}
	return out, total, nil
}

// Inbounds returns every inbound userID is currently assigned to.
func (s *Store) Inbounds(userID int64) ([]UserInbound, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, inbound_id, inbound_tag, protocol, created_at, updated_at
		FROM user_inbounds WHERE user_id = ? ORDER BY inbound_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("users: inbounds %d: %w", userID, err)
	}
	defer rows.Close()

	out := make([]UserInbound, 0)
	for rows.Next() {
		var ui UserInbound
		if err := rows.Scan(&ui.ID, &ui.UserID, &ui.InboundID, &ui.InboundTag, &ui.Protocol, &ui.CreatedAt, &ui.UpdatedAt); err != nil {
			return nil, fmt.Errorf("users: scan inbound: %w", err)
		}
		out = append(out, ui)
	}
	return out, rows.Err()
}

// Desired describes one inbound a user should be assigned to, as passed to
// SetInbounds.
type Desired struct {
	InboundID  int
	InboundTag string
	Protocol   string
}

// SetInbounds reconciles userID's assignments to exactly the given desired
// set, inserting new rows and deleting rows no longer wanted. It returns
// what was added and removed so the caller can enqueue the matching sync
// jobs for only the delta.
func (s *Store) SetInbounds(userID int64, desired []Desired) (added, removed []UserInbound, err error) {
	current, err := s.Inbounds(userID)
	if err != nil {
		return nil, nil, err
	}

	currentByID := make(map[int]UserInbound, len(current))
	for _, ui := range current {
		currentByID[ui.InboundID] = ui
	}
	desiredByID := make(map[int]Desired, len(desired))
	for _, d := range desired {
		desiredByID[d.InboundID] = d
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("users: set inbounds begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UnixNano()

	for _, d := range desired {
		if _, exists := currentByID[d.InboundID]; exists {
			continue
		}
		res, err := tx.Exec(`
			INSERT INTO user_inbounds (user_id, inbound_id, inbound_tag, protocol, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, userID, d.InboundID, d.InboundTag, d.Protocol, now, now)
		if err != nil {
			return nil, nil, fmt.Errorf("users: assign inbound %d: %w", d.InboundID, err)
		}
		id, _ := res.LastInsertId()
		added = append(added, UserInbound{ID: id, UserID: userID, InboundID: d.InboundID, InboundTag: d.InboundTag, Protocol: d.Protocol, CreatedAt: now, UpdatedAt: now})
	}

	for _, ui := range current {
		if _, wanted := desiredByID[ui.InboundID]; wanted {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM user_inbounds WHERE id = ?`, ui.ID); err != nil {
			return nil, nil, fmt.Errorf("users: unassign inbound %d: %w", ui.InboundID, err)
		}
		removed = append(removed, ui)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("users: set inbounds commit: %w", err)
	}
	return added, removed, nil
}

func (s *Store) scanOne(row *sql.Row) (User, error) {
	var u User
	var enabled int
	err := row.Scan(&u.ID, &u.Username, &u.SubID, &u.UUID, &u.Password, &u.Method, &u.Flow, &enabled, &u.TotalGB, &u.ExpiryMs, &u.Notes, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("users: get: %w", err)
	}
	u.Enabled = enabled != 0
	return u, nil
}

func scanUser(rows *sql.Rows) (User, error) {
	var u User
	var enabled int
	if err := rows.Scan(&u.ID, &u.Username, &u.SubID, &u.UUID, &u.Password, &u.Method, &u.Flow, &enabled, &u.TotalGB, &u.ExpiryMs, &u.Notes, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return User{}, fmt.Errorf("users: scan: %w", err)
	}
	u.Enabled = enabled != 0
	return u, nil
}

func checkRowsAffected(res sql.Result, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("users: rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isUniqueConstraint(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("users: generate random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
