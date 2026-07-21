// Package xrayjson generates a full xray-core client JSON configuration
// (for users running the standalone xray-core binary) from a single
// admin-editable template stored in the shared database ("templates" table,
// format="xray_json"), hot-reloaded on change.
package xrayjson

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplcache"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplfuncs"
)

const format = "xray_json"

// context is the data passed to the full-config template.
type context struct {
	Clients []tmplctx.ClientContext
}

// Generator renders the full xray-core client JSON config from a template
// loaded per-profile, hot-reloaded on change.
type Generator struct {
	cache *tmplcache.Cache
}

// New builds a Generator backed by db.
func New(db *sql.DB) *Generator {
	cache := tmplcache.New(db, format, func(name, content string) (*template.Template, error) {
		return template.New(name).Funcs(tmplfuncs.FuncMap()).Parse(content)
	})
	return &Generator{cache: cache}
}

// Build renders the full config for every matched client using the
// subscriber's assigned profile, and validates the result is well-formed
// JSON before returning it.
func (g *Generator) Build(clients []domain.MatchedClient, profile string) (string, error) {
	tmpl, err := g.cache.Get(profile, "")
	if err != nil {
		return "", fmt.Errorf("xrayjson: load template: %w", err)
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, context{Clients: tmplctx.FromMatchedClients(clients)}); err != nil {
		return "", fmt.Errorf("xrayjson: render: %w", err)
	}

	out := sb.String()
	if !json.Valid([]byte(out)) {
		return "", fmt.Errorf("xrayjson: rendered template is not valid JSON")
	}
	return out, nil
}
