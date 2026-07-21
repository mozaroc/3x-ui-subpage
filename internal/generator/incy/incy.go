// Package incy generates subscriptions for the Incy client. Rendering
// mechanics are shared with Happ via rawgen; this package exists as its own
// import path so callers can address Incy templates independently. Incy has
// no publicly-documented wire format this project can assert as
// authoritative, so the admin-editable template fully owns the output
// bytes — verify against the installed client version.
package incy

import (
	"database/sql"

	"github.com/irazin/3x-ui-subpage/internal/generator/rawgen"
)

// Generator renders Incy configs.
type Generator = rawgen.Generator

// New builds a Generator backed by db.
func New(db *sql.DB) *Generator { return rawgen.New(db, "incy") }
