package routing

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// wireProfile is the exact Happ/Incy Routing Profile wire schema — field
// names and casing confirmed against routing.happ.su / docs.incy.cc and
// upstream 3x-ui's own implementation. Two real quirks worth flagging:
// GlobalProxy and FakeDNS are encoded as the literal strings "true"/
// "false" (not JSON booleans), while UseChunkFiles is a genuine JSON bool.
type wireProfile struct {
	Name              string            `json:"Name"`
	GlobalProxy       string            `json:"GlobalProxy"`
	RouteOrder        string            `json:"RouteOrder"`
	DomainStrategy    string            `json:"DomainStrategy"`
	RemoteDNSType     string            `json:"RemoteDNSType,omitempty"`
	RemoteDNSDomain   string            `json:"RemoteDNSDomain,omitempty"`
	RemoteDNSIP       string            `json:"RemoteDNSIP,omitempty"`
	DomesticDNSType   string            `json:"DomesticDNSType,omitempty"`
	DomesticDNSDomain string            `json:"DomesticDNSDomain,omitempty"`
	DomesticDNSIP     string            `json:"DomesticDNSIP,omitempty"`
	DnsHosts          map[string]string `json:"DnsHosts,omitempty"`
	Geoipurl          string            `json:"Geoipurl,omitempty"`
	Geositeurl        string            `json:"Geositeurl,omitempty"`
	DirectSites       []string          `json:"DirectSites,omitempty"`
	DirectIp          []string          `json:"DirectIp,omitempty"`
	ProxySites        []string          `json:"ProxySites,omitempty"`
	ProxyIp           []string          `json:"ProxyIp,omitempty"`
	BlockSites        []string          `json:"BlockSites,omitempty"`
	BlockIp           []string          `json:"BlockIp,omitempty"`
	FakeDNS           string            `json:"FakeDNS"`
	UseChunkFiles     bool              `json:"UseChunkFiles"`
	LastUpdated       string            `json:"LastUpdated"`
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (p Profile) toWire(name string) wireProfile {
	return wireProfile{
		Name:              name,
		GlobalProxy:       boolString(p.GlobalProxy),
		RouteOrder:        p.RouteOrder,
		DomainStrategy:    p.DomainStrategy,
		RemoteDNSType:     p.RemoteDNSType,
		RemoteDNSDomain:   p.RemoteDNSDomain,
		RemoteDNSIP:       p.RemoteDNSIP,
		DomesticDNSType:   p.DomesticDNSType,
		DomesticDNSDomain: p.DomesticDNSDomain,
		DomesticDNSIP:     p.DomesticDNSIP,
		DnsHosts:          p.DNSHosts,
		Geoipurl:          p.GeoIPURL,
		Geositeurl:        p.GeoSiteURL,
		DirectSites:       p.DirectSites,
		DirectIp:          p.DirectIP,
		ProxySites:        p.ProxySites,
		ProxyIp:           p.ProxyIP,
		BlockSites:        p.BlockSites,
		BlockIp:           p.BlockIP,
		FakeDNS:           boolString(p.FakeDNS),
		UseChunkFiles:     p.UseChunkFiles,
		LastUpdated:       strconv.FormatInt(time.Now().Unix(), 10),
	}
}

// GeneratedRouting is one snapshot of a Profile encoded for both display
// (PreviewJSON) and deep-link embedding (Base64) -- generated from a single
// wire object so the two never disagree on LastUpdated.
type GeneratedRouting struct {
	PreviewJSON string // pretty JSON, for the admin UI
	Base64      string // base64(compact JSON) -- the literal string a Happ/Incy
	// deep link embeds as "{scheme}://routing/{action}/{Base64}"
}

// Encode renders p as the exact Happ/Incy wire JSON (named per the admin's
// chosen label), returning both a pretty-printed preview and the compact
// Base64 form that gets pasted into a user's routing_b64 field.
func (p Profile) Encode(name string) (GeneratedRouting, error) {
	wire := p.toWire(name)

	pretty, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return GeneratedRouting{}, fmt.Errorf("routing: encode preview: %w", err)
	}
	compact, err := json.Marshal(wire)
	if err != nil {
		return GeneratedRouting{}, fmt.Errorf("routing: encode base64: %w", err)
	}
	return GeneratedRouting{
		PreviewJSON: string(pretty),
		Base64:      base64.StdEncoding.EncodeToString(compact),
	}, nil
}
