package xui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

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

// selectHostForInbound returns the Host group that applies to inboundID —
// the lowest-sortOrder group (among those listing inboundID) that isn't
// disabled or hidden — or false if none applies. Deliberately picks exactly
// one deterministic winner rather than merging multiple groups, the same
// "one clear fallback" convention this project already uses elsewhere
// (routing/assignment/tmplcache profile fallback).
func selectHostForInbound(groups []HostGroup, inboundID int) (HostGroup, bool) {
	var best HostGroup
	found := false
	for _, g := range groups {
		if g.IsDisabled || g.IsHidden {
			continue
		}
		applies := false
		for _, id := range g.InboundIDs {
			if id == inboundID {
				applies = true
				break
			}
		}
		if !applies {
			continue
		}
		if !found || g.SortOrder < best.SortOrder {
			best = g
			found = true
		}
	}
	return best, found
}

// parseHostAddress splits a "host:port" (or bare "host") Hosts[] entry.
// fallbackPort is used when the entry carries no port of its own.
func parseHostAddress(entry string, fallbackPort int) (addr string, port int) {
	h, p, err := net.SplitHostPort(entry)
	if err != nil {
		return entry, fallbackPort
	}
	if parsed, err := strconv.Atoi(p); err == nil {
		return h, parsed
	}
	return h, fallbackPort
}

// applyHost overrides mc's connection fields with host's, per the rules
// confirmed against a live 3x-ui 3.5.0 instance: security "same" means
// "don't touch the inbound's own security"; every other field overrides
// only when the Host actually sets something (a zero value means "inherit
// the inbound's own value"). Reality's crypto fields (PublicKey/ShortID/
// SpiderX) are never part of a Host and are left untouched unconditionally.
func applyHost(mc *domain.MatchedClient, host HostGroup) {
	if len(host.Hosts) > 0 {
		mc.Server, mc.Port = parseHostAddress(host.Hosts[0], firstNonZero(host.Port, mc.Port))
	}
	if host.Security != "" && host.Security != "same" {
		mc.Stream.Security = domain.Security(host.Security)
	}
	if host.SNI != "" {
		mc.Stream.TLS.SNI = host.SNI
	}
	if len(host.ALPN) > 0 {
		mc.Stream.TLS.ALPN = host.ALPN
	}
	if host.Fingerprint != "" {
		mc.Stream.TLS.Fingerprint = host.Fingerprint
	}
	if host.Path != "" {
		mc.Stream.Transport.Path = host.Path
	}
	if host.HostHeader != "" {
		mc.Stream.Transport.Host = host.HostHeader
	}
	mc.Stream.TLS.Insecure = host.AllowInsecure
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

// MatchedClientsBySubID scans every inbound's client list and returns one
// domain.MatchedClient per client whose SubID equals subID, across all
// inbounds — mirroring 3x-ui's own multi-inbound subscription merge.
// hostGroups (from GET /panel/api/hosts/list) overrides each match's
// connection fields when a Host applies to that client's inbound — pass
// nil if Hosts aren't available (a fetch failure degrades gracefully to
// inbound-derived connection info, not a hard failure).
func MatchedClientsBySubID(inbounds []Inbound, hostGroups []HostGroup, subID, fallbackHost string) ([]domain.MatchedClient, error) {
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

		connectAddr := connectHost(ib, fallbackHost)
		hostGroup, hasHostGroup := selectHostForInbound(hostGroups, ib.ID)
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

			mc := domain.MatchedClient{
				InboundID: ib.ID,
				Tag:       ib.Tag,
				Remark:    ib.Remark,
				Protocol:  proto,
				Server:    connectAddr,
				Port:      ib.Port,
				Client:    account,
				Stream:    stream,
			}
			if hasHostGroup {
				applyHost(&mc, hostGroup)
			}
			out = append(out, mc)
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
