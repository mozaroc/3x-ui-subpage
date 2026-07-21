package admin

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/irazin/3x-ui-subpage/internal/adminauth"
	"github.com/irazin/3x-ui-subpage/internal/assignment"
	"github.com/irazin/3x-ui-subpage/internal/config"
	"github.com/irazin/3x-ui-subpage/internal/generator/clash"
	"github.com/irazin/3x-ui-subpage/internal/store"
	"github.com/irazin/3x-ui-subpage/internal/templatestore"
	"github.com/irazin/3x-ui-subpage/internal/theme"
)

func newTestServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := adminauth.New(db).CreateUser("admin", "correct-horse-battery-staple"); err != nil {
		t.Fatalf("create admin user: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(db, logger), db
}

// loginAndGetCookie logs in against s.Router() and returns the resulting
// session cookie for use on subsequent requests.
func loginAndGetCookie(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	form := url.Values{"username": {"admin"}, "password": {"correct-horse-battery-staple"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("login: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	t.Fatal("login: no session cookie set")
	return nil
}

// csrfTokenFor extracts the current session's CSRF token by hitting the
// dashboard (which always renders it in the logout form).
func csrfTokenFor(t *testing.T, s *Server, cookie *http.Cookie) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	body := rec.Body.String()
	const marker = `name="csrf_token" value="`
	i := strings.Index(body, marker)
	if i == -1 {
		t.Fatalf("csrf token not found in dashboard body: %s", body)
	}
	rest := body[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

func TestLogin_Success(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	if cookie.Value == "" {
		t.Fatal("expected non-empty session cookie value")
	}
	if !cookie.HttpOnly {
		t.Error("expected session cookie to be HttpOnly")
	}
}

func TestLogin_WrongPasswordFails(t *testing.T) {
	s, _ := newTestServer(t)
	form := url.Values{"username": {"admin"}, "password": {"wrong"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid username or password") {
		t.Errorf("expected error message in body, got: %s", rec.Body.String())
	}
}

func TestRequireSession_RedirectsUnauthenticated(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("expected redirect to /admin/login, got %q", loc)
	}
}

func TestRequireSession_InvalidCookieRedirects(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "bogus-session-id"})
	rec := httptest.NewRecorder()

	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
}

func TestCSRF_RejectsPostWithoutToken(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)

	form := url.Values{"name": {"Test App"}}
	req := httptest.NewRequest(http.MethodPost, "/applications", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without csrf token, got %d", rec.Code)
	}
}

func TestCSRF_AcceptsWithValidToken(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	form := url.Values{"name": {"Test App"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/applications", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()

	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 after successful create, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLogout_ClearsSession(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}

	// The same session id should no longer work.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	s.Router().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusFound || rec2.Header().Get("Location") != "/admin/login" {
		t.Errorf("expected session to be invalidated after logout, got %d %q", rec2.Code, rec2.Header().Get("Location"))
	}
}

func TestApplications_FullCRUDRoundTrip(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	// Create
	form := url.Values{
		"name": {"Clash Verge"}, "icon": {"clash.png"}, "platforms": {"Windows, macOS"},
		"deeplink": {"clash://x"}, "sort_order": {"1"}, "visible": {"1"}, "csrf_token": {token},
	}
	req := httptest.NewRequest(http.MethodPost, "/applications", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("create: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	// List should show it
	req = httptest.NewRequest(http.MethodGet, "/applications", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "Clash Verge") {
		t.Fatalf("expected list to contain created app, got: %s", rec.Body.String())
	}

	all, err := s.apps.ListAll()
	if err != nil || len(all) != 1 {
		t.Fatalf("expected exactly 1 app, got %v (err=%v)", all, err)
	}
	id := all[0].ID

	// Update
	form = url.Values{
		"name": {"Clash Verge Renamed"}, "platforms": {"Windows"}, "sort_order": {"2"}, "csrf_token": {token},
	}
	req = httptest.NewRequest(http.MethodPost, "/applications/"+itoa(id), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("update: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	updated, err := s.apps.Get(id)
	if err != nil || updated.Name != "Clash Verge Renamed" {
		t.Fatalf("expected updated name, got %+v (err=%v)", updated, err)
	}

	// Delete
	form = url.Values{"csrf_token": {token}}
	req = httptest.NewRequest(http.MethodPost, "/applications/"+itoa(id)+"/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("delete: expected 302, got %d", rec.Code)
	}

	all, err = s.apps.ListAll()
	if err != nil || len(all) != 0 {
		t.Fatalf("expected 0 apps after delete, got %v", all)
	}
}

func TestThemes_MetaAndFileCRUD_ReflectedByEngine(t *testing.T) {
	s, db := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	form := url.Values{"name": {"Gaming Theme"}, "colors": {`{"primary":"#f00"}`}, "fonts": {"{}"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/themes/gaming", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("theme meta save: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	form = url.Values{"content": {`{{define "layout"}}{{template "content" .}}{{end}}`}, "csrf_token": {token}}
	req = httptest.NewRequest(http.MethodPost, "/themes/gaming/files/layout.html", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("layout save: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	form = url.Values{"content": {`{{define "content"}}hello-from-admin-ui{{end}}`}, "csrf_token": {token}}
	req = httptest.NewRequest(http.MethodPost, "/themes/gaming/files/pages/subscription.html", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("content save: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	engine := theme.New(db, "gaming")
	var buf strings.Builder
	if err := engine.Render(&buf, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "hello-from-admin-ui") {
		t.Errorf("expected theme.Engine to reflect admin-UI-written files, got: %s", buf.String())
	}
}

func TestTemplates_SaveIsPickedUpByGenerator(t *testing.T) {
	s, db := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	form := url.Values{"content": {"proxies: []\nmarker: from-admin-ui\n"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/templates/clash/default/-", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("template save: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	gen := clash.New(db)
	out, err := gen.Build(nil, "default")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(out, "from-admin-ui") {
		t.Errorf("expected generator to pick up admin-saved template, got: %s", out)
	}
}

func TestAssignments_SaveIsPickedUpByResolver(t *testing.T) {
	s, db := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	form := url.Values{"sub_id": {"tok-alice"}, "profile": {"gaming"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/assignments", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("assignment save: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	profile, err := assignment.New(db).Resolve("tok-alice")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if profile != "gaming" {
		t.Errorf("expected resolver to reflect admin-saved assignment, got %q", profile)
	}

	// Delete it
	form = url.Values{"csrf_token": {token}}
	req = httptest.NewRequest(http.MethodPost, "/assignments/tok-alice/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("assignment delete: expected 302, got %d", rec.Code)
	}

	profile, err = assignment.New(db).Resolve("tok-alice")
	if err != nil {
		t.Fatalf("Resolve after delete: %v", err)
	}
	if profile != assignment.DefaultProfile {
		t.Errorf("expected fallback to default after delete, got %q", profile)
	}
}

func TestSettings_SaveValidAndInvalid(t *testing.T) {
	s, db := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	form := url.Values{"value": {`{"telegram":"https://t.me/example"}`}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/settings/support", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("valid save: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	value, err := config.GetSetting(db, "support")
	if err != nil || value != `{"telegram":"https://t.me/example"}` {
		t.Fatalf("unexpected stored setting: %q (err=%v)", value, err)
	}

	form = url.Values{"value": {`not valid json`}, "csrf_token": {token}}
	req = httptest.NewRequest(http.MethodPost, "/settings/support", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid save: expected 400, got %d", rec.Code)
	}
}

func TestThemesLookup_RedirectsToSlugRoute(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)

	req := httptest.NewRequest(http.MethodGet, "/themes/lookup?slug=gaming", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/themes/gaming" {
		t.Errorf("expected redirect to /admin/themes/gaming, got %q", loc)
	}
}

func TestThemesList_NotShadowedByLookupRoute(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)

	req := httptest.NewRequest(http.MethodGet, "/themes", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for themes list, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "New or existing theme slug") {
		t.Errorf("expected themes list page, got: %s", rec.Body.String())
	}
}

func TestThemeNew_ShowsBlankFormWithoutError(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)

	req := httptest.NewRequest(http.MethodGet, "/themes/does-not-exist-yet", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for new theme form, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestThemeFileDelete(t *testing.T) {
	s, db := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	themeAdmin := theme.NewAdminStore(db)
	if err := themeAdmin.UpsertMeta("t", theme.Meta{Name: "T"}); err != nil {
		t.Fatalf("UpsertMeta: %v", err)
	}
	if err := themeAdmin.PutFile("t", "static/css/style.css", []byte("body{}")); err != nil {
		t.Fatalf("PutFile: %v", err)
	}

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/themes/t/files-delete/static/css/style.css", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := themeAdmin.GetFile("t", "static/css/style.css"); err != theme.ErrNotFound {
		t.Errorf("expected file to be deleted, got err=%v", err)
	}
}

func TestTemplatesLookup_RedirectsWithPlaceholderProtocol(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)

	req := httptest.NewRequest(http.MethodGet, "/templates/lookup?format=clash&profile=default&protocol=", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/templates/clash/default/-" {
		t.Errorf("expected placeholder protocol in redirect, got %q", loc)
	}
}

func TestTemplateDelete(t *testing.T) {
	s, db := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	if err := (templatestore.New(db)).Put("clash", "gaming", "", "proxies: []"); err != nil {
		t.Fatalf("seed template: %v", err)
	}

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/templates/clash/gaming/-/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := templatestore.New(db).Get("clash", "gaming", ""); err == nil {
		t.Error("expected template to be deleted")
	}
}

func TestRouting_FullCRUDRoundTrip(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	// Create
	form := url.Values{
		"profile": {"gaming"}, "type": {"geoip"}, "value": {"CN"}, "outbound": {"direct"},
		"sort_order": {"0"}, "enabled": {"1"}, "csrf_token": {token},
	}
	req := httptest.NewRequest(http.MethodPost, "/routing", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("create: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	// List should show it
	req = httptest.NewRequest(http.MethodGet, "/routing", nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "gaming") {
		t.Fatalf("expected list to contain created rule, got: %s", rec.Body.String())
	}

	all, err := s.routing.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("expected exactly 1 rule, got %v (err=%v)", all, err)
	}
	id := all[0].ID

	// Update
	form = url.Values{
		"profile": {"gaming"}, "type": {"cidr"}, "value": {"10.0.0.0/8"}, "outbound": {"proxy"},
		"sort_order": {"5"}, "csrf_token": {token},
	}
	req = httptest.NewRequest(http.MethodPost, "/routing/"+itoa(id), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("update: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	updated, err := s.routing.Get(id)
	if err != nil || updated.Type != "cidr" || updated.Enabled {
		t.Fatalf("expected updated+disabled rule (checkbox omitted), got %+v (err=%v)", updated, err)
	}

	// Delete
	form = url.Values{"csrf_token": {token}}
	req = httptest.NewRequest(http.MethodPost, "/routing/"+itoa(id)+"/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("delete: expected 302, got %d", rec.Code)
	}

	all, err = s.routing.List()
	if err != nil || len(all) != 0 {
		t.Fatalf("expected 0 rules after delete, got %v", all)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
