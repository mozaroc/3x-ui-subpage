package admin

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
)

// templateFS embeds the admin panel's own HTML — this is application code,
// not admin-editable content (unlike the subscriber-facing theme engine,
// which loads its templates from the database). Letting database content
// control the panel that administers the database would be a
// privilege-escalation footgun.
//
//go:embed templates/*.html
var templateFS embed.FS

var pageTemplates = template.Must(template.New("admin").ParseFS(templateFS, "templates/*.html"))

// PageData is passed to both the page-specific template and the shared
// layout — Body is filled in by render() after the page template runs, so
// page templates never set it themselves.
type PageData struct {
	Username  string
	CSRFToken string
	Body      template.HTML
	Data      any
}

// render executes the named page template ("page-login", "page-dashboard",
// ...) with pd, then wraps the result in the shared layout.
func render(w http.ResponseWriter, page string, pd PageData) error {
	var body bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&body, page, pd); err != nil {
		return fmt.Errorf("admin: render %s: %w", page, err)
	}
	pd.Body = template.HTML(body.String()) //nolint:gosec // server-generated, not user input

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplates.ExecuteTemplate(w, "layout", pd); err != nil {
		return fmt.Errorf("admin: render layout: %w", err)
	}
	return nil
}
