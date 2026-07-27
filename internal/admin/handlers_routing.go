package admin

import (
	"net/http"
	"sort"
	"strings"

	"github.com/irazin/3x-ui-subpage/internal/routing"
)

// routeOrderOptions/domainStrategyOptions/dnsTypeOptions are the Happ
// Routing Generator's own enum choices (routing.happ.su / docs.incy.cc).
var (
	routeOrderOptions = []string{
		"Block>Proxy>Direct", "Block>Direct>Proxy",
		"Proxy>Direct>Block", "Proxy>Block>Direct",
		"Direct>Proxy>Block", "Direct>Block>Proxy",
	}
	domainStrategyOptions = []string{"AsIs", "IPIfNonMatch", "IPOnDemand"}
	dnsTypeOptions        = []string{"", "DoH", "DoU"}
)

func linesToSlice(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func sliceToLines(s []string) string {
	return strings.Join(s, "\n")
}

func hostsToText(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+" "+m[k])
	}
	return strings.Join(lines, "\n")
}

func textToHosts(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		out[fields[0]] = fields[1]
	}
	return out
}

// routingGeneratorView is the Routing Generator page's full field set,
// ready to render.
type routingGeneratorView struct {
	Name string

	GlobalProxy    bool
	RouteOrder     string
	DomainStrategy string

	RemoteDNSType     string
	RemoteDNSDomain   string
	RemoteDNSIP       string
	DomesticDNSType   string
	DomesticDNSDomain string
	DomesticDNSIP     string
	DNSHosts          string // one "domain ip" pair per line

	GeoIPURL   string
	GeoSiteURL string

	DirectSites string // one entry per line
	DirectIP    string
	ProxySites  string
	ProxyIP     string
	BlockSites  string
	BlockIP     string

	FakeDNS       bool
	UseChunkFiles bool

	RouteOrderOptions     []string
	DomainStrategyOptions []string
	DNSTypeOptions        []string

	GeneratedB64 string
	PreviewJSON  string
}

// routingGeneratorViewFor builds the render-ready view for name/p, with
// generated (the last Encode result, zero value if nothing generated yet)
// filling in the output section.
func routingGeneratorViewFor(name string, p routing.Profile, generated routing.GeneratedRouting) routingGeneratorView {
	return routingGeneratorView{
		Name:                  name,
		GlobalProxy:           p.GlobalProxy,
		RouteOrder:            p.RouteOrder,
		DomainStrategy:        p.DomainStrategy,
		RemoteDNSType:         p.RemoteDNSType,
		RemoteDNSDomain:       p.RemoteDNSDomain,
		RemoteDNSIP:           p.RemoteDNSIP,
		DomesticDNSType:       p.DomesticDNSType,
		DomesticDNSDomain:     p.DomesticDNSDomain,
		DomesticDNSIP:         p.DomesticDNSIP,
		DNSHosts:              hostsToText(p.DNSHosts),
		GeoIPURL:              p.GeoIPURL,
		GeoSiteURL:            p.GeoSiteURL,
		DirectSites:           sliceToLines(p.DirectSites),
		DirectIP:              sliceToLines(p.DirectIP),
		ProxySites:            sliceToLines(p.ProxySites),
		ProxyIP:               sliceToLines(p.ProxyIP),
		BlockSites:            sliceToLines(p.BlockSites),
		BlockIP:               sliceToLines(p.BlockIP),
		FakeDNS:               p.FakeDNS,
		UseChunkFiles:         p.UseChunkFiles,
		RouteOrderOptions:     routeOrderOptions,
		DomainStrategyOptions: domainStrategyOptions,
		DNSTypeOptions:        dnsTypeOptions,
		GeneratedB64:          generated.Base64,
		PreviewJSON:           generated.PreviewJSON,
	}
}

// generatorProfileFromForm parses the Routing Generator page's fields.
func generatorProfileFromForm(r *http.Request) routing.Profile {
	return routing.Profile{
		GlobalProxy:       r.FormValue("routing_global_proxy") != "",
		RouteOrder:        r.FormValue("routing_route_order"),
		DomainStrategy:    r.FormValue("routing_domain_strategy"),
		RemoteDNSType:     r.FormValue("routing_remote_dns_type"),
		RemoteDNSDomain:   strings.TrimSpace(r.FormValue("routing_remote_dns_domain")),
		RemoteDNSIP:       strings.TrimSpace(r.FormValue("routing_remote_dns_ip")),
		DomesticDNSType:   r.FormValue("routing_domestic_dns_type"),
		DomesticDNSDomain: strings.TrimSpace(r.FormValue("routing_domestic_dns_domain")),
		DomesticDNSIP:     strings.TrimSpace(r.FormValue("routing_domestic_dns_ip")),
		DNSHosts:          textToHosts(r.FormValue("routing_dns_hosts")),
		GeoIPURL:          strings.TrimSpace(r.FormValue("routing_geoip_url")),
		GeoSiteURL:        strings.TrimSpace(r.FormValue("routing_geosite_url")),
		DirectSites:       linesToSlice(r.FormValue("routing_direct_sites")),
		DirectIP:          linesToSlice(r.FormValue("routing_direct_ip")),
		ProxySites:        linesToSlice(r.FormValue("routing_proxy_sites")),
		ProxyIP:           linesToSlice(r.FormValue("routing_proxy_ip")),
		BlockSites:        linesToSlice(r.FormValue("routing_block_sites")),
		BlockIP:           linesToSlice(r.FormValue("routing_block_ip")),
		FakeDNS:           r.FormValue("routing_fake_dns") != "",
		UseChunkFiles:     r.FormValue("routing_use_chunk_files") != "",
	}
}

type routingGeneratorPageData struct {
	Routing routingGeneratorView
	Error   string
}

func (s *Server) handleRoutingGeneratorForm(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	name, profile, generatedB64, err := s.routing.GetGenerator()
	if err != nil {
		s.logger.Error("admin: load routing generator failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	generated := routing.GeneratedRouting{Base64: generatedB64}
	if generatedB64 != "" {
		if enc, err := profile.Encode(name); err == nil {
			generated.PreviewJSON = enc.PreviewJSON
		}
	}

	_ = render(w, "page-routing-generator", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: routingGeneratorPageData{Routing: routingGeneratorViewFor(name, profile, generated)},
	})
}

func (s *Server) handleRoutingGeneratorGenerate(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	name := strings.TrimSpace(r.FormValue("routing_name"))
	profile := generatorProfileFromForm(r)

	generated, err := s.routing.SaveGenerator(name, profile)
	if err != nil {
		s.logger.Error("admin: save routing generator failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-routing-generator", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: routingGeneratorPageData{Routing: routingGeneratorViewFor(name, profile, generated)},
	})
}
