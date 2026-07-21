// Package mihomo generates Mihomo YAML subscriptions. Rendering mechanics
// are shared with Clash via yamlgen; this package exists as its own import
// path so callers can address Clash and Mihomo templates independently,
// since Mihomo's proxy dialect (smux, TUN fields) diverges from Clash's
// even though the overall shape matches.
package mihomo

import (
	"database/sql"

	"github.com/irazin/3x-ui-subpage/internal/generator/yamlgen"
)

// Generator renders Mihomo YAML configs.
type Generator = yamlgen.Generator

// New builds a Generator backed by db.
func New(db *sql.DB) *Generator { return yamlgen.New(db, "mihomo") }
