// Package admin implements the server-rendered admin panel: login/session
// management and full CRUD over settings, the application catalog, themes,
// generator templates, and per-user template assignments. Mounted at
// /admin by cmd/subscription-service.
//
// Unlike the subscriber-facing theme engine, this package's own HTML is
// //go:embed'ded application code, not admin-editable database content —
// see render.go.
package admin

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/adminauth"
	"github.com/irazin/3x-ui-subpage/internal/apps"
	"github.com/irazin/3x-ui-subpage/internal/assignment"
	"github.com/irazin/3x-ui-subpage/internal/ratelimit"
	"github.com/irazin/3x-ui-subpage/internal/templatestore"
	"github.com/irazin/3x-ui-subpage/internal/theme"
)

// Server holds every collaborator the admin panel needs.
type Server struct {
	db     *sql.DB
	logger *slog.Logger

	auth        *adminauth.Store
	apps        *apps.Catalog
	themes      *theme.AdminStore
	templates   *templatestore.Store
	assignments *assignment.Store

	loginLimiter *ratelimit.Limiter
}

// New builds a Server. db backs every store directly (settings read/write
// goes through internal/config functions called with the same db).
func New(db *sql.DB, logger *slog.Logger) *Server {
	return &Server{
		db:           db,
		logger:       logger,
		auth:         adminauth.New(db),
		apps:         apps.New(db),
		themes:       theme.NewAdminStore(db),
		templates:    templatestore.New(db),
		assignments:  assignment.New(db),
		loginLimiter: ratelimit.New(5, 5), // 5/min/IP on login specifically
	}
}

// Router builds the admin route tree. Every route except /login is behind
// requireSession; every POST is additionally behind verifyCSRF.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(secureHeaders)

	r.Get("/login", s.handleLoginForm)
	r.With(s.loginLimiter.Middleware(s.logger)).Post("/login", s.handleLoginSubmit)

	r.Group(func(r chi.Router) {
		r.Use(s.requireSession)

		r.Get("/", s.handleDashboard)
		r.With(s.verifyCSRF).Post("/logout", s.handleLogout)

		r.Get("/settings", s.handleSettingsList)
		r.With(s.verifyCSRF).Post("/settings/{key}", s.handleSettingsSave)

		r.Get("/applications", s.handleApplicationsList)
		r.Get("/applications/new", s.handleApplicationForm)
		r.With(s.verifyCSRF).Post("/applications", s.handleApplicationCreate)
		r.Get("/applications/{id}/edit", s.handleApplicationForm)
		r.With(s.verifyCSRF).Post("/applications/{id}", s.handleApplicationUpdate)
		r.With(s.verifyCSRF).Post("/applications/{id}/delete", s.handleApplicationDelete)

		r.Get("/themes", s.handleThemesList)
		r.Get("/themes/lookup", s.handleThemeLookup)
		r.Get("/themes/{slug}", s.handleThemeEdit)
		r.With(s.verifyCSRF).Post("/themes/{slug}", s.handleThemeMetaSave)
		r.Get("/themes/{slug}/files/lookup", s.handleThemeFileLookup)
		r.Get("/themes/{slug}/files/*", s.handleThemeFileEdit)
		r.With(s.verifyCSRF).Post("/themes/{slug}/files/*", s.handleThemeFileSave)
		r.With(s.verifyCSRF).Post("/themes/{slug}/files-delete/*", s.handleThemeFileDelete)

		r.Get("/templates", s.handleTemplatesList)
		r.Get("/templates/lookup", s.handleTemplateLookup)
		r.Get("/templates/{format}/{profile}/{protocol}", s.handleTemplateEdit)
		r.With(s.verifyCSRF).Post("/templates/{format}/{profile}/{protocol}", s.handleTemplateSave)
		r.With(s.verifyCSRF).Post("/templates/{format}/{profile}/{protocol}/delete", s.handleTemplateDelete)

		r.Get("/assignments", s.handleAssignmentsList)
		r.With(s.verifyCSRF).Post("/assignments", s.handleAssignmentSave)
		r.With(s.verifyCSRF).Post("/assignments/{subID}/delete", s.handleAssignmentDelete)
	})

	return r
}
