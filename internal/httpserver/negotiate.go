package httpserver

import "strings"

// format identifies which representation of a subscription to serve.
type format int

const (
	formatXray format = iota
	formatClash
	formatMihomo
	formatHTML
)

// browserTokens are User-Agent substrings that indicate a real web browser
// (as opposed to a proxy client spoofing a generic UA).
var browserTokens = []string{"mozilla", "chrome", "safari", "firefox", "edg/", "opr/"}

// detectFormat picks a response format from the request's User-Agent,
// mirroring the convention used by 3x-ui's own subscription server and
// Remnawave: known client cores get their native config format, browsers
// get the HTML page, everything else falls back to the classic base64
// Xray link subscription.
func detectFormat(userAgent string) format {
	ua := strings.ToLower(userAgent)

	switch {
	case strings.Contains(ua, "mihomo"):
		return formatMihomo
	case strings.Contains(ua, "clash"), strings.Contains(ua, "stash"):
		return formatClash
	}

	for _, tok := range browserTokens {
		if strings.Contains(ua, tok) {
			return formatHTML
		}
	}

	return formatXray
}
