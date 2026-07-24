package yamlgen

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
)

// injectClash builds every client's proxy definition, inserts them under
// the template's "proxies" key, and auto-populates every "proxy-groups"
// entry (see injectGroup). A `remnawave:` block found under any
// "proxy-providers" entry is rejected outright — dialer-chaining is not
// implemented, so silently ignoring it would produce a config that looks
// right but doesn't chain as the admin intended.
func injectClash(doc *yaml.Node, ccs []tmplctx.ClientContext) error {
	root := rootMapping(doc)

	proxies := &yaml.Node{Kind: yaml.SequenceNode}
	names := make([]string, len(ccs))
	for i, cc := range ccs {
		node, err := buildProxyNode(cc)
		if err != nil {
			return err
		}
		proxies.Content = append(proxies.Content, node)
		names[i] = cc.Remark
	}
	mapSet(root, "proxies", proxies)

	if groups := mapGet(root, "proxy-groups"); groups != nil && groups.Kind == yaml.SequenceNode {
		for _, group := range groups.Content {
			if group.Kind != yaml.MappingNode {
				continue
			}
			if err := injectGroup(group, names); err != nil {
				return err
			}
		}
	}

	if providers := mapGet(root, "proxy-providers"); providers != nil && providers.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(providers.Content); i += 2 {
			name := providers.Content[i].Value
			entry := providers.Content[i+1]
			if entry.Kind == yaml.MappingNode && mapGet(entry, "remnawave") != nil {
				return fmt.Errorf("proxy-providers %q: dialer-chaining (remnawave: block) is not supported", name)
			}
		}
	}

	return nil
}

// remnawaveGroupOpts mirrors the `remnawave:` block Remnawave documents for
// proxy-groups entries (https://docs.rw/guides/templates/mihomo).
// IncludeProxies is a pointer so "absent" (default: populate) is
// distinguishable from an explicit `false` (opt out).
type remnawaveGroupOpts struct {
	IncludeProxies      *bool
	SelectRandomProxy   bool
	ShuffleProxiesOrder bool
}

// injectGroup auto-populates one proxy-groups entry's "proxies" list with
// the generated client names, honoring the group's `remnawave:` block and
// native filter/exclude-filter regexes. Existing manually-listed entries
// (e.g. "DIRECT", another group's name) are preserved — injected names are
// appended, never replacing what the admin already wrote.
func injectGroup(group *yaml.Node, names []string) error {
	opts, err := parseRemnawaveGroupOpts(mapDelete(group, "remnawave"))
	if err != nil {
		return err
	}
	if opts.IncludeProxies != nil && !*opts.IncludeProxies {
		return nil
	}

	candidates := names
	if node := mapGet(group, "filter"); node != nil {
		re, err := regexp.Compile(node.Value)
		if err != nil {
			return fmt.Errorf("proxy-groups: invalid filter regexp %q: %w", node.Value, err)
		}
		candidates = filterNames(candidates, re, true)
	}
	if node := mapGet(group, "exclude-filter"); node != nil {
		re, err := regexp.Compile(node.Value)
		if err != nil {
			return fmt.Errorf("proxy-groups: invalid exclude-filter regexp %q: %w", node.Value, err)
		}
		candidates = filterNames(candidates, re, false)
	}

	var injected []string
	switch {
	case opts.SelectRandomProxy:
		if len(candidates) > 0 {
			injected = []string{candidates[rand.Intn(len(candidates))]}
		}
	case opts.ShuffleProxiesOrder:
		injected = append([]string(nil), candidates...)
		rand.Shuffle(len(injected), func(i, j int) { injected[i], injected[j] = injected[j], injected[i] })
	default:
		injected = candidates
	}

	proxiesNode := mapGet(group, "proxies")
	if proxiesNode == nil || proxiesNode.Kind != yaml.SequenceNode {
		proxiesNode = &yaml.Node{Kind: yaml.SequenceNode}
		mapSet(group, "proxies", proxiesNode)
	}
	for _, name := range injected {
		proxiesNode.Content = append(proxiesNode.Content, str(name))
	}
	return nil
}

func filterNames(names []string, re *regexp.Regexp, keepMatching bool) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if re.MatchString(n) == keepMatching {
			out = append(out, n)
		}
	}
	return out
}

func parseRemnawaveGroupOpts(node *yaml.Node) (remnawaveGroupOpts, error) {
	var opts remnawaveGroupOpts
	if node == nil {
		return opts, nil
	}
	if node.Kind != yaml.MappingNode {
		return opts, fmt.Errorf("proxy-groups: remnawave key must be a mapping")
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i].Value
		var b bool
		if err := node.Content[i+1].Decode(&b); err != nil {
			return opts, fmt.Errorf("proxy-groups: remnawave.%s: %w", k, err)
		}
		switch k {
		case "include-proxies":
			opts.IncludeProxies = &b
		case "select-random-proxy":
			opts.SelectRandomProxy = b
		case "shuffle-proxies-order":
			opts.ShuffleProxiesOrder = b
		}
	}
	return opts, nil
}

// buildProxyNode renders one client's proxy definition, matching the field
// set the admin-editable .tmpl files used to hand-write per protocol.
func buildProxyNode(cc tmplctx.ClientContext) (*yaml.Node, error) {
	m := mapping(
		key("name"), str(cc.Remark),
		key("type"), key(proxyType(cc.Protocol)),
		key("server"), str(cc.Server),
		key("port"), num(cc.Port),
		key("udp"), boolean(true),
	)

	switch domain.Protocol(cc.Protocol) {
	case domain.ProtocolVLESS:
		appendVLESS(m, cc)
	case domain.ProtocolVMess:
		appendVMess(m, cc)
	case domain.ProtocolTrojan:
		appendTrojan(m, cc)
	case domain.ProtocolShadowsocks:
		appendShadowsocks(m, cc)
	default:
		return nil, fmt.Errorf("client %q: unknown protocol %q", cc.Remark, cc.Protocol)
	}
	return m, nil
}

func proxyType(protocol string) string {
	if protocol == string(domain.ProtocolShadowsocks) {
		return "ss"
	}
	return protocol
}

func appendVLESS(m *yaml.Node, cc tmplctx.ClientContext) {
	mapAppend(m,
		key("uuid"), str(cc.UUID),
		key("network"), key(cc.Network),
		key("tls"), boolean(cc.Security != string(domain.SecurityNone)),
	)
	if cc.Insecure {
		mapAppend(m, key("skip-cert-verify"), boolean(true))
	}
	if cc.Flow != "" {
		mapAppend(m, key("flow"), str(cc.Flow))
	}
	switch domain.Security(cc.Security) {
	case domain.SecurityReality:
		mapAppend(m,
			key("servername"), str(cc.SNI),
			key("client-fingerprint"), str(cc.Fingerprint),
			key("reality-opts"), mapping(
				key("public-key"), str(cc.PublicKey),
				key("short-id"), str(cc.ShortID),
			),
		)
	case domain.SecurityTLS:
		mapAppend(m,
			key("servername"), str(cc.SNI),
			key("client-fingerprint"), str(cc.Fingerprint),
		)
	}
	appendTransportOpts(m, cc)
}

func appendVMess(m *yaml.Node, cc tmplctx.ClientContext) {
	mapAppend(m,
		key("uuid"), str(cc.UUID),
		key("alterId"), num(0),
		key("cipher"), key("auto"),
		key("network"), key(cc.Network),
		key("tls"), boolean(cc.Security != string(domain.SecurityNone)),
	)
	if cc.Insecure {
		mapAppend(m, key("skip-cert-verify"), boolean(true))
	}
	appendTransportOpts(m, cc)
}

func appendTrojan(m *yaml.Node, cc tmplctx.ClientContext) {
	mapAppend(m, key("password"), str(cc.Password))
	if cc.SNI != "" {
		mapAppend(m, key("sni"), str(cc.SNI))
	}
	if cc.Insecure {
		mapAppend(m, key("skip-cert-verify"), boolean(true))
	}
}

func appendShadowsocks(m *yaml.Node, cc tmplctx.ClientContext) {
	mapAppend(m,
		key("cipher"), str(cc.Method),
		key("password"), str(cc.Password),
	)
}

func appendTransportOpts(m *yaml.Node, cc tmplctx.ClientContext) {
	switch domain.Network(cc.Network) {
	case domain.NetworkWS:
		mapAppend(m, key("ws-opts"), mapping(
			key("path"), str(cc.Path),
			key("headers"), mapping(key("Host"), str(cc.Host)),
		))
	case domain.NetworkGRPC:
		mapAppend(m, key("grpc-opts"), mapping(
			key("grpc-service-name"), str(cc.ServiceName),
		))
	case domain.NetworkXHTTP:
		// Deliberately minimal: path+host is enough for basic connectivity.
		// xhttp carries many more sub-fields (mode, padding, session/seq
		// placement, ...) that this project doesn't assert a verified
		// mihomo YAML schema for -- see RawParams/xray_json's raw "extra"
		// splice for the byte-accurate xray-core-side rendering instead.
		mapAppend(m, key("xhttp-opts"), mapping(
			key("path"), str(cc.Path),
			key("host"), str(cc.Host),
		))
	}
}

// --- yaml.Node builders and mapping helpers ---

// key builds a plain (unquoted) string scalar, for fixed YAML key names
// and enum-like literal values (protocol/network names).
func key(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// str builds a double-quoted string scalar, for values sourced from panel
// data (names, passwords, hosts, ...) that may contain YAML-special
// characters and must always round-trip safely.
func str(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.DoubleQuotedStyle}
}

func num(i int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(i)}
}

func boolean(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
}

func mapping(pairs ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Content: pairs}
}

func mapAppend(m *yaml.Node, pairs ...*yaml.Node) {
	m.Content = append(m.Content, pairs...)
}

// rootMapping returns doc's top-level mapping node, creating an empty one
// if the document is empty or its root isn't a mapping (a defensively
// handled edge case — real templates always have top-level settings).
func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	return doc.Content[0]
}

// mapGet returns the value node for keyName in mapping m, or nil if absent.
func mapGet(m *yaml.Node, keyName string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == keyName {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapSet replaces keyName's value in mapping m, or appends a new pair if
// keyName isn't already present.
func mapSet(m *yaml.Node, keyName string, value *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == keyName {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, key(keyName), value)
}

// mapDelete removes keyName's pair from mapping m and returns its value
// node, or nil if keyName wasn't present.
func mapDelete(m *yaml.Node, keyName string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == keyName {
			value := m.Content[i+1]
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return value
		}
	}
	return nil
}
