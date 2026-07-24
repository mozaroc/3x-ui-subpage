// Package tmplfuncs provides the text/template FuncMap shared by every
// config generator (xray links, xray-core JSON, Clash, Mihomo), so admin
// templates can URL-encode, base64-encode, and join fields themselves.
package tmplfuncs

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"text/template"
)

// FuncMap returns the shared helper functions available inside generator
// templates.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"urlquery":  url.QueryEscape,
		"b64":       func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
		"join":      strings.Join,
		"jsonstr":   jsonString,
		"splitJSON": splitJSON,
	}
}

// jsonString renders s as a properly-escaped, double-quoted JSON string
// literal (quotes included) — for interpolating arbitrary field values
// (remarks, passwords, SNI, ...) into a JSON-format template without a
// stray '"' or '\' in the value breaking the surrounding document.
// json.Marshal of a string never fails.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// splitJSON splits s on sep and renders the result as a JSON string array
// (e.g. for a comma-joined ALPN list) — "" renders as "[]", never [""].
func splitJSON(sep, s string) string {
	if s == "" {
		return "[]"
	}
	b, _ := json.Marshal(strings.Split(s, sep))
	return string(b)
}
