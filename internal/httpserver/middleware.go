package httpserver

import (
	"compress/gzip"
	"net/http"
	"regexp"
	"strings"
)

// secureHeaders sets the standard defensive response headers on every
// request. CSRF protection is intentionally omitted: phase 1 exposes no
// state-changing or authenticated public endpoint.
func secureHeaders(csp string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			if csp != "" {
				h.Set("Content-Security-Policy", csp)
			}
			if r.TLS != nil {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// gzipResponseWriter wraps http.ResponseWriter, transparently compressing
// the body when the client advertises gzip support.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) { return w.gz.Write(b) }

// gzipCompress compresses responses when the client sends
// "Accept-Encoding: gzip".
func gzipCompress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")

		gz := gzip.NewWriter(w)
		defer gz.Close()

		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, gz: gz}, r)
	})
}

var subIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// validSubID reports whether s is a well-formed subscription token
// (bounded length, alnum/hyphen/underscore only) before it's used in any
// lookup or file path.
func validSubID(s string) bool {
	return subIDPattern.MatchString(s)
}
