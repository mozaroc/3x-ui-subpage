// Package ratelimit implements a per-key token-bucket rate limiter used by
// both the public subscription API and the admin login route (each gets its
// own Limiter instance with its own rate/burst).
package ratelimit

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

// Limiter is a per-key token bucket limiter with lazy bucket creation;
// buckets are never evicted (bounded by unique keys seen, acceptable at the
// target scale — revisit if that becomes an issue).
type Limiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

// New builds a Limiter allowing requestsPerMinute steady-state throughput
// per key, with burst as the token bucket's capacity.
func New(requestsPerMinute, burst int) *Limiter {
	rps := rate.Limit(float64(requestsPerMinute) / 60.0)
	if burst < 1 {
		burst = 1
	}
	return &Limiter{limiters: make(map[string]*rate.Limiter), rps: rps, burst: burst}
}

// Allow reports whether a request for key may proceed right now, consuming
// a token if so.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	lim, ok := l.limiters[key]
	if !ok {
		lim = rate.NewLimiter(l.rps, l.burst)
		l.limiters[key] = lim
	}
	l.mu.Unlock()
	return lim.Allow()
}

// ClientIP extracts the remote IP from a request, stripping the port.
func ClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// Middleware rejects requests exceeding the limit (keyed by client IP) with
// 429, logging a warning via logger.
func (l *Limiter) Middleware(logger interface {
	Warn(msg string, args ...any)
}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := ClientIP(r)
			if !l.Allow(key) {
				logger.Warn("rate limit exceeded", "ip", key, "path", r.URL.Path)
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
