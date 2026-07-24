package httpserver

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/irazin/3x-ui-subpage/internal/apps"
	"github.com/irazin/3x-ui-subpage/internal/config"
	"github.com/irazin/3x-ui-subpage/internal/domain"
	"github.com/irazin/3x-ui-subpage/internal/resolver"
)

type fakeResolver struct {
	sub domain.Subscription
	err error
}

func (f fakeResolver) Resolve(ctx context.Context, subID string) (domain.Subscription, error) {
	return f.sub, f.err
}

type fakeLinkGen struct{}

func (fakeLinkGen) BuildLink(mc domain.MatchedClient, profile string) (string, error) {
	return "vless://fake", nil
}
func (fakeLinkGen) BuildSubscription(clients []domain.MatchedClient, profile string) (string, error) {
	return base64.StdEncoding.EncodeToString([]byte("vless://fake\n")), nil
}

// fakeConfigGen returns a fixed string for "default" profile and a distinct
// one for any other profile, so tests can assert profile selection reached
// the generator correctly.
type fakeConfigGen struct {
	out        string
	profileOut map[string]string
}

func (f fakeConfigGen) Build(clients []domain.MatchedClient, profile string) (string, error) {
	if v, ok := f.profileOut[profile]; ok {
		return v, nil
	}
	return f.out, nil
}

type fakeTheme struct {
	static map[string]string
}

func (fakeTheme) Render(w io.Writer, data any) error {
	view := data.(SubscriptionView)
	_, err := w.Write([]byte("<html>" + view.Username + "</html>"))
	return err
}

func (f fakeTheme) ServeStatic(w http.ResponseWriter, r *http.Request, path string) (bool, error) {
	content, ok := f.static[path]
	if !ok {
		return false, nil
	}
	w.Header().Set("Content-Type", "text/css")
	_, err := w.Write([]byte(content))
	return true, err
}

type fakeApps struct{ apps []apps.App }

func (f fakeApps) List() ([]apps.App, error) { return f.apps, nil }

// fakeAssignments returns a fixed profile for a fixed subID, "default" for
// everything else.
type fakeAssignments struct {
	bySubID map[string]string
	err     error
}

func (f fakeAssignments) Resolve(subID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if p, ok := f.bySubID[subID]; ok {
		return p, nil
	}
	return "default", nil
}

func testDeps(t *testing.T, sub domain.Subscription, resolveErr error) Deps {
	t.Helper()
	return Deps{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Resolver:    fakeResolver{sub: sub, err: resolveErr},
		LinkGen:     fakeLinkGen{},
		XrayJSON:    fakeConfigGen{out: `{"ok":true}`},
		Clash:       fakeConfigGen{out: "proxies: []\n"},
		Mihomo:      fakeConfigGen{out: "proxies: []\n"},
		Happ:        fakeConfigGen{out: `{"happ":true}`},
		Incy:        fakeConfigGen{out: `{"incy":true}`},
		Theme:       fakeTheme{static: map[string]string{}},
		ThemeSlug:   "default",
		Apps:        fakeApps{apps: []apps.App{{Name: "Test App", Deeplink: "test://{subscription}"}}},
		Assignments: fakeAssignments{},
		QRDefaults:  config.QRConfig{Size: 64, Margin: 2, Foreground: "#000000", Background: "#FFFFFF"},
		PublicURL:   "https://sub.example.com",
		Support:     config.SupportConfig{Telegram: "https://t.me/example"},
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{RequestsPerMinute: 6000, Burst: 100},
			CSP:       "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'",
		},
	}
}

func sampleSubscription() domain.Subscription {
	return domain.Subscription{
		SubID:    "tok-abc",
		Username: "alice",
		Status:   domain.StatusActive,
		Clients: []domain.MatchedClient{
			{Protocol: domain.ProtocolVLESS, Server: "vpn.example.com", Port: 443, Client: domain.ClientAccount{ID: "uuid-1", Email: "alice"}},
		},
	}
}

func TestHandleSubscription_HTMLForBrowser(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120")
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "alice") {
		t.Errorf("expected HTML body to contain username, got: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html content-type, got %q", ct)
	}
}

func TestHandleSubscription_ClashForClashUA(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc", nil)
	req.Header.Set("User-Agent", "ClashX/1.90.0")
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "proxies") {
		t.Errorf("expected clash yaml body, got: %s", rec.Body.String())
	}
}

func TestHandleSubscription_MihomoForMihomoUA(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc", nil)
	req.Header.Set("User-Agent", "mihomo/1.18.0")
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "mihomo.yaml") {
		t.Errorf("expected mihomo generator to be used, got Content-Disposition: %q", cd)
	}
}

// Mihomo was renamed from "Clash.Meta" -- plenty of real mihomo-core
// clients still send that name verbatim in their User-Agent for backward
// compat (confirmed: Prizrak-Box, a mihomo-only GUI client, sends
// "Clash-Meta/Prizrak-Box"). These must get the mihomo template, not the
// original-Clash-dialect one, or they're missing mihomo-only fields
// (tun, geodata-mode, ...) they may depend on.
func TestHandleSubscription_MihomoForClashMetaUA(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc", nil)
	req.Header.Set("User-Agent", "Clash-Meta/Prizrak-Box")
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "mihomo.yaml") {
		t.Errorf("expected mihomo generator for a Clash-Meta UA, got Content-Disposition: %q", cd)
	}
}

// The real Happ app requests and imports the same base64 share-link
// subscription as every other unrecognized client (V2RayN/V2RayA/Clash/
// SingBox all consume it too) — confirmed against Happ's own subscription
// tooling. It must NOT get the admin-editable /happ JSON format by
// default, or the app treats the response as an opaque file download
// instead of an importable subscription. See detectFormat's doc comment.
func TestHandleSubscription_HappUAGetsStandardXrayLinks(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc", nil)
	req.Header.Set("User-Agent", "Happ/1.0")
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	decoded, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil {
		t.Fatalf("expected base64 xray-link body for a Happ UA, got: %s", rec.Body.String())
	}
	if !strings.Contains(string(decoded), "vless://fake") {
		t.Errorf("expected decoded body to contain the share link, got: %s", decoded)
	}
}

func TestHandleSubscription_IncyForIncyUA(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc", nil)
	req.Header.Set("User-Agent", "Incy/2.0")
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"incy":true`) {
		t.Errorf("expected incy body, got: %s", rec.Body.String())
	}
}

func TestHandleHapp_ExplicitRoute(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc/happ", nil)
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"happ":true`) {
		t.Errorf("expected happ body, got: %s", rec.Body.String())
	}
}

func TestHandleIncy_ExplicitRoute(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc/incy", nil)
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"incy":true`) {
		t.Errorf("expected incy body, got: %s", rec.Body.String())
	}
}

func TestHandleSubscription_DefaultXrayLinksForUnknownUA(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc", nil)
	req.Header.Set("User-Agent", "v2rayNG/1.8.5")
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	decoded, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil {
		t.Fatalf("expected base64 body, decode failed: %v", err)
	}
	if !strings.Contains(string(decoded), "vless://") {
		t.Errorf("expected decoded body to contain vless link, got: %s", decoded)
	}
	if rec.Header().Get("Subscription-Userinfo") == "" {
		t.Error("expected Subscription-Userinfo header to be set")
	}
}

func TestHandleSubscription_NotFound(t *testing.T) {
	srv := New(testDeps(t, domain.Subscription{}, resolver.ErrNotFound))
	req := httptest.NewRequest(http.MethodGet, "/sub/does-not-exist", nil)
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleSubscription_InvalidSubID(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/bad%20id%20with%20spaces", nil)
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid subID, got %d", rec.Code)
	}
}

func TestHandleSubscription_UpstreamError(t *testing.T) {
	srv := New(testDeps(t, domain.Subscription{}, errors.New("panel unreachable")))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc", nil)
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

func TestHandleXrayJSON(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc/xray.json", nil)
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestHandleQRPNG(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc/qr.png", nil)
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("expected image/png, got %q", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("expected non-empty PNG body")
	}
}

func TestHandleAPIApplications(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/applications", nil)
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Test App") {
		t.Errorf("expected app catalog JSON, got: %s", rec.Body.String())
	}
}

func TestHandleHealth(t *testing.T) {
	srv := New(testDeps(t, sampleSubscription(), nil))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleStaticAsset_ServesAndNotFound(t *testing.T) {
	deps := testDeps(t, sampleSubscription(), nil)
	deps.Theme = fakeTheme{static: map[string]string{"css/style.css": "body{}"}}
	srv := New(deps)

	req := httptest.NewRequest(http.MethodGet, "/assets/default/css/style.css", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "body{}" {
		t.Fatalf("expected 200 with asset body, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/assets/default/css/missing.css", nil)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing asset, got %d", rec.Code)
	}
}

func TestHandleSubscription_AssignedProfileSelectsItsTemplate(t *testing.T) {
	deps := testDeps(t, sampleSubscription(), nil)
	deps.Mihomo = fakeConfigGen{
		out:        "proxies: []\n", // default
		profileOut: map[string]string{"gaming": "proxies: []\ngaming: true\n"},
	}
	deps.Assignments = fakeAssignments{bySubID: map[string]string{"tok-abc": "gaming"}}
	srv := New(deps)

	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc/mihomo", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "gaming: true") {
		t.Errorf("expected gaming profile's template to be used, got: %s", rec.Body.String())
	}
}

func TestHandleSubscription_UnassignedSubscriberGetsDefaultProfile(t *testing.T) {
	deps := testDeps(t, sampleSubscription(), nil)
	deps.Mihomo = fakeConfigGen{
		out:        "proxies: []\ndefault: true\n",
		profileOut: map[string]string{"gaming": "proxies: []\ngaming: true\n"},
	}
	// no assignment for tok-abc -> fakeAssignments defaults to "default"
	deps.Assignments = fakeAssignments{}
	srv := New(deps)

	req := httptest.NewRequest(http.MethodGet, "/sub/tok-abc/mihomo", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "default: true") {
		t.Errorf("expected default profile's template to be used, got: %s", rec.Body.String())
	}
}

func TestRateLimit_Returns429WhenExceeded(t *testing.T) {
	deps := testDeps(t, sampleSubscription(), nil)
	deps.Security.RateLimit = config.RateLimitConfig{RequestsPerMinute: 60, Burst: 1}
	srv := New(deps)

	var lastCode int
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("expected eventual 429 under burst=1, got %d", lastCode)
	}
}
