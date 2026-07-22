// Package domain holds core types shared across the service, independent of
// how they were obtained (3x-ui API) or how they will be rendered (HTML,
// Clash/Mihomo YAML, Xray share links).
package domain

import "time"

// Protocol identifies an Xray inbound protocol.
type Protocol string

const (
	ProtocolVLESS       Protocol = "vless"
	ProtocolVMess       Protocol = "vmess"
	ProtocolTrojan      Protocol = "trojan"
	ProtocolShadowsocks Protocol = "shadowsocks"
)

// Security identifies the stream security layer of an inbound.
type Security string

const (
	SecurityNone    Security = "none"
	SecurityTLS     Security = "tls"
	SecurityReality Security = "reality"
)

// Network identifies the Xray transport.
type Network string

const (
	NetworkTCP  Network = "tcp"
	NetworkKCP  Network = "kcp"
	NetworkWS   Network = "ws"
	NetworkHTTP Network = "http"
	NetworkGRPC Network = "grpc"
)

// TLSSettings carries the fields needed to build TLS/Reality share links and
// config templates. Fields are populated selectively depending on Security.
type TLSSettings struct {
	SNI         string
	ALPN        []string
	Fingerprint string
	Insecure    bool // skip certificate verification (3x-ui Host "allowInsecure")

	// Reality-only fields.
	PublicKey string
	ShortID   string
	SpiderX   string
}

// TransportSettings carries transport-specific fields (ws path/host, grpc
// service name, etc.) needed by templates. Only the relevant fields are set
// for a given Network.
type TransportSettings struct {
	Path        string // ws/http path
	Host        string // ws/http Host header
	ServiceName string // grpc serviceName
	HeaderType  string // tcp/kcp header type ("none", "http", ...)
}

// StreamSettings is the parsed form of an inbound's streamSettings JSON.
type StreamSettings struct {
	Network   Network
	Security  Security
	TLS       TLSSettings
	Transport TransportSettings
}

// ClientAccount is one client entry inside an inbound's settings.clients
// array, normalized across protocols. Not every field applies to every
// protocol (e.g. Password is shadowsocks/trojan only).
type ClientAccount struct {
	ID       string // uuid (vless/vmess) or empty
	Password string // trojan/shadowsocks
	Method   string // shadowsocks cipher
	Email    string
	SubID    string
	Flow     string // vless flow control
	Enable   bool
	TotalGB  int64 // 0 = unlimited
	ExpiryMs int64 // 0 = never, epoch millis
	Up       int64
	Down     int64
}

// MatchedClient pairs an inbound with the specific client account inside it
// that matched a subscription token, plus everything needed to render a
// share link / config for that single proxy entry.
type MatchedClient struct {
	InboundID int
	Tag       string
	Remark    string
	Protocol  Protocol
	Server    string
	Port      int
	Client    ClientAccount
	Stream    StreamSettings
}

// TrafficStats aggregates usage across all of a subscriber's matched
// clients.
type TrafficStats struct {
	Up    int64
	Down  int64
	Total int64 // 0 = unlimited
}

// Used returns total bytes consumed (up+down).
func (t TrafficStats) Used() int64 { return t.Up + t.Down }

// Remaining returns bytes left before the quota is hit. Returns -1 when the
// quota is unlimited (Total == 0).
func (t TrafficStats) Remaining() int64 {
	if t.Total == 0 {
		return -1
	}
	r := t.Total - t.Used()
	if r < 0 {
		return 0
	}
	return r
}

// Status describes the overall subscriber account state.
type Status string

const (
	StatusActive   Status = "active"
	StatusExpired  Status = "expired"
	StatusDepleted Status = "depleted"
	StatusDisabled Status = "disabled"
)

// Subscription is the fully resolved view of a subscriber, ready to be
// handed to any renderer (HTML page, Clash/Mihomo YAML, Xray link list).
type Subscription struct {
	SubID     string
	Username  string
	Status    Status
	ExpiresAt *time.Time // nil = never
	Traffic   TrafficStats
	Clients   []MatchedClient
}
