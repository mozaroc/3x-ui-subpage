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
	remark := mc.Client.Email
	if remark == "" {
		remark = mc.Remark
	}
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
