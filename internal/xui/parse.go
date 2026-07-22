package xui

import (
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

// decodeStreamSettings parses an inbound's StreamSettings JSON string into a
// normalized domain.StreamSettings.
func decodeStreamSettings(raw string) (domain.StreamSettings, error) {
	if raw == "" {
		return domain.StreamSettings{Network: domain.NetworkTCP, Security: domain.SecurityNone}, nil
	}

	var rs rawStreamSettings
	if err := json.Unmarshal([]byte(raw), &rs); err != nil {
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

// decodeClients parses an inbound's Settings JSON string into a client list.
// Protocols without a client list (e.g. dokodemo-door) simply yield none.
func decodeClients(raw string) ([]ClientPayload, error) {
	if raw == "" {
		return nil, nil
	}
	var s rawSettings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
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

		var matches []ClientPayload
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

// FindClient returns the client entry with the given email inside the
// inbound identified by inboundID, if any — used by the sync worker to
// detect a drifted/pre-existing client before deciding whether to add or
// update.
func FindClient(inbounds []Inbound, inboundID int, email string) (ClientPayload, bool, error) {
	for _, ib := range inbounds {
		if ib.ID != inboundID {
			continue
		}
		clients, err := decodeClients(ib.Settings)
		if err != nil {
			return ClientPayload{}, false, fmt.Errorf("inbound %d: %w", ib.ID, err)
		}
		for _, c := range clients {
			if c.Email == email {
				return c, true, nil
			}
		}
		return ClientPayload{}, false, nil
	}
	return ClientPayload{}, false, nil
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
