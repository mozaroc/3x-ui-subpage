package xui

// apiResponse is the envelope every 3x-ui panel API endpoint responds with.
type apiResponse[T any] struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
	Obj     T      `json:"obj"`
}

// Inbound mirrors the JSON shape returned by GET /panel/api/inbounds/list.
// Settings and StreamSettings arrive as JSON-encoded strings, not nested
// objects, and are decoded separately (see parse.go).
type Inbound struct {
	ID             int          `json:"id"`
	Up             int64        `json:"up"`
	Down           int64        `json:"down"`
	Total          int64        `json:"total"`
	Remark         string       `json:"remark"`
	Enable         bool         `json:"enable"`
	ExpiryTime     int64        `json:"expiryTime"`
	Listen         string       `json:"listen"`
	Port           int          `json:"port"`
	Protocol       string       `json:"protocol"`
	Settings       string       `json:"settings"`
	StreamSettings string       `json:"streamSettings"`
	Tag            string       `json:"tag"`
	Sniffing       string       `json:"sniffing"`
	ClientStats    []ClientStat `json:"clientStats"`
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

// ClientPayload is one entry of settings.clients, a superset of every
// protocol's fields — unused fields are simply left zero-valued. Used both
// to decode the panel's client list (parse.go) and to build the settings
// body for write calls (addClient/updateClient, below), so read and write
// stay symmetric against one shape.
type ClientPayload struct {
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

// rawSettings is the decoded form of Inbound.Settings for protocols that
// carry a client list (vless, vmess, trojan, shadowsocks).
type rawSettings struct {
	Clients []ClientPayload `json:"clients"`
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
