package adminauth

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE admin_users (username TEXT PRIMARY KEY, password_hash TEXT NOT NULL, updated_at INTEGER NOT NULL);
		CREATE TABLE sessions (id TEXT PRIMARY KEY, username TEXT NOT NULL, csrf_token TEXT NOT NULL, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL);
	`); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateUser_AndVerifyPassword(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.CreateUser("admin", "correct-horse-battery-staple"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := s.VerifyPassword("admin", "correct-horse-battery-staple"); err != nil {
		t.Errorf("expected correct password to verify, got %v", err)
	}
	if err := s.VerifyPassword("admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials for wrong password, got %v", err)
	}
	if err := s.VerifyPassword("nonexistent", "anything"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials for unknown user, got %v", err)
	}
}

func TestCreateUser_UpsertsOnReuse(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.CreateUser("admin", "first-password"); err != nil {
		t.Fatalf("CreateUser (1st): %v", err)
	}
	if err := s.CreateUser("admin", "second-password"); err != nil {
		t.Fatalf("CreateUser (2nd): %v", err)
	}

	if err := s.VerifyPassword("admin", "first-password"); err == nil {
		t.Error("expected old password to no longer verify after upsert")
	}
	if err := s.VerifyPassword("admin", "second-password"); err != nil {
		t.Errorf("expected new password to verify, got %v", err)
	}
}

func TestCreateUser_EmptyFieldsRejected(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if err := s.CreateUser("", "password"); err == nil {
		t.Error("expected error for empty username")
	}
	if err := s.CreateUser("admin", ""); err == nil {
		t.Error("expected error for empty password")
	}
}

func TestCreateSession_AndGetSession(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	sess, err := s.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID == "" || sess.CSRFToken == "" {
		t.Fatal("expected non-empty session id and csrf token")
	}
	if sess.ID == sess.CSRFToken {
		t.Error("session id and csrf token should not be equal")
	}

	got, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Username != "admin" || got.CSRFToken != sess.CSRFToken {
		t.Errorf("unexpected session: %+v", got)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if _, err := s.GetSession("does-not-exist"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestGetSession_ExpiredIsRejectedAndDeleted(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	past := time.Now().Add(-time.Hour).Unix()
	if _, err := db.Exec(`INSERT INTO sessions (id, username, csrf_token, expires_at, created_at) VALUES ('expired-id', 'admin', 'tok', ?, ?)`, past, past); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}

	if _, err := s.GetSession("expired-id"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected ErrSessionNotFound for expired session, got %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = 'expired-id'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Error("expected expired session to be deleted as a side effect")
	}
}

func TestDeleteSession(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	sess, err := s.CreateSession("admin")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.DeleteSession(sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.GetSession(sess.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("expected session to be gone after DeleteSession, got %v", err)
	}
}
