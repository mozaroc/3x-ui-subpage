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

// PreviewJSON renders the exact wire JSON, pretty-printed, for display in
// the admin UI -- no base64/deep-link wrapping.
func (p Profile) PreviewJSON(name string) (string, error) {
	b, err := json.MarshalIndent(p.toWire(name), "", "  ")
	if err != nil {
		return "", fmt.Errorf("routing: encode preview: %w", err)
	}
	return string(b), nil
}

// EncodeDeepLink builds the deep link the Happ/Incy app reads to install
// this routing profile: "{scheme}://routing/{action}/{base64(json)}".
// scheme is "happ" or "incy"; action is typically "onadd" (add and
// activate immediately).
func (p Profile) EncodeDeepLink(scheme, action, name string) (string, error) {
	b, err := json.Marshal(p.toWire(name))
	if err != nil {
		return "", fmt.Errorf("routing: encode deep link: %w", err)
	}
	return fmt.Sprintf("%s://routing/%s/%s", scheme, action, base64.StdEncoding.EncodeToString(b)), nil
}
