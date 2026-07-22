package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/irazin/3x-ui-subpage/internal/sync"
	"github.com/irazin/3x-ui-subpage/internal/users"
	"github.com/irazin/3x-ui-subpage/internal/xui"
)

func createUserViaHandler(t *testing.T, s *Server, cookie *http.Cookie, token, username, subID string) int64 {
	t.Helper()
	form := url.Values{"username": {username}, "sub_id": {subID}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("create user: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	all, _, err := s.users.List(users.ListFilter{Query: username})
	if err != nil || len(all) != 1 {
		t.Fatalf("expected exactly 1 user named %q, got %v (err=%v)", username, all, err)
	}
	return all[0].ID
}

func TestUsers_CreateListDetail(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	id := createUserViaHandler(t, s, cookie, token, "alice", "sub-alice")

	req := httptest.NewRequest(http.MethodGet, "/users", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "alice") {
		t.Fatalf("expected list to contain alice, got: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/users/"+itoa(id), nil)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "sub-alice") {
		t.Fatalf("expected detail page to show sub-alice, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "https://sub.example.com/sub/sub-alice") {
		t.Fatalf("expected detail page to show the full subscription URL, got: %s", body)
	}
	if !strings.Contains(body, "/sub/sub-alice/qr.png") || !strings.Contains(body, "/sub/sub-alice/qr.svg") {
		t.Fatalf("expected detail page to reference the QR endpoints, got: %s", body)
	}
	if !strings.Contains(body, "data-copy=") {
		t.Fatalf("expected detail page to have a copy button, got: %s", body)
	}

	u, err := s.users.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if u.UUID == "" || u.Password == "" || !u.Enabled {
		t.Fatalf("expected generated credentials and enabled=true, got %+v", u)
	}
}

func TestUsers_Update(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	id := createUserViaHandler(t, s, cookie, token, "bob", "sub-bob")

	form := url.Values{
		"username": {"bob2"}, "sub_id": {"sub-bob"}, "total_gb": {"5"}, "expiry": {"2030-01-01"},
		"csrf_token": {token},
	}
	req := httptest.NewRequest(http.MethodPost, "/users/"+itoa(id), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("update: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	u, err := s.users.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if u.Username != "bob2" || u.TotalGB != 5_000_000_000 {
		t.Fatalf("update didn't apply: %+v", u)
	}
}

func TestUsers_SetInbounds_EnqueuesAssignAndUnassign(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	lister := s.inbounds.(*fakeInboundLister)
	lister.Inbounds = []xui.Inbound{
		{ID: 10, Remark: "vless-in", Protocol: "vless", Enable: true},
		{ID: 11, Remark: "trojan-in", Protocol: "trojan", Enable: true},
	}

	id := createUserViaHandler(t, s, cookie, token, "carol", "sub-carol")

	form := url.Values{"inbound_id": {"10", "11"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/users/"+itoa(id)+"/inbounds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("set inbounds: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	assigned, err := s.users.Inbounds(id)
	if err != nil || len(assigned) != 2 {
		t.Fatalf("expected 2 assignments, got %v (err=%v)", assigned, err)
	}

	jobs, err := s.syncJobs.ListForUser(id, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 assign jobs, got %+v", jobs)
	}
	for _, j := range jobs {
		if j.Op != sync.OpAssign {
			t.Fatalf("expected op=assign, got %+v", j)
		}
		if j.Payload.Email != "carol" || j.Payload.SubID != "sub-carol" {
			t.Fatalf("unexpected payload snapshot: %+v", j.Payload)
		}
	}

	// Now unassign inbound 11.
	form = url.Values{"inbound_id": {"10"}, "csrf_token": {token}}
	req = httptest.NewRequest(http.MethodPost, "/users/"+itoa(id)+"/inbounds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("unassign: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	jobs, err = s.syncJobs.ListForUser(id, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	var unassignCount int
	for _, j := range jobs {
		if j.Op == sync.OpUnassign {
			unassignCount++
		}
	}
	if unassignCount != 1 {
		t.Fatalf("expected 1 unassign job, got %+v", jobs)
	}
}

func TestUsers_Toggle_EnqueuesUpdatePerAssignment(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	lister := s.inbounds.(*fakeInboundLister)
	lister.Inbounds = []xui.Inbound{{ID: 10, Remark: "vless-in", Protocol: "vless", Enable: true}}

	id := createUserViaHandler(t, s, cookie, token, "dave", "sub-dave")
	assignInbounds(t, s, cookie, token, id, "10")

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/users/"+itoa(id)+"/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("toggle: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	u, err := s.users.Get(id)
	if err != nil || u.Enabled {
		t.Fatalf("expected disabled after toggle, got %+v (err=%v)", u, err)
	}

	jobs, err := s.syncJobs.ListForUser(id, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	var updateJobs int
	for _, j := range jobs {
		if j.Op == sync.OpUpdate {
			updateJobs++
			if j.Payload.Enable {
				t.Fatalf("expected payload.Enable=false after disabling, got %+v", j.Payload)
			}
		}
	}
	if updateJobs != 1 {
		t.Fatalf("expected 1 update job from toggle, got %+v", jobs)
	}
}

func TestUsers_ResetTraffic_EnqueuesResetPerAssignment(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	lister := s.inbounds.(*fakeInboundLister)
	lister.Inbounds = []xui.Inbound{{ID: 10, Remark: "vless-in", Protocol: "vless", Enable: true}}

	id := createUserViaHandler(t, s, cookie, token, "erin", "sub-erin")
	assignInbounds(t, s, cookie, token, id, "10")

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/users/"+itoa(id)+"/reset-traffic", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("reset-traffic: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	jobs, err := s.syncJobs.ListForUser(id, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	var resetJobs int
	for _, j := range jobs {
		if j.Op == sync.OpResetTraffic {
			resetJobs++
		}
	}
	if resetJobs != 1 {
		t.Fatalf("expected 1 reset_traffic job, got %+v", jobs)
	}
}

func TestUsers_RegenerateUUID_ChangesCredentialsAndEnqueuesUpdate(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	lister := s.inbounds.(*fakeInboundLister)
	lister.Inbounds = []xui.Inbound{{ID: 10, Remark: "vless-in", Protocol: "vless", Enable: true}}

	id := createUserViaHandler(t, s, cookie, token, "frank", "sub-frank")
	assignInbounds(t, s, cookie, token, id, "10")

	before, err := s.users.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/users/"+itoa(id)+"/regenerate-uuid", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("regenerate-uuid: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	after, err := s.users.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.UUID == before.UUID || after.Password == before.Password {
		t.Fatalf("expected new credentials, before=%+v after=%+v", before, after)
	}

	jobs, err := s.syncJobs.ListForUser(id, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	var updateJobs int
	for _, j := range jobs {
		if j.Op == sync.OpUpdate && j.Payload.UUID == after.UUID {
			updateJobs++
		}
	}
	if updateJobs != 1 {
		t.Fatalf("expected 1 update job carrying the new uuid, got %+v", jobs)
	}
}

func TestUsers_Delete_EnqueuesUnassignThenRemovesUser(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	lister := s.inbounds.(*fakeInboundLister)
	lister.Inbounds = []xui.Inbound{{ID: 10, Remark: "vless-in", Protocol: "vless", Enable: true}}

	id := createUserViaHandler(t, s, cookie, token, "grace", "sub-grace")
	assignInbounds(t, s, cookie, token, id, "10")

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/users/"+itoa(id)+"/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("delete: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	if _, err := s.users.Get(id); err != users.ErrNotFound {
		t.Fatalf("expected user to be gone, got err=%v", err)
	}

	jobs, err := s.syncJobs.ListForUser(id, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	var deleteJobs int
	for _, j := range jobs {
		if j.Op == sync.OpDelete {
			deleteJobs++
			if j.Payload.Email != "grace" {
				t.Fatalf("expected delete job for grace, got %+v", j.Payload)
			}
		}
	}
	if deleteJobs != 1 {
		t.Fatalf("expected 1 delete job enqueued before removing the local user, got %+v", jobs)
	}
}

func TestUsers_Delete_NoSyncJobWhenNeverAssigned(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	id := createUserViaHandler(t, s, cookie, token, "hank", "sub-hank")

	form := url.Values{"csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/users/"+itoa(id)+"/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("delete: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	jobs, err := s.syncJobs.ListForUser(id, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected no sync jobs for a user that was never assigned an inbound, got %+v", jobs)
	}
}

func TestUsers_BulkDisable(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	id1 := createUserViaHandler(t, s, cookie, token, "heidi", "sub-heidi")
	id2 := createUserViaHandler(t, s, cookie, token, "ivan", "sub-ivan")

	form := url.Values{"ids": {itoa(id1), itoa(id2)}, "action": {"disable"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/users/bulk", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("bulk: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	u1, err := s.users.Get(id1)
	if err != nil || u1.Enabled {
		t.Fatalf("expected heidi disabled, got %+v (err=%v)", u1, err)
	}
	u2, err := s.users.Get(id2)
	if err != nil || u2.Enabled {
		t.Fatalf("expected ivan disabled, got %+v (err=%v)", u2, err)
	}
}

func TestUsers_List_SearchAndStatusFilter(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	createUserViaHandler(t, s, cookie, token, "judy", "sub-judy")
	createUserViaHandler(t, s, cookie, token, "kevin", "sub-kevin")

	req := httptest.NewRequest(http.MethodGet, "/users?q=jud", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "judy") || strings.Contains(body, "kevin") {
		t.Fatalf("expected search to show only judy, got: %s", body)
	}
}

func TestSyncLog_ListAndRetry(t *testing.T) {
	s, _ := newTestServer(t)
	cookie := loginAndGetCookie(t, s)
	token := csrfTokenFor(t, s, cookie)

	id, err := s.syncJobs.Enqueue(1, 10, sync.OpAssign, sync.Payload{Email: "leo"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := s.syncJobs.ClaimBatch(10); err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if err := s.syncJobs.MarkFailedTerminal(id, 8, "panel unreachable"); err != nil {
		t.Fatalf("MarkFailedTerminal: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/sync", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "leo") || !strings.Contains(rec.Body.String(), "panel unreachable") {
		t.Fatalf("expected sync log to show the failed job, got: %s", rec.Body.String())
	}

	form := url.Values{"csrf_token": {token}}
	req = httptest.NewRequest(http.MethodPost, "/sync/"+itoa(id)+"/retry", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("retry: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}

	jobs, err := s.syncJobs.ListForUser(1, 10)
	if err != nil || jobs[0].Status != sync.StatusPending {
		t.Fatalf("expected retried job to be pending again, got %+v (err=%v)", jobs, err)
	}
}

// assignInbounds is a small test helper that posts the inbound assignment
// form for a user, used by tests that need an assignment set up before
// exercising a different action.
func assignInbounds(t *testing.T, s *Server, cookie *http.Cookie, token string, userID int64, inboundIDs ...string) {
	t.Helper()
	form := url.Values{"inbound_id": inboundIDs, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/users/"+itoa(userID)+"/inbounds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("assign inbounds: expected 302, got %d: %s", rec.Code, rec.Body.String())
	}
}
