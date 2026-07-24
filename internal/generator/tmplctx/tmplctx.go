// Package tmplctx parses 3x-ui's own canonical share-link strings (as
// returned by the panel's /panel/api/clients/subLinks/{subId} endpoint)
// into the plain struct every config-generator template (Xray-core JSON,
// Clash, Mihomo, Happ, Incy) renders against. This project never
// reconstructs a share link itself — the panel's string is parsed, not
// rebuilt, so every connection parameter (including ones this project
// doesn't know about yet) survives intact.
package tmplctx

// ClientContext is the data made available to generator templates for one
// canonical share link.
type ClientContext struct {
	Protocol string
	UUID     string
	Password string
	Method   string
	// Email is not carried by a share link at all (it's an account-level
	// field, not a connection parameter) -- kept only so a custom
	// admin-authored template referencing .Email doesn't break; always "".
	Email  string
	Remark string

	Server string
	Port   int
	Flow   string

	Network  string
	Security string

	SNI         string
	ALPN        string // comma-joined, matching 3x-ui's own convention
	Fingerprint string
	Insecure    bool
	PublicKey   string
	ShortID     string
	SpiderX     string

	Path        string
	Host        string
	ServiceName string
	// HeaderType has no equivalent share-link query parameter to parse it
	// back from -- kept for template back-compat; always "".
	HeaderType string

	// RawParams holds every query parameter (vless/trojan/shadowsocks) or
	// JSON key (vmess) from the canonical link, verbatim and unfiltered.
	// This is the forward-compatibility mechanism: any parameter 3x-ui
	// adds tomorrow (xhttp's mode/extra/x_padding_bytes, ECH's ech,
	// pinned-cert's pcs, whatever comes next) is reachable from a template
	// as e.g. {{.RawParams.extra}} with zero code changes here.
	RawParams map[string]string
}

// Entry pairs one panel-provided canonical share link with its parsed
// ClientContext. Raw is always present, even when ParseErr != nil — a
// caller that only needs the verbatim string (subscription body,
// direct-link display, QR) never loses it just because this project's
// parser doesn't yet understand some new link shape.
type Entry struct {
	Raw      string
	Context  ClientContext
	ParseErr error
}

// ParseEntries parses every link, never dropping one even on failure.
func ParseEntries(links []string) []Entry {
	out := make([]Entry, len(links))
	for i, link := range links {
		cc, err := ParseShareLink(link)
		out[i] = Entry{Raw: link, Context: cc, ParseErr: err}
	}
	return out
}
