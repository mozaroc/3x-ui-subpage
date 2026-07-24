// Package httpserver wires the resolver, generators, QR renderer, theme
// engine, app catalog, and profile assignment store into an HTTP API. It is
// the only layer aware of net/http — everything it calls is plain,
// independently testable Go.
package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	"github.com/irazin/3x-ui-subpage/internal/apps"
	"github.com/irazin/3x-ui-subpage/internal/assignment"
	"github.com/irazin/3x-ui-subpage/internal/config"
	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/generator/clash"
	"github.com/irazin/3x-ui-subpage/internal/generator/happ"
	"github.com/irazin/3x-ui-subpage/internal/generator/incy"
	"github.com/irazin/3x-ui-subpage/internal/generator/mihomo"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
	"github.com/irazin/3x-ui-subpage/internal/generator/xrayjson"
	"github.com/irazin/3x-ui-subpage/internal/resolver"
	"github.com/irazin/3x-ui-subpage/internal/routing"
	"github.com/irazin/3x-ui-subpage/internal/theme"
)

// Resolver resolves a subscription token into a domain.Subscription.
type Resolver interface {
	Resolve(ctx context.Context, subID string) (domain.Subscription, error)
}

// XrayJSONGenerator builds the full xray-core client JSON config for a
// subscriber's assigned profile, from already-parsed canonical share links
// (see internal/generator/tmplctx).
type XrayJSONGenerator interface {
	Build(clients []tmplctx.ClientContext, profile string) (string, error)
}

// YAMLGenerator builds a Clash/Mihomo config for a subscriber's assigned
// profile (satisfied by both generator/clash.Generator and
// generator/mihomo.Generator).
type YAMLGenerator interface {
	Build(clients []tmplctx.ClientContext, profile string) (string, error)
}

// RawGenerator builds a config in a format this project asserts no
// authoritative schema for (Happ, Incy) — the admin-editable template owns
// the output bytes entirely.
type RawGenerator interface {
	Build(clients []tmplctx.ClientContext, profile string) (string, error)
}

// ThemeRenderer renders the HTML subscription page and serves the active
// theme's static assets. Satisfied by *theme.Engine.
type ThemeRenderer interface {
	Render(w io.Writer, data any) error
	ServeStatic(w http.ResponseWriter, r *http.Request, path string) (bool, error)
}

// AppCatalog lists the administrator-configured application catalog.
// Satisfied by *apps.Catalog.
type AppCatalog interface {
	List() ([]apps.App, error)
}

// AssignmentResolver resolves which template profile a subscriber is
// assigned to for a given "templates" format. Satisfied by
// *assignment.Store.
type AssignmentResolver interface {
	Resolve(subID, format string) (string, error)
}

// RoutingResolver resolves a subscriber's Happ/Incy Routing Profile.
// Satisfied by *routing.Store.
type RoutingResolver interface {
	Get(subID string) (enabled bool, profile routing.Profile, err error)
}

// Deps holds every collaborator the HTTP layer needs.
type Deps struct {
	Logger *slog.Logger

	Resolver    Resolver
	XrayJSON    XrayJSONGenerator
	Clash       YAMLGenerator
	Mihomo      YAMLGenerator
	Happ        RawGenerator
	Incy        RawGenerator
	Theme       ThemeRenderer
	ThemeSlug   string // active theme's slug, e.g. "default" — used to mount /assets/{slug}/...
	Apps        AppCatalog
	Assignments AssignmentResolver
	Routing     RoutingResolver

	QRDefaults config.QRConfig
	PublicURL  string
	Support    config.SupportConfig
	Security   config.SecurityConfig
}

// compile-time interface satisfaction checks for the concrete generator
// types, so a signature drift fails the build immediately rather than at
// wiring time in main.go.
var (
	_ XrayJSONGenerator  = (*xrayjson.Generator)(nil)
	_ YAMLGenerator      = (*clash.Generator)(nil)
	_ YAMLGenerator      = (*mihomo.Generator)(nil)
	_ RawGenerator       = (*happ.Generator)(nil)
	_ RawGenerator       = (*incy.Generator)(nil)
	_ Resolver           = (*resolver.Resolver)(nil)
	_ ThemeRenderer      = (*theme.Engine)(nil)
	_ AppCatalog         = (*apps.Catalog)(nil)
	_ AssignmentResolver = (*assignment.Store)(nil)
	_ RoutingResolver    = (*routing.Store)(nil)
)
