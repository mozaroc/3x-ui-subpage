// Package yamlgen implements the shared mechanics behind the Clash and
// Mihomo generators. The admin-editable template loaded per-profile from
// the shared database is plain, static YAML — proxy-groups, rules, dns,
// tun, whatever — with no template programming. yamlgen generates every
// client's proxy definition internally, inserts them under the template's
// "proxies" key, and auto-populates every "proxy-groups" entry, following
// Remnawave's own documented `remnawave:` keys (include-proxies,
// select-random-proxy, shuffle-proxies-order) plus the native Clash-Meta
// filter/exclude-filter keys, so existing Remnawave Mihomo templates work
// with little or no modification. See inject.go for the mechanics. The two
// client dialects differ only in which "format" they're stored under, not
// in how injection works.
package yamlgen

import (
	"database/sql"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplcache"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
)

// Generator renders a full Clash/Mihomo config from a template loaded
// per-profile, hot-reloaded on change.
type Generator struct {
	cache *tmplcache.Cache[string]
}

// New builds a Generator backed by db for the given format ("clash" or
// "mihomo").
func New(db *sql.DB, format string) *Generator {
	cache := tmplcache.New(db, format, func(_, content string) (string, error) {
		var probe any
		if err := yaml.Unmarshal([]byte(content), &probe); err != nil {
			return "", fmt.Errorf("template is not valid YAML: %w", err)
		}
		return content, nil
	})
	return &Generator{cache: cache}
}

// Build renders the config for every matched client using the subscriber's
// assigned profile: generates every client's proxy entry, injects them
// into the template's "proxies" key, and auto-populates every
// "proxy-groups" entry, then validates the result parses as YAML before
// returning it.
func (g *Generator) Build(clients []domain.MatchedClient, profile string) (string, error) {
	content, err := g.cache.Get(profile, "")
	if err != nil {
		return "", fmt.Errorf("yamlgen: load template: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", fmt.Errorf("yamlgen: parse template: %w", err)
	}

	if err := injectClash(&doc, tmplctx.FromMatchedClients(clients)); err != nil {
		return "", fmt.Errorf("yamlgen: inject proxies: %w", err)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", fmt.Errorf("yamlgen: render: %w", err)
	}

	var probe any
	if err := yaml.Unmarshal(out, &probe); err != nil {
		return "", fmt.Errorf("yamlgen: rendered template is not valid YAML: %w", err)
	}
	return string(out), nil
}
