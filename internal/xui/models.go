package xui

import "encoding/json"

// apiResponse is the envelope every 3x-ui panel API endpoint responds with.
type apiResponse[T any] struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
	Obj     T      `json:"obj"`
}

// Inbound mirrors the JSON shape returned by GET /panel/api/inbounds/list.
// Settings and StreamSettings vary by panel version — some serve them as
// JSON-encoded strings (vanilla 3x-ui convention), others as nested JSON
// objects directly (confirmed against a live 3.5.0 instance) — so both
// fields are kept as raw JSON and decoded flexibly (see parse.go).
type Inbound struct {
	ID             int             `json:"id"`
	Up             int64           `json:"up"`
	Down           int64           `json:"down"`
	Total          int64           `json:"total"`
	Remark         string          `json:"remark"`
	Enable         bool            `json:"enable"`
	ExpiryTime     int64           `json:"expiryTime"`
	Listen         string          `json:"listen"`
	Port           int             `json:"port"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings"`
	StreamSettings json.RawMessage `json:"streamSettings"`
	Tag            string          `json:"tag"`
	Sniffing       json.RawMessage `json:"sniffing"`
	ClientStats    []ClientStat    `json:"clientStats"`
}

// ClientStat is per-client live traffic/expiry data 3x-ui embeds directly in
// the inbound list response (keyed by email, not subId/uuid).
type ClientStat struct {
	Email      string `json:"email"`
	Enable     bool   `json:"enable"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	Total      int64  `json:"total"`
	ExpiryTime int64  `json:"expiryTime"`
}

// EmbeddedClient is one entry of an inbound's settings.clients array (the
// read path only — decoded from Inbound.Settings by decodeClients). Its "id"
// field is the client's uuid/protocol-id as a string, matching the classic
// per-inbound client-list shape every 3x-ui version serves this data in,
// even on panels whose separate client-management API (ManagedClient,
// below) uses different field names for the same concepts.
type EmbeddedClient struct {
	ID         string `json:"id"`
	Password   string `json:"password"`
	Method     string `json:"method"`
	Flow       string `json:"flow"`
	Email      string `json:"email"`
	SubID      string `json:"subId"`
	Enable     bool   `json:"enable"`
	TotalGB    int64  `json:"totalGB"`
	ExpiryTime int64  `json:"expiryTime"`
}

// ManagedClient is the request/response shape for the panel's client
// management API (/panel/api/clients/*) — confirmed against a live 3.5.0
// instance. Unlike EmbeddedClient, the protocol uuid is carried in a
// separate "uuid" field ("id" here is the panel's own numeric row id,
// returned on read but never sent on write). Updates replace the full row,
// not a patch — any client field this project doesn't track (limitIp, tgId,
// group, comment, and the WireGuard/Hysteria-specific fields) resets to its
// zero value on every UpdateClient call, which is consistent with this
// service being the source of truth for clients it manages.
type ManagedClient struct {
	ID         int    `json:"id,omitempty"`
	Email      string `json:"email"`
	SubID      string `json:"subId,omitempty"`
	UUID       string `json:"uuid,omitempty"`
	Password   string `json:"password,omitempty"`
	Flow       string `json:"flow,omitempty"`
	Enable     bool   `json:"enable"`
	TotalGB    int64  `json:"totalGB"`
	ExpiryTime int64  `json:"expiryTime"`
}

// rawSettings is the decoded form of Inbound.Settings for protocols that
// carry a client list (vless, vmess, trojan, shadowsocks).
type rawSettings struct {
	Clients []EmbeddedClient `json:"clients"`
}

// rawStreamSettings is the decoded form of Inbound.StreamSettings.
type rawStreamSettings struct {
	Network  string `json:"network"`
	Security string `json:"security"`

	TLSSettings struct {
		ServerName string   `json:"serverName"`
		Alpn       []string `json:"alpn"`
		Settings   struct {
			Fingerprint string `json:"fingerprint"`
		} `json:"settings"`
	} `json:"tlsSettings"`

	RealitySettings struct {
		ServerNames []string `json:"serverNames"`
		ShortIds    []string `json:"shortIds"`
		Settings    struct {
			PublicKey   string `json:"publicKey"`
			Fingerprint string `json:"fingerprint"`
			SpiderX     string `json:"spiderX"`
		} `json:"settings"`
	} `json:"realitySettings"`

	TCPSettings struct {
		Header struct {
			Type string `json:"type"`
		} `json:"header"`
	} `json:"tcpSettings"`

	WSSettings struct {
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
	} `json:"wsSettings"`

	GRPCSettings struct {
		ServiceName string `json:"serviceName"`
	} `json:"grpcSettings"`
}
