// Package logging configures the service's structured logger (log/slog) and
// provides an HTTP request-logging middleware.
package logging

import (
	"log/slog"
	"net/http"
	"os"
	"time"
)

// New builds a slog.Logger writing to stdout in either "json" or "console"
// (text) format at the given level ("debug", "info", "warn", "error").
func New(level, format string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if format == "console" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

// statusRecorder captures the response status code so it can be logged
// after the handler chain runs.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// RequestLogger returns middleware that logs method, path, status, remote
// address, and latency for every request at info level.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"remote", r.RemoteAddr,
				"user_agent", r.UserAgent(),
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
