package admin

import (
	"errors"
	"net/http"

	"github.com/irazin/3x-ui-subpage/internal/adminauth"
	"github.com/irazin/3x-ui-subpage/internal/ratelimit"
)

type loginPageData struct {
	Error string
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	// Already logged in? Skip straight to the dashboard.
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if _, err := s.auth.GetSession(cookie.Value); err == nil {
			http.Redirect(w, r, "/admin/", http.StatusFound)
			return
		}
	}

	_ = render(w, "page-login", PageData{Data: loginPageData{}})
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	if err := s.auth.VerifyPassword(username, password); err != nil {
		if !errors.Is(err, adminauth.ErrInvalidCredentials) {
			s.logger.Error("admin: verify password failed", "err", err)
		}
		s.logger.Warn("admin: failed login attempt", "username", username, "remote_ip", ratelimit.ClientIP(r))
		w.WriteHeader(http.StatusUnauthorized)
		_ = render(w, "page-login", PageData{Data: loginPageData{Error: "Invalid username or password"}})
		return
	}

	sess, err := s.auth.CreateSession(username)
	if err != nil {
		s.logger.Error("admin: create session failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	setSessionCookie(w, r, sess)
	http.Redirect(w, r, "/admin/", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.auth.DeleteSession(cookie.Value)
	}
	clearSessionCookie(w, r)
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)
	_ = render(w, "page-dashboard", PageData{Username: sess.Username, CSRFToken: sess.CSRFToken})
}
