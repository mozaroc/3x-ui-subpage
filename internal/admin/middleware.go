package admin

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/irazin/3x-ui-subpage/internal/adminauth"
	"github.com/irazin/3x-ui-subpage/internal/ratelimit"
)

const sessionCookieName = "admin_session"

type ctxKey int

const sessionCtxKey ctxKey = 0

func sessionFromContext(r *http.Request) (adminauth.Session, bool) {
	sess, ok := r.Context().Value(sessionCtxKey).(adminauth.Session)
	return sess, ok
}

// requireSession redirects to /admin/login unless a valid, non-expired
// session cookie is present, injecting the session into the request
// context for downstream handlers.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}

		sess, err := s.auth.GetSession(cookie.Value)
		if err != nil {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}

		ctx := context.WithValue(r.Context(), sessionCtxKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// verifyCSRF rejects any POST whose "csrf_token" form field doesn't match
// the current session's token (synchronizer token pattern). Must run after
// requireSession.
func (s *Server) verifyCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := sessionFromContext(r)
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		token := r.FormValue("csrf_token")
		match := len(token) == len(sess.CSRFToken) &&
			subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRFToken)) == 1
		if !match {
			s.logger.Warn("admin: csrf token mismatch", "path", r.URL.Path, "remote_ip", ratelimit.ClientIP(r))
			http.Error(w, "forbidden: invalid csrf token", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// isRequestSecure reports whether r arrived over HTTPS, either directly
// (r.TLS set) or via a reverse proxy that terminated TLS and forwarded the
// original scheme (X-Forwarded-Proto: https) — this service's documented
// deployment (install.sh) always has nginx terminate TLS and proxy to this
// process over plain loopback HTTP, so r.TLS alone is never set there and
// checking it exclusively would silently disable Secure cookies and HSTS in
// the intended production setup. The server only ever listens on loopback
// (nginx is the sole caller), so the header can't be spoofed by an
// end-client.
func isRequestSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// secureHeaders applies the standard defensive response headers to every
// admin request.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// style-src/script-src allow 'unsafe-inline': layout.html's theme
		// toggle needs a tiny inline <script>/<style> block, and nothing in
		// this codebase ever interpolates untrusted content into an inline
		// script or style context, so the XSS risk that keyword normally
		// carries doesn't apply here.
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'")
		if isRequestSecure(r) {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, sess adminauth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sess.ID,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   isRequestSecure(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.ExpiresAt,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/admin",
		HttpOnly: true,
		Secure:   isRequestSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
