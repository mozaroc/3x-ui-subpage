// Package yamlgen implements the shared mechanics behind the Clash and
// Mihomo generators: render an admin-editable template (proxies,
// proxy-groups, rules — the entire file, not just placeholders) loaded
// per-profile from the shared database, and validate the result is
// well-formed YAML. The two client dialects differ only in which "format"
// they're stored under, not in how rendering works.
package yamlgen

import (
	"database/sql"
	"fmt"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplcache"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplfuncs"
)

// context is the data passed to the YAML template.
type context struct {
	Clients []tmplctx.ClientContext
}

// Generator renders a full Clash/Mihomo config from a template loaded
// per-profile, hot-reloaded on change.
type Generator struct {
	cache *tmplcache.Cache
}

// New builds a Generator backed by db for the given format ("clash" or
// "mihomo").
func New(db *sql.DB, format string) *Generator {
	cache := tmplcache.New(db, format, func(name, content string) (*template.Template, error) {
		return template.New(name).Funcs(tmplfuncs.FuncMap()).Parse(content)
	})
	return &Generator{cache: cache}
}

// Build renders the config for every matched client using the subscriber's
// assigned profile, and validates the result parses as YAML before
// returning it.
func (g *Generator) Build(clients []domain.MatchedClient, profile string) (string, error) {
	tmpl, err := g.cache.Get(profile, "")
	if err != nil {
		return "", fmt.Errorf("yamlgen: load template: %w", err)
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, context{Clients: tmplctx.FromMatchedClients(clients)}); err != nil {
		return "", fmt.Errorf("yamlgen: render: %w", err)
	}

	out := sb.String()
	var probe any
	if err := yaml.Unmarshal([]byte(out), &probe); err != nil {
		return "", fmt.Errorf("yamlgen: rendered template is not valid YAML: %w", err)
	}
	return out, nil
}
