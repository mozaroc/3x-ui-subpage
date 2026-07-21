// Package adminauth implements the admin panel's authentication: a single
// bcrypt-hashed admin account (table "admin_users") and server-side
// sessions (table "sessions") carrying a per-session CSRF token.
package adminauth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// SessionTTL is how long a session stays valid after creation.
const SessionTTL = 12 * time.Hour

// ErrInvalidCredentials is returned by VerifyPassword when the username
// doesn't exist or the password doesn't match — deliberately the same error
// for both cases, so callers can't distinguish "no such user" from "wrong
// password" and use that to enumerate accounts.
var ErrInvalidCredentials = errors.New("adminauth: invalid credentials")

// ErrSessionNotFound is returned by GetSession for a missing or expired
// session.
var ErrSessionNotFound = errors.New("adminauth: session not found")

// Session is a validated, non-expired session.
type Session struct {
	ID        string
	Username  string
	CSRFToken string
	ExpiresAt time.Time
}

// Store wraps the admin_users/sessions tables.
type Store struct {
	db *sql.DB
}

// New builds a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateUser hashes password and upserts the admin account. Used by the
// one-shot `-create-admin` CLI flow, not exposed through the web UI itself
// (there is exactly one admin account).
func (s *Store) CreateUser(username, password string) error {
	if username == "" || password == "" {
		return fmt.Errorf("adminauth: username and password are required")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("adminauth: hash password: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO admin_users (username, password_hash, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
		username, string(hash), time.Now().Unix())
	if err != nil {
		return fmt.Errorf("adminauth: create user: %w", err)
	}
	return nil
}

// VerifyPassword checks username/password against the stored hash.
// Returns ErrInvalidCredentials for either a missing user or a wrong
// password.
func (s *Store) VerifyPassword(username, password string) error {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM admin_users WHERE username = ?`, username).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		// Run the bcrypt comparison anyway against a fixed dummy hash, so a
		// nonexistent-username response takes roughly the same time as a
		// wrong-password one and doesn't leak account existence via timing.
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		return ErrInvalidCredentials
	}
	if err != nil {
		return fmt.Errorf("adminauth: query user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// dummyHash is a valid bcrypt hash of an arbitrary fixed password, used only
// to equalize timing on unknown-username login attempts.
const dummyHash = "$2a$10$C6UzMDM.H6dfI/f/IKcEeO7C64/dO0kt2mWQEuRDtQTPCJEsEHVEG"

// CreateSession issues a new session for username with a fresh random ID
// and CSRF token, expiring after SessionTTL.
func (s *Store) CreateSession(username string) (Session, error) {
	id, err := randomToken()
	if err != nil {
		return Session{}, fmt.Errorf("adminauth: generate session id: %w", err)
	}
	csrfToken, err := randomToken()
	if err != nil {
		return Session{}, fmt.Errorf("adminauth: generate csrf token: %w", err)
	}

	now := time.Now()
	expiresAt := now.Add(SessionTTL)

	_, err = s.db.Exec(`INSERT INTO sessions (id, username, csrf_token, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, username, csrfToken, expiresAt.Unix(), now.Unix())
	if err != nil {
		return Session{}, fmt.Errorf("adminauth: create session: %w", err)
	}

	return Session{ID: id, Username: username, CSRFToken: csrfToken, ExpiresAt: expiresAt}, nil
}

// GetSession looks up sessionID, returning ErrSessionNotFound if it's
// missing or has expired (an expired session is deleted as a side effect).
func (s *Store) GetSession(sessionID string) (Session, error) {
	var sess Session
	var expiresAtUnix int64

	err := s.db.QueryRow(`SELECT id, username, csrf_token, expires_at FROM sessions WHERE id = ?`, sessionID).
		Scan(&sess.ID, &sess.Username, &sess.CSRFToken, &expiresAtUnix)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("adminauth: query session: %w", err)
	}

	sess.ExpiresAt = time.Unix(expiresAtUnix, 0)
	if sess.ExpiresAt.Before(time.Now()) {
		_ = s.DeleteSession(sessionID)
		return Session{}, ErrSessionNotFound
	}

	return sess, nil
}

// DeleteSession removes a session (logout, or lazy expiry cleanup).
func (s *Store) DeleteSession(sessionID string) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("adminauth: delete session: %w", err)
	}
	return nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
