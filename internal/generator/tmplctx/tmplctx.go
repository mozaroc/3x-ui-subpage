// Package tmplctx flattens a domain.MatchedClient into the plain struct
// every config-generator template (Xray links, Xray-core JSON, Clash,
// Mihomo) renders against, so the field set stays identical no matter which
// output format an administrator is editing.
package tmplctx

import (
	"strings"

	"github.com/irazin/3x-ui-subpage/internal/domain"
)

// ClientContext is the data made available to generator templates for one
// matched client.
type ClientContext struct {
	Protocol string
	UUID     string
	Password string
	Method   string
	Email    string
	Remark   string

	Server string
	Port   int
	Flow   string

	Network  string
	Security string

	SNI         string
	ALPN        string // comma-joined
	Fingerprint string
	Insecure    bool
	PublicKey   string
	ShortID     string
	SpiderX     string

	Path        string
	Host        string
	ServiceName string
	HeaderType  string
}

// FromMatchedClient flattens mc into a ClientContext.
func FromMatchedClient(mc domain.MatchedClient) ClientContext {
	remark := combineName(mc.Remark, mc.Client.Email)
	return ClientContext{
		Protocol:    string(mc.Protocol),
		UUID:        mc.Client.ID,
		Password:    mc.Client.Password,
		Method:      mc.Client.Method,
		Email:       mc.Client.Email,
		Remark:      remark,
		Server:      mc.Server,
		Port:        mc.Port,
		Flow:        mc.Client.Flow,
		Network:     string(mc.Stream.Network),
		Security:    string(mc.Stream.Security),
		SNI:         mc.Stream.TLS.SNI,
		ALPN:        strings.Join(mc.Stream.TLS.ALPN, ","),
		Fingerprint: mc.Stream.TLS.Fingerprint,
		Insecure:    mc.Stream.TLS.Insecure,
		PublicKey:   mc.Stream.TLS.PublicKey,
		ShortID:     mc.Stream.TLS.ShortID,
		SpiderX:     mc.Stream.TLS.SpiderX,
		Path:        mc.Stream.Transport.Path,
		Host:        mc.Stream.Transport.Host,
		ServiceName: mc.Stream.Transport.ServiceName,
		HeaderType:  mc.Stream.Transport.HeaderType,
	}
}

// FromMatchedClients flattens a slice.
func FromMatchedClients(clients []domain.MatchedClient) []ClientContext {
	out := make([]ClientContext, len(clients))
	for i, mc := range clients {
		out[i] = FromMatchedClient(mc)
	}
	return out
}

// combineName builds "<inbound name> + <client name>", the display name
// every generator format renders each proxy entry under. Degrades to
// whichever half is non-empty if the other is missing (a client with no
// email, or an inbound with no remark set) rather than emitting a stray
// " + ". This also disambiguates the previously-common case of one client
// assigned to several inbounds, where every entry used to render under the
// exact same name (just the client's email) with no way to tell them apart.
func combineName(inboundName, clientName string) string {
	switch {
	case inboundName == "":
		return clientName
	case clientName == "":
		return inboundName
	default:
		return inboundName + " + " + clientName
	}
}
