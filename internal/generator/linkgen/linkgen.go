// Package linkgen builds Xray share-link URIs (vless://, vmess://,
// trojan://, ss://) from admin-editable templates stored in the shared
// database ("templates" table, format="xray_link"), and assembles them into
// the classic base64 subscription body most mobile clients (v2rayNG,
// Shadowrocket, NekoBox, ...) consume.
package linkgen

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplcache"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplfuncs"
)

const format = "xray_link"

// Generator renders share links for every supported protocol using
// per-(profile, protocol) templates loaded from the database, hot-reloaded
// on change, with missing profiles falling back to "default".
type Generator struct {
	cache *tmplcache.Cache[*template.Template]
}

// New builds a Generator backed by db.
func New(db *sql.DB) *Generator {
	cache := tmplcache.New(db, format, func(name, content string) (*template.Template, error) {
		return template.New(name).Funcs(tmplfuncs.FuncMap()).Parse(content)
	})
	return &Generator{cache: cache}
}

// BuildLink renders the share-link URI for a single matched client using
// its assigned profile. vmess is special-cased: its template produces the
// inner JSON object, which is then base64-encoded and wrapped as
// "vmess://...".
func (g *Generator) BuildLink(mc domain.MatchedClient, profile string) (string, error) {
	switch mc.Protocol {
	case domain.ProtocolVLESS, domain.ProtocolVMess, domain.ProtocolTrojan, domain.ProtocolShadowsocks:
	default:
		return "", fmt.Errorf("linkgen: unsupported protocol %q", mc.Protocol)
	}

	tmpl, err := g.cache.Get(profile, string(mc.Protocol))
	if err != nil {
		return "", fmt.Errorf("linkgen: load template for %s: %w", mc.Protocol, err)
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, tmplctx.FromMatchedClient(mc)); err != nil {
		return "", fmt.Errorf("linkgen: render %s: %w", mc.Protocol, err)
	}

	if mc.Protocol == domain.ProtocolVMess {
		// Unlike the other protocols' URI-query templates, vmess's template
		// body IS the payload (a JSON object, base64-wrapped) — a malformed
		// admin template or an unescaped field would otherwise silently
		// produce an unusable link with no error surfaced anywhere.
		if !json.Valid([]byte(sb.String())) {
			return "", fmt.Errorf("linkgen: render vmess: template output is not valid JSON")
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(sb.String()))
		return "vmess://" + encoded, nil
	}

	return sb.String(), nil
}

// BuildSubscription renders every matched client (using the subscriber's
// assigned profile) into a share link and joins them into the classic
// base64-encoded subscription body.
func (g *Generator) BuildSubscription(clients []domain.MatchedClient, profile string) (string, error) {
	var lines []string
	for _, mc := range clients {
		link, err := g.BuildLink(mc, profile)
		if err != nil {
			return "", err
		}
		lines = append(lines, link)
	}
	joined := strings.Join(lines, "\n")
	return base64.StdEncoding.EncodeToString([]byte(joined)), nil
}
