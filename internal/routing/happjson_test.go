package routing

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestEncode_Shape(t *testing.T) {
	p := Profile{
		GlobalProxy:    true,
		RouteOrder:     "Proxy>Direct>Block",
		DomainStrategy: "IPIfNonMatch",
		DirectSites:    []string{"example.com"},
		BlockIP:        []string{"1.2.3.0/24"},
		FakeDNS:        false,
		UseChunkFiles:  true,
	}

	generated, err := p.Encode("my-user")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(generated.Base64)
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

func TestEncode_PreviewJSONMatchesBase64Payload(t *testing.T) {
	generated, err := Profile{RouteOrder: "Block>Proxy>Direct"}.Encode("user")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(generated.PreviewJSON, "://") {
		t.Errorf("expected plain JSON with no deep-link scheme, got: %s", generated.PreviewJSON)
	}

	var preview map[string]any
	if err := json.Unmarshal([]byte(generated.PreviewJSON), &preview); err != nil {
		t.Fatalf("expected preview to be valid JSON: %v\n%s", err, generated.PreviewJSON)
	}
	if preview["RouteOrder"] != "Block>Proxy>Direct" {
		t.Errorf("unexpected RouteOrder in preview: %v", preview["RouteOrder"])
	}

	decoded, err := base64.StdEncoding.DecodeString(generated.Base64)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	var fromB64 map[string]any
	if err := json.Unmarshal(decoded, &fromB64); err != nil {
		t.Fatalf("unmarshal decoded base64: %v", err)
	}
	// Same wire snapshot -- preview and the pasteable base64 must agree,
	// including LastUpdated (they come from one toWire() call, not two).
	if preview["LastUpdated"] != fromB64["LastUpdated"] {
		t.Errorf("expected preview and base64 to share LastUpdated, got %v vs %v", preview["LastUpdated"], fromB64["LastUpdated"])
	}
}

func TestEncode_OmitsEmptyOptionalFields(t *testing.T) {
	generated, err := Profile{}.Encode("user")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(generated.Base64)
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
