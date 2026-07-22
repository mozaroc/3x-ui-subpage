package xui

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

// wildcardHosts are "listen" values that cannot be used as a client connect
// address; callers must substitute a configured fallback host instead.
var wildcardHosts = map[string]bool{
	"":        true,
	"0.0.0.0": true,
	"::":      true,
}

// connectHost returns the address clients should dial for this inbound.
func connectHost(inbound Inbound, fallback string) string {
	if wildcardHosts[inbound.Listen] {
		return fallback
	}
	return inbound.Listen
}

// unmarshalFlexible decodes raw into out, whether raw holds the target value
// directly (a JSON object, this project's own 3.5.0 test panel's
// convention) or as a JSON string containing that value's JSON text
// (double-encoded — vanilla 3x-ui's convention, and possibly other forks).
// Empty/null input leaves out untouched.
func unmarshalFlexible(raw json.RawMessage, out any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		if s == "" {
			return nil
		}
		return json.Unmarshal([]byte(s), out)
	}
	return json.Unmarshal(trimmed, out)
}

// decodeStreamSettings parses an inbound's StreamSettings into a normalized
// domain.StreamSettings.
func decodeStreamSettings(raw json.RawMessage) (domain.StreamSettings, error) {
	var rs rawStreamSettings
	if err := unmarshalFlexible(raw, &rs); err != nil {
		return domain.StreamSettings{}, fmt.Errorf("decode streamSettings: %w", err)
	}

	ss := domain.StreamSettings{
		Network:  domain.Network(rs.Network),
		Security: domain.Security(rs.Security),
	}
	if ss.Network == "" {
		ss.Network = domain.NetworkTCP
	}
	if ss.Security == "" {
		ss.Security = domain.SecurityNone
	}

	switch ss.Security {
	case domain.SecurityTLS:
		ss.TLS.SNI = rs.TLSSettings.ServerName
		ss.TLS.ALPN = rs.TLSSettings.Alpn
		ss.TLS.Fingerprint = rs.TLSSettings.Settings.Fingerprint
	case domain.SecurityReality:
		if len(rs.RealitySettings.ServerNames) > 0 {
			ss.TLS.SNI = rs.RealitySettings.ServerNames[0]
		}
		if len(rs.RealitySettings.ShortIds) > 0 {
			ss.TLS.ShortID = rs.RealitySettings.ShortIds[0]
		}
		ss.TLS.PublicKey = rs.RealitySettings.Settings.PublicKey
		ss.TLS.Fingerprint = rs.RealitySettings.Settings.Fingerprint
		ss.TLS.SpiderX = rs.RealitySettings.Settings.SpiderX
	}

	switch ss.Network {
	case domain.NetworkTCP:
		ss.Transport.HeaderType = rs.TCPSettings.Header.Type
	case domain.NetworkWS:
		ss.Transport.Path = rs.WSSettings.Path
		ss.Transport.Host = rs.WSSettings.Headers["Host"]
	case domain.NetworkGRPC:
		ss.Transport.ServiceName = rs.GRPCSettings.ServiceName
	}

	return ss, nil
}

// decodeClients parses an inbound's Settings into a client list. Protocols
// without a client list (e.g. dokodemo-door) simply yield none.
func decodeClients(raw json.RawMessage) ([]EmbeddedClient, error) {
	var s rawSettings
	if err := unmarshalFlexible(raw, &s); err != nil {
		return nil, fmt.Errorf("decode settings: %w", err)
	}
	return s.Clients, nil
}

// MatchedClientsBySubID scans every inbound's client list and returns one
// domain.MatchedClient per client whose SubID equals subID, across all
// inbounds — mirroring 3x-ui's own multi-inbound subscription merge.
func MatchedClientsBySubID(inbounds []Inbound, subID, fallbackHost string) ([]domain.MatchedClient, error) {
	var out []domain.MatchedClient

	for _, ib := range inbounds {
		if !ib.Enable {
			continue
		}

		proto := domain.Protocol(ib.Protocol)
		switch proto {
		case domain.ProtocolVLESS, domain.ProtocolVMess, domain.ProtocolTrojan, domain.ProtocolShadowsocks:
		default:
			continue // unsupported protocol for client-based subscriptions
		}

		clients, err := decodeClients(ib.Settings)
		if err != nil {
			return nil, fmt.Errorf("inbound %d: %w", ib.ID, err)
		}

		var matches []EmbeddedClient
		for _, c := range clients {
			if c.SubID == subID {
				matches = append(matches, c)
			}
		}
		if len(matches) == 0 {
			continue
		}

		stream, err := decodeStreamSettings(ib.StreamSettings)
		if err != nil {
			return nil, fmt.Errorf("inbound %d: %w", ib.ID, err)
		}

		host := connectHost(ib, fallbackHost)
		stats := statsByEmail(ib.ClientStats)

		for _, c := range matches {
			account := domain.ClientAccount{
				ID:       c.ID,
				Password: c.Password,
				Method:   c.Method,
				Email:    c.Email,
				SubID:    c.SubID,
				Flow:     c.Flow,
				Enable:   c.Enable,
				TotalGB:  c.TotalGB,
				ExpiryMs: c.ExpiryTime,
			}
			if st, ok := stats[c.Email]; ok {
				account.Up = st.Up
				account.Down = st.Down
				if account.TotalGB == 0 {
					account.TotalGB = st.Total
				}
				if account.ExpiryMs == 0 {
					account.ExpiryMs = st.ExpiryTime
				}
			}

			out = append(out, domain.MatchedClient{
				InboundID: ib.ID,
				Tag:       ib.Tag,
				Remark:    ib.Remark,
				Protocol:  proto,
				Server:    host,
				Port:      ib.Port,
				Client:    account,
				Stream:    stream,
			})
		}
	}

	return out, nil
}

// statsByEmail indexes an inbound's ClientStats by email for O(1) lookup
// while merging traffic/expiry into each matched client.
func statsByEmail(stats []ClientStat) map[string]ClientStat {
	m := make(map[string]ClientStat, len(stats))
	for _, s := range stats {
		m[s.Email] = s
	}
	return m
}
