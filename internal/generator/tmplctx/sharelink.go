package tmplctx

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ParseShareLink parses a single canonical share-link string, exactly as
// rendered by 3x-ui itself, into a ClientContext. Supports the four
// protocols this project's generators know how to render: vless, vmess,
// trojan, shadowsocks.
func ParseShareLink(link string) (ClientContext, error) {
	scheme, _, ok := strings.Cut(link, "://")
	if !ok {
		return ClientContext{}, fmt.Errorf("tmplctx: %q has no scheme", link)
	}

	switch strings.ToLower(scheme) {
	case "vless":
		return parseURIScheme(link, "vless")
	case "trojan":
		return parseURIScheme(link, "trojan")
	case "vmess":
		return parseVMess(link)
	case "ss":
		return parseShadowsocks(link)
	default:
		return ClientContext{}, fmt.Errorf("tmplctx: unsupported share-link scheme %q", scheme)
	}
}

// parseURIScheme handles vless and trojan, which are both standard URIs
// net/url already knows how to parse: userinfo carries the uuid (vless) or
// password (trojan), and every connection parameter lives in the query
// string exactly as 3x-ui encoded it.
func parseURIScheme(link, protocol string) (ClientContext, error) {
	u, err := url.Parse(link)
	if err != nil {
		return ClientContext{}, fmt.Errorf("tmplctx: parse %s link: %w", protocol, err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return ClientContext{}, fmt.Errorf("tmplctx: parse %s port: %w", protocol, err)
	}

	q := u.Query()
	rawParams := make(map[string]string, len(q))
	for k := range q {
		rawParams[k] = q.Get(k)
	}

	cc := ClientContext{
		Protocol:    protocol,
		Server:      u.Hostname(),
		Port:        port,
		Remark:      u.Fragment,
		Network:     firstNonEmpty(q.Get("type"), "tcp"),
		Security:    firstNonEmpty(q.Get("security"), "none"),
		Flow:        q.Get("flow"),
		SNI:         q.Get("sni"),
		ALPN:        q.Get("alpn"),
		Fingerprint: q.Get("fp"),
		Insecure:    isTruthy(q.Get("allowInsecure")),
		PublicKey:   q.Get("pbk"),
		ShortID:     q.Get("sid"),
		SpiderX:     q.Get("spx"),
		Path:        q.Get("path"),
		Host:        q.Get("host"),
		ServiceName: q.Get("serviceName"),
		RawParams:   rawParams,
	}

	switch protocol {
	case "vless":
		cc.UUID = u.User.Username()
	case "trojan":
		cc.Password = u.User.Username()
	}
	return cc, nil
}

// parseVMess handles the vmess:// scheme, whose body is base64-encoded
// JSON (the standard v2rayN-shaped object: v,ps,add,port,id,aid,scy,net,
// type,host,path,tls,sni,alpn,fp), not a query-string URI.
func parseVMess(link string) (ClientContext, error) {
	body := strings.TrimPrefix(link, "vmess://")
	body, _, _ = strings.Cut(body, "#") // vmess links don't standardly carry a fragment, but strip defensively

	decoded, err := decodeBase64Any(body)
	if err != nil {
		return ClientContext{}, fmt.Errorf("tmplctx: base64-decode vmess link: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(decoded, &raw); err != nil {
		return ClientContext{}, fmt.Errorf("tmplctx: parse vmess JSON: %w", err)
	}

	rawParams := make(map[string]string, len(raw))
	for k, v := range raw {
		rawParams[k] = fmt.Sprint(v)
	}

	port, _ := strconv.Atoi(stringField(raw, "port"))

	return ClientContext{
		Protocol:    "vmess",
		UUID:        stringField(raw, "id"),
		Remark:      stringField(raw, "ps"),
		Server:      stringField(raw, "add"),
		Port:        port,
		Network:     firstNonEmpty(stringField(raw, "net"), "tcp"),
		Security:    firstNonEmpty(stringField(raw, "tls"), "none"),
		SNI:         stringField(raw, "sni"),
		ALPN:        stringField(raw, "alpn"),
		Fingerprint: stringField(raw, "fp"),
		Path:        stringField(raw, "path"),
		Host:        stringField(raw, "host"),
		HeaderType:  stringField(raw, "type"),
		RawParams:   rawParams,
	}, nil
}

// parseShadowsocks handles both shapes ss:// links take: SIP002
// (base64(method:password)@host:port#remark, optionally with a plugin
// query string) and the legacy fully-base64'd form
// (base64(method:password@host:port)#remark).
func parseShadowsocks(link string) (ClientContext, error) {
	rest := strings.TrimPrefix(link, "ss://")

	var remark string
	if body, frag, ok := strings.Cut(rest, "#"); ok {
		rest = body
		if unescaped, err := url.QueryUnescape(frag); err == nil {
			remark = unescaped
		} else {
			remark = frag
		}
	}

	var methodPass, hostPort string
	if userinfo, hp, ok := strings.Cut(rest, "@"); ok {
		hostPort = hp
		if decoded, err := decodeBase64Any(userinfo); err == nil {
			methodPass = string(decoded)
		} else if unescaped, err := url.QueryUnescape(userinfo); err == nil {
			methodPass = unescaped
		} else {
			methodPass = userinfo
		}
	} else {
		decoded, err := decodeBase64Any(rest)
		if err != nil {
			return ClientContext{}, fmt.Errorf("tmplctx: decode legacy shadowsocks link: %w", err)
		}
		full := string(decoded)
		at := strings.LastIndexByte(full, '@')
		if at < 0 {
			return ClientContext{}, fmt.Errorf("tmplctx: legacy shadowsocks link missing '@'")
		}
		methodPass, hostPort = full[:at], full[at+1:]
	}

	rawParams := map[string]string{}
	if hp, query, ok := strings.Cut(hostPort, "?"); ok {
		hostPort = hp
		if q, err := url.ParseQuery(query); err == nil {
			for k := range q {
				rawParams[k] = q.Get(k)
			}
		}
	}

	method, password, ok := strings.Cut(methodPass, ":")
	if !ok {
		return ClientContext{}, fmt.Errorf("tmplctx: shadowsocks link method:password malformed")
	}

	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return ClientContext{}, fmt.Errorf("tmplctx: shadowsocks link host:port malformed: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return ClientContext{}, fmt.Errorf("tmplctx: shadowsocks link port malformed: %w", err)
	}

	return ClientContext{
		Protocol:  "shadowsocks",
		Method:    method,
		Password:  password,
		Server:    host,
		Port:      port,
		Remark:    remark,
		Network:   "tcp",
		Security:  "none",
		RawParams: rawParams,
	}, nil
}

// decodeBase64Any tries every base64 variant xray-ecosystem tools commonly
// emit (standard/URL-safe, padded/unpadded), since producers disagree.
func decodeBase64Any(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("tmplctx: %q is not valid base64 in any known variant", s)
}

func firstNonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func isTruthy(s string) bool {
	return s == "1" || strings.EqualFold(s, "true")
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
