// Package rawgen implements the shared mechanics behind config formats
// whose exact wire syntax this project does not assert authoritative
// knowledge of (Happ, Incy): render an admin-editable template loaded
// per-profile from the shared database — the entire file, not just
// placeholders — with no validation of the result, since the target format
// is proprietary/undocumented and validating it as YAML or JSON would be
// presumptuous. The administrator's template is responsible for producing
// bytes their installed client version actually accepts.
package rawgen

import (
	"database/sql"
	"fmt"
	"strings"
	"text/template"

	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplcache"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplfuncs"
	"github.com/irazin/3x-ui-subpage/internal/routing"
)

// context is the data passed to the template.
type context struct {
	Clients []tmplctx.ClientContext
	Rules   []routing.Rule
}

// Generator renders a full client config from a template loaded
// per-profile, hot-reloaded on change, plus that profile's routing rules
// (falling back to the "default" profile independently for each).
type Generator struct {
	cache *tmplcache.Cache[*template.Template]
	rules *routing.Store
}

// New builds a Generator backed by db for the given format (e.g. "happ" or
// "incy").
func New(db *sql.DB, format string) *Generator {
	cache := tmplcache.New(db, format, func(name, content string) (*template.Template, error) {
		return template.New(name).Funcs(tmplfuncs.FuncMap()).Parse(content)
	})
	return &Generator{cache: cache, rules: routing.New(db)}
}

// Build renders the config for every matched client using the subscriber's
// assigned profile.
func (g *Generator) Build(clients []domain.MatchedClient, profile string) (string, error) {
	tmpl, err := g.cache.Get(profile, "")
	if err != nil {
		return "", fmt.Errorf("rawgen: load template: %w", err)
	}

	rules, err := g.rules.ForProfile(profile)
	if err != nil {
		return "", fmt.Errorf("rawgen: load routing rules: %w", err)
	}

	var sb strings.Builder
	ctx := context{Clients: tmplctx.FromMatchedClients(clients), Rules: rules}
	if err := tmpl.Execute(&sb, ctx); err != nil {
		return "", fmt.Errorf("rawgen: render: %w", err)
	}
	return sb.String(), nil
}
