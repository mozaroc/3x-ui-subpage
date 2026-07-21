// Package happ generates subscriptions for the Happ client. Rendering
// mechanics are shared with Incy via rawgen; this package exists as its own
// import path so callers can address Happ templates independently. Happ has
// no publicly-documented wire format this project can assert as
// authoritative, so the admin-editable template fully owns the output
// bytes — verify against the installed client version.
package happ

import (
	"database/sql"

	"github.com/irazin/3x-ui-subpage/internal/generator/rawgen"
)

// Generator renders Happ configs.
type Generator = rawgen.Generator

// New builds a Generator backed by db.
func New(db *sql.DB) *Generator { return rawgen.New(db, "happ") }
