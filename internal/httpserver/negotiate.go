package httpserver

import "strings"

// format identifies which representation of a subscription to serve.
type format int

const (
	formatXray format = iota
	formatClash
	formatMihomo
	formatIncy
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
//
// The real Happ app (User-Agent "Happ/<version>") is deliberately NOT
// routed to formatHapp here: confirmed against Happ's own subscription
// tooling, the stock app requests and imports the exact same base64
// share-link list as V2RayN/V2RayA/Clash/SingBox — the same thing every
// other unrecognized client gets by falling through to formatXray below.
// Routing it to the admin-editable JSON format instead (as this used to
// do) makes the app treat the response as an opaque file download rather
// than an importable subscription, since that JSON has no schema Happ
// itself actually parses by default. The dedicated /sub/{subId}/happ
// endpoint is still there for admins who explicitly want to experiment
// with that JSON format (e.g. via a custom app-catalog deeplink) — this
// only changes what a plain "add subscription by URL" gets automatically.
func detectFormat(userAgent string) format {
	ua := strings.ToLower(userAgent)

	switch {
	// "Clash-Meta" is mihomo's old name (the mihomo project was renamed
	// from Clash.Meta) -- plenty of real mihomo-core clients still send it
	// verbatim in their User-Agent for backward compat with providers that
	// only recognize "clash" (confirmed: Prizrak-Box, a mihomo-only GUI
	// client, sends "Clash-Meta/Prizrak-Box"). Must be checked before the
	// generic "clash" substring below, or every one of these gets the
	// original-Clash-dialect template instead of the mihomo one it
	// actually needs (missing tun/geodata-mode and other mihomo-only
	// fields it may rely on).
	case strings.Contains(ua, "mihomo"), strings.Contains(ua, "clash-meta"), strings.Contains(ua, "clash.meta"), strings.Contains(ua, "clashmeta"):
		return formatMihomo
	case strings.Contains(ua, "clash"), strings.Contains(ua, "stash"):
		return formatClash
	case strings.Contains(ua, "incy"):
		return formatIncy
	}

	for _, tok := range browserTokens {
		if strings.Contains(ua, tok) {
			return formatHTML
		}
	}

	return formatXray
}
