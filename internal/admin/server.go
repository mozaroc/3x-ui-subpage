// Package admin implements the server-rendered admin panel: login/session
// management and full CRUD over settings, the application catalog, themes,
// generator templates, and per-user template assignments (set directly on
// the User pages -- see handlers_users.go). Mounted at /admin by
// cmd/subscription-service.
//
// Unlike the subscriber-facing theme engine, this package's own HTML is
// //go:embed'ded application code, not admin-editable database content —
// see render.go.
package admin

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/adminauth"
	"github.com/irazin/3x-ui-subpage/internal/apps"
	"github.com/irazin/3x-ui-subpage/internal/assignment"
	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/ratelimit"
	"github.com/irazin/3x-ui-subpage/internal/routing"
	"github.com/irazin/3x-ui-subpage/internal/sync"
	"github.com/irazin/3x-ui-subpage/internal/templatestore"
	"github.com/irazin/3x-ui-subpage/internal/theme"
	"github.com/irazin/3x-ui-subpage/internal/users"
	"github.com/irazin/3x-ui-subpage/internal/xui"
)

// SubscriptionResolver is the subset of *resolver.Resolver the admin Users
// pages need — live traffic/status/expiry for display, reusing exactly what
// the public subscription page already computes instead of duplicating it.
type SubscriptionResolver interface {
	Resolve(ctx context.Context, subID string) (domain.Subscription, error)
}

// Server holds every collaborator the admin panel needs.
type Server struct {
	db     *sql.DB
	logger *slog.Logger

	auth        *adminauth.Store
	apps        *apps.Catalog
	themes      *theme.AdminStore
	templates   *templatestore.Store
	assignments *assignment.Store
	routing     *routing.Store
	users       *users.Store
	syncJobs    *sync.Store
	inbounds    xui.InboundLister
	resolve     SubscriptionResolver
	publicURL   string

	loginLimiter *ratelimit.Limiter
}

// New builds a Server. db backs every store directly (settings read/write
// goes through internal/config functions called with the same db). inbounds
// and resolve are typically the same xui.CachedLister / resolver.Resolver
// the public-facing httpserver already uses, shared rather than duplicated.
// publicURL is the subscription service's own public base URL
// (Settings -> subscription -> public_url), used to build the full
// subscription link shown on a user's detail page.
func New(db *sql.DB, logger *slog.Logger, usersStore *users.Store, syncStore *sync.Store, inbounds xui.InboundLister, resolve SubscriptionResolver, publicURL string) *Server {
	return &Server{
		db:           db,
		logger:       logger,
		auth:         adminauth.New(db),
		apps:         apps.New(db),
		themes:       theme.NewAdminStore(db),
		templates:    templatestore.New(db),
		assignments:  assignment.New(db),
		routing:      routing.New(db),
		users:        usersStore,
		syncJobs:     syncStore,
		inbounds:     inbounds,
		resolve:      resolve,
		publicURL:    publicURL,
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

		r.Get("/routing", s.handleRoutingList)
		r.Get("/routing/new", s.handleRoutingForm)
		r.With(s.verifyCSRF).Post("/routing", s.handleRoutingCreate)
		r.Get("/routing/{id}/edit", s.handleRoutingForm)
		r.With(s.verifyCSRF).Post("/routing/{id}", s.handleRoutingUpdate)
		r.With(s.verifyCSRF).Post("/routing/{id}/delete", s.handleRoutingDelete)

		r.Get("/users", s.handleUsersList)
		r.Get("/users/new", s.handleUserForm)
		r.With(s.verifyCSRF).Post("/users", s.handleUserCreate)
		r.With(s.verifyCSRF).Post("/users/bulk", s.handleUsersBulk)
		r.Get("/users/{id}", s.handleUserDetail)
		r.With(s.verifyCSRF).Post("/users/{id}", s.handleUserUpdate)
		r.With(s.verifyCSRF).Post("/users/{id}/delete", s.handleUserDelete)
		r.With(s.verifyCSRF).Post("/users/{id}/toggle", s.handleUserToggle)
		r.With(s.verifyCSRF).Post("/users/{id}/reset-traffic", s.handleUserResetTraffic)
		r.With(s.verifyCSRF).Post("/users/{id}/regenerate-uuid", s.handleUserRegenerateUUID)
		r.With(s.verifyCSRF).Post("/users/{id}/inbounds", s.handleUserSetInbounds)

		r.Get("/sync", s.handleSyncLog)
		r.With(s.verifyCSRF).Post("/sync/{id}/retry", s.handleSyncRetry)
	})

	return r
}
