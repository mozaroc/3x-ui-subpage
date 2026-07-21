// Package clash generates Clash YAML subscriptions. Rendering mechanics are
// shared with Mihomo via yamlgen; this package exists as its own import
// path so callers can address Clash and Mihomo templates independently.
package clash

import (
	"database/sql"

	"github.com/irazin/3x-ui-subpage/internal/generator/yamlgen"
)

// Generator renders Clash YAML configs.
type Generator = yamlgen.Generator

// New builds a Generator backed by db.
func New(db *sql.DB) *Generator { return yamlgen.New(db, "clash") }
