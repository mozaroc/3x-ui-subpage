// Package tmplfuncs provides the text/template FuncMap shared by every
// config generator (xray links, xray-core JSON, Clash, Mihomo), so admin
// templates can URL-encode, base64-encode, and join fields themselves.
package tmplfuncs

import (
	"encoding/base64"
	"net/url"
	"strings"
	"text/template"
)

// FuncMap returns the shared helper functions available inside generator
// templates.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"urlquery": url.QueryEscape,
		"b64":      func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
		"join":     strings.Join,
	}
}
