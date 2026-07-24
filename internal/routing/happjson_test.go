package routing

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestEncodeDeepLink_Shape(t *testing.T) {
	p := Profile{
		GlobalProxy:    true,
		RouteOrder:     "Proxy>Direct>Block",
		DomainStrategy: "IPIfNonMatch",
		DirectSites:    []string{"example.com"},
		BlockIP:        []string{"1.2.3.0/24"},
		FakeDNS:        false,
		UseChunkFiles:  true,
	}

	link, err := p.EncodeDeepLink("happ", "onadd", "my-user")
	if err != nil {
		t.Fatalf("EncodeDeepLink: %v", err)
	}

	const prefix = "happ://routing/onadd/"
	if !strings.HasPrefix(link, prefix) {
		t.Fatalf("expected link to start with %q, got %q", prefix, link)
	}

	encoded := strings.TrimPrefix(link, prefix)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("expected valid base64 payload: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(decoded, &raw); err != nil {
		t.Fatalf("expected valid JSON payload: %v\n%s", err, decoded)
	}

	if raw["Name"] != "my-user" {
		t.Errorf("expected Name=my-user, got %v", raw["Name"])
	}
	// The two documented quirks: GlobalProxy/FakeDNS are string "true"/
	// "false", not JSON booleans -- a client parsing this expecting a
	// literal string would break if we ever emitted a JSON bool instead.
	if raw["GlobalProxy"] != "true" {
		t.Errorf(`expected GlobalProxy="true" (string), got %#v`, raw["GlobalProxy"])
	}
	if raw["FakeDNS"] != "false" {
		t.Errorf(`expected FakeDNS="false" (string), got %#v`, raw["FakeDNS"])
	}
	// UseChunkFiles is a genuine JSON bool.
	if v, ok := raw["UseChunkFiles"].(bool); !ok || !v {
		t.Errorf("expected UseChunkFiles=true (bool), got %#v", raw["UseChunkFiles"])
	}
	if raw["RouteOrder"] != "Proxy>Direct>Block" {
		t.Errorf("unexpected RouteOrder: %v", raw["RouteOrder"])
	}
	sites, ok := raw["DirectSites"].([]any)
	if !ok || len(sites) != 1 || sites[0] != "example.com" {
		t.Errorf("unexpected DirectSites: %v", raw["DirectSites"])
	}
	if _, err := strconv.ParseInt(raw["LastUpdated"].(string), 10, 64); err != nil {
		t.Errorf("expected LastUpdated to be a numeric string timestamp, got %v", raw["LastUpdated"])
	}
}

func TestEncodeDeepLink_IncyScheme(t *testing.T) {
	link, err := Profile{}.EncodeDeepLink("incy", "onadd", "user")
	if err != nil {
		t.Fatalf("EncodeDeepLink: %v", err)
	}
	if !strings.HasPrefix(link, "incy://routing/onadd/") {
		t.Errorf("expected incy scheme, got %q", link)
	}
}

func TestPreviewJSON_NoBase64Wrapping(t *testing.T) {
	preview, err := Profile{RouteOrder: "Block>Proxy>Direct"}.PreviewJSON("user")
	if err != nil {
		t.Fatalf("PreviewJSON: %v", err)
	}
	if strings.Contains(preview, "://") {
		t.Errorf("expected plain JSON with no deep-link scheme, got: %s", preview)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(preview), &raw); err != nil {
		t.Fatalf("expected preview to be valid JSON: %v\n%s", err, preview)
	}
	if raw["RouteOrder"] != "Block>Proxy>Direct" {
		t.Errorf("unexpected RouteOrder in preview: %v", raw["RouteOrder"])
	}
}

func TestEncodeDeepLink_OmitsEmptyOptionalFields(t *testing.T) {
	link, err := Profile{}.EncodeDeepLink("happ", "onadd", "user")
	if err != nil {
		t.Fatalf("EncodeDeepLink: %v", err)
	}
	encoded := strings.TrimPrefix(link, "happ://routing/onadd/")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(decoded, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"DirectSites", "DirectIp", "ProxySites", "ProxyIp", "BlockSites", "BlockIp", "DnsHosts", "RemoteDNSType", "Geoipurl"} {
		if _, present := raw[key]; present {
			t.Errorf("expected empty optional field %q to be omitted, got %v", key, raw[key])
		}
	}
	// Non-optional fields must still always be present.
	for _, key := range []string{"Name", "GlobalProxy", "RouteOrder", "DomainStrategy", "FakeDNS", "UseChunkFiles", "LastUpdated"} {
		if _, present := raw[key]; !present {
			t.Errorf("expected required field %q to always be present", key)
		}
	}
}
