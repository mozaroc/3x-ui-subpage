package admin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/assignment"
	"github.com/irazin/3x-ui-subpage/internal/connlink"
	"github.com/irazin/3x-ui-subpage/internal/generator/tmplctx"
	"github.com/irazin/3x-ui-subpage/internal/sync"
	"github.com/irazin/3x-ui-subpage/internal/users"
	"github.com/irazin/3x-ui-subpage/internal/xui"
)

const usersPageSize = 25

// supportedProtocols mirrors xui.MatchedClientsBySubID's protocol allowlist
// — only inbounds of these protocols carry a client list a User can be
// assigned to.
var supportedProtocols = map[string]bool{
	"vless": true, "vmess": true, "trojan": true, "shadowsocks": true,
}

func parseID64(r *http.Request) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
}

// payloadFor snapshots the fields a sync job needs from u, at enqueue time.
func payloadFor(u users.User) sync.Payload {
	return sync.Payload{
		Email:    u.Username,
		SubID:    u.SubID,
		UUID:     u.UUID,
		Password: u.Password,
		Method:   u.Method,
		Flow:     u.Flow,
		Enable:   u.Enabled,
		TotalGB:  u.TotalGB,
		ExpiryMs: u.ExpiryMs,
	}
}

func (s *Server) enqueueUpdateForAllAssignments(userID int64, u users.User) error {
	assignments, err := s.users.Inbounds(userID)
	if err != nil {
		return err
	}
	payload := payloadFor(u)
	var firstErr error
	for _, a := range assignments {
		if _, err := s.syncJobs.Enqueue(userID, a.InboundID, sync.OpUpdate, payload); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func formatBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	return humanize.Bytes(uint64(n))
}

func formatBytesLimit(n int64) string {
	if n <= 0 {
		return "Unlimited"
	}
	return humanize.Bytes(uint64(n))
}

func bytesToGBString(n int64) string {
	if n <= 0 {
		return ""
	}
	return strconv.FormatFloat(float64(n)/1e9, 'f', -1, 64)
}

func expiryInputValue(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02")
}

// userFromForm parses the shared create/edit form fields. It never sets
// Enabled — that's changed exclusively via the dedicated toggle action.
func userFromForm(r *http.Request) (users.User, error) {
	username := strings.TrimSpace(r.FormValue("username"))
	subID := strings.TrimSpace(r.FormValue("sub_id"))
	if username == "" || subID == "" {
		return users.User{}, fmt.Errorf("username and subscription id are required")
	}

	var totalBytes int64
	if v := strings.TrimSpace(r.FormValue("total_gb")); v != "" {
		gb, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return users.User{}, fmt.Errorf("invalid traffic limit %q", v)
		}
		totalBytes = int64(gb * 1e9)
	}

	var expiryMs int64
	if v := strings.TrimSpace(r.FormValue("expiry")); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return users.User{}, fmt.Errorf("invalid expiry date %q (use YYYY-MM-DD)", v)
		}
		expiryMs = t.UnixMilli()
	}

	return users.User{
		Username: username,
		SubID:    subID,
		Flow:     r.FormValue("flow"),
		Method:   r.FormValue("method"),
		Notes:    r.FormValue("notes"),
		TotalGB:  totalBytes,
		ExpiryMs: expiryMs,
	}, nil
}

// clientTypeOption is one client-type template-profile selector, ready to
// render as a <select> on the User create/edit page.
type clientTypeOption struct {
	Key      string
	Label    string
	Options  []string
	Selected string
}

// clientTypeOptions builds the per-client-type template selector list for
// subID (pass "" for a brand-new user that has no assignments yet -- every
// client type simply comes back selected on "default").
func (s *Server) clientTypeOptions(subID string) ([]clientTypeOption, error) {
	assigned, err := s.assignments.ForSubID(subID)
	if err != nil {
		return nil, fmt.Errorf("resolve template assignments for %s: %w", subID, err)
	}

	out := make([]clientTypeOption, 0, len(assignment.ClientTypes))
	for _, ct := range assignment.ClientTypes {
		profiles, err := s.templates.ProfilesForFormats(ct.Formats)
		if err != nil {
			return nil, fmt.Errorf("list profiles for %s: %w", ct.Key, err)
		}
		out = append(out, clientTypeOption{
			Key: ct.Key, Label: ct.Label, Options: profiles, Selected: assigned[ct.Key],
		})
	}
	return out, nil
}

// applyAssignmentsFromForm reads one profile_<key> field per known client
// type and upserts it for subID. A blank/missing field normalizes to
// assignment.DefaultProfile -- otherwise a submission that skips these
// fields (e.g. an API caller, or a test that doesn't render the real form)
// would persist a literal empty-string profile instead of falling back to
// "default".
func (s *Server) applyAssignmentsFromForm(r *http.Request, subID string) error {
	for _, ct := range assignment.ClientTypes {
		profile := strings.TrimSpace(r.FormValue("profile_" + ct.Key))
		if profile == "" {
			profile = assignment.DefaultProfile
		}
		if err := s.assignments.Set(subID, ct.Key, profile); err != nil {
			return err
		}
	}
	return nil
}

// userRoutingView is the per-user Happ/Incy routing section, ready to
// render on the User create/edit page: a toggle plus the pasted Base64
// blob authored on the standalone Routing Generator page (see
// handlers_routing.go).
type userRoutingView struct {
	Enabled bool
	B64     string
}

// userRoutingFromForm parses the User form's routing fields and validates
// the pasted Base64 string, if any.
func userRoutingFromForm(r *http.Request) (enabled bool, routingB64 string, err error) {
	enabled = r.FormValue("routing_enabled") != ""
	routingB64 = strings.TrimSpace(r.FormValue("routing_b64"))
	if routingB64 != "" {
		if _, decodeErr := base64.StdEncoding.DecodeString(routingB64); decodeErr != nil {
			return false, "", fmt.Errorf("routing configuration must be a valid Base64 string")
		}
	}
	return enabled, routingB64, nil
}

type userRow struct {
	ID           int64
	Username     string
	SubID        string
	Enabled      bool
	Status       string
	TrafficUsed  string
	TrafficLimit string
	Expiry       string
	InboundCount int
	InboundTags  string
	SyncStatus   string
}

func (s *Server) buildUserRow(r *http.Request, u users.User) userRow {
	assignments, err := s.users.Inbounds(u.ID)
	if err != nil {
		s.logger.Warn("admin: list user inbounds failed", "user_id", u.ID, "err", err)
	}
	tags := make([]string, 0, len(assignments))
	for _, a := range assignments {
		tags = append(tags, a.InboundTag)
	}

	status := "Not synced"
	trafficUsed := "0 B"
	trafficLimit := formatBytesLimit(u.TotalGB)
	expiry := formatExpiryDisplay(u.ExpiryMs)

	if sub, err := s.resolve.Resolve(r.Context(), u.SubID); err == nil {
		status = capitalize(string(sub.Status))
		trafficUsed = formatBytes(sub.Traffic.Used())
		trafficLimit = formatBytesLimit(sub.Traffic.Total)
		if sub.ExpiresAt != nil {
			expiry = sub.ExpiresAt.Format("2006-01-02")
		} else {
			expiry = "Never"
		}
	} else if !u.Enabled {
		status = "Disabled"
	}

	syncStatus := "none"
	if st, err := s.syncJobs.RollupStatusForUser(u.ID); err == nil {
		syncStatus = st
	}

	return userRow{
		ID: u.ID, Username: u.Username, SubID: u.SubID, Enabled: u.Enabled,
		Status: status, TrafficUsed: trafficUsed, TrafficLimit: trafficLimit, Expiry: expiry,
		InboundCount: len(assignments), InboundTags: strings.Join(tags, ", "),
		SyncStatus: syncStatus,
	}
}

func formatExpiryDisplay(ms int64) string {
	if ms <= 0 {
		return "Never"
	}
	return time.UnixMilli(ms).Format("2006-01-02")
}

type usersListPageData struct {
	Users      []userRow
	Query      string
	Status     string
	SortBy     string
	SortDir    string
	Page       int
	PrevPage   int
	NextPage   int
	TotalPages int
	Error      string
}

func (s *Server) handleUsersList(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	q := r.URL.Query()
	filter := users.ListFilter{
		Query:   q.Get("q"),
		Status:  q.Get("status"),
		SortBy:  q.Get("sort"),
		SortDir: q.Get("dir"),
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	filter.Limit = usersPageSize
	filter.Offset = (page - 1) * usersPageSize

	all, total, err := s.users.List(filter)
	if err != nil {
		s.logger.Error("admin: list users failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rows := make([]userRow, 0, len(all))
	for _, u := range all {
		rows = append(rows, s.buildUserRow(r, u))
	}

	totalPages := (total + usersPageSize - 1) / usersPageSize
	if totalPages < 1 {
		totalPages = 1
	}

	_ = render(w, "page-users-list", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: usersListPageData{
			Users: rows, Query: filter.Query, Status: filter.Status,
			SortBy: filter.SortBy, SortDir: filter.SortDir,
			Page: page, PrevPage: page - 1, NextPage: page + 1, TotalPages: totalPages,
		},
	})
}

type userFormPageData struct {
	IsNew       bool
	ClientTypes []clientTypeOption
	Routing     userRoutingView
	Error       string
}

func (s *Server) handleUserForm(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	clientTypes, err := s.clientTypeOptions("")
	if err != nil {
		s.logger.Error("admin: load client type options failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-user-form", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: userFormPageData{IsNew: true, ClientTypes: clientTypes},
	})
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	u, err := userFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	routingEnabled, routingB64, err := userRoutingFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u.Enabled = true

	id, err := s.users.Create(u)
	if errors.Is(err, users.ErrDuplicate) {
		http.Error(w, "username or subscription id already in use", http.StatusConflict)
		return
	}
	if err != nil {
		s.logger.Error("admin: create user failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := s.applyAssignmentsFromForm(r, u.SubID); err != nil {
		s.logger.Error("admin: set template assignments failed", "id", id, "err", err)
	}
	if err := s.routing.Set(u.SubID, routingEnabled, routingB64); err != nil {
		s.logger.Error("admin: set routing failed", "id", id, "err", err)
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/users/%d", id), http.StatusFound)
}

type inboundOption struct {
	ID       int
	Tag      string
	Protocol string
	Assigned bool
}

type syncJobRow struct {
	ID        int64
	InboundID int
	Op        string
	Status    string
	Attempts  int
	LastError string
	UpdatedAt string
}

type userDetailPageData struct {
	User            users.User
	TotalGBValue    string
	ExpiryValue     string
	Status          string
	TrafficUsed     string
	TrafficLimit    string
	LiveExpiry      string
	SyncStatus      string
	SubscriptionURL string
	QRPngURL        string
	Inbounds        []inboundOption
	ClientTypes     []clientTypeOption
	Routing         userRoutingView
	Connections     []connlink.View
	SyncJobs        []syncJobRow
	Error           string
}

func (s *Server) handleUserDetail(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)
	id, err := parseID64(r)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	u, err := s.users.Get(id)
	if errors.Is(err, users.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.logger.Error("admin: get user failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	assignments, err := s.users.Inbounds(id)
	if err != nil {
		s.logger.Error("admin: list user inbounds failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	assignedByID := make(map[int]bool, len(assignments))
	for _, a := range assignments {
		assignedByID[a.InboundID] = true
	}

	var options []inboundOption
	if live, err := s.inbounds.ListInbounds(r.Context()); err != nil {
		s.logger.Warn("admin: list live inbounds failed", "err", err)
	} else {
		for _, ib := range live {
			if !supportedProtocols[ib.Protocol] {
				continue
			}
			options = append(options, inboundOption{ID: ib.ID, Tag: ib.Remark, Protocol: ib.Protocol, Assigned: assignedByID[ib.ID]})
		}
	}

	status, trafficUsed, trafficLimit, liveExpiry := "Not synced", "0 B", formatBytesLimit(u.TotalGB), "n/a"
	sub, resolveErr := s.resolve.Resolve(r.Context(), u.SubID)
	if resolveErr == nil {
		status = capitalize(string(sub.Status))
		trafficUsed = formatBytes(sub.Traffic.Used())
		trafficLimit = formatBytesLimit(sub.Traffic.Total)
		if sub.ExpiresAt != nil {
			liveExpiry = sub.ExpiresAt.Format("2006-01-02")
		} else {
			liveExpiry = "Never"
		}
	} else if !u.Enabled {
		status = "Disabled"
	}

	syncStatus := "none"
	if st, err := s.syncJobs.RollupStatusForUser(id); err == nil {
		syncStatus = st
	}

	jobs, err := s.syncJobs.ListForUser(id, 20)
	if err != nil {
		s.logger.Warn("admin: list sync jobs failed", "id", id, "err", err)
	}
	jobRows := make([]syncJobRow, 0, len(jobs))
	for _, j := range jobs {
		jobRows = append(jobRows, syncJobRow{
			ID: j.ID, InboundID: j.InboundID, Op: j.Op, Status: j.Status,
			Attempts: j.Attempts, LastError: j.LastError,
			UpdatedAt: time.Unix(0, j.UpdatedAt).Format("2006-01-02 15:04:05"),
		})
	}

	clientTypes, err := s.clientTypeOptions(u.SubID)
	if err != nil {
		s.logger.Error("admin: load client type options failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	routingEnabled, routingB64, err := s.routing.Get(u.SubID)
	if err != nil {
		s.logger.Error("admin: load routing failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Direct connection links are the panel's own canonical share links,
	// verbatim -- no profile, no generation, just parsed for display.
	var connections []connlink.View
	if resolveErr == nil {
		connections = connlink.Build(u.SubID, tmplctx.ParseEntries(sub.Links))
	}

	_ = render(w, "page-user-detail", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: userDetailPageData{
			User:         u,
			TotalGBValue: bytesToGBString(u.TotalGB),
			ExpiryValue:  expiryInputValue(u.ExpiryMs),
			Status:       status, TrafficUsed: trafficUsed, TrafficLimit: trafficLimit, LiveExpiry: liveExpiry,
			SyncStatus:      syncStatus,
			SubscriptionURL: fmt.Sprintf("%s/sub/%s", strings.TrimSuffix(s.publicURL, "/"), u.SubID),
			QRPngURL:        fmt.Sprintf("/sub/%s/qr.png", u.SubID),
			Inbounds:        options, ClientTypes: clientTypes,
			Routing:     userRoutingView{Enabled: routingEnabled, B64: routingB64},
			Connections: connections, SyncJobs: jobRows,
		},
	})
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := parseID64(r)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	u, err := userFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	routingEnabled, routingB64, err := userRoutingFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	existing, err := s.users.Get(id)
	if errors.Is(err, users.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.logger.Error("admin: get user before update failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	oldSubID := existing.SubID

	if err := s.users.Update(id, u); err != nil {
		if errors.Is(err, users.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, users.ErrDuplicate) {
			http.Error(w, "username or subscription id already in use", http.StatusConflict)
			return
		}
		s.logger.Error("admin: update user failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if oldSubID != u.SubID {
		if err := s.assignments.DeleteAll(oldSubID); err != nil {
			s.logger.Error("admin: clean up template assignments for old sub_id failed", "id", id, "err", err)
		}
		if err := s.routing.DeleteAll(oldSubID); err != nil {
			s.logger.Error("admin: clean up routing profile for old sub_id failed", "id", id, "err", err)
		}
	}
	if err := s.applyAssignmentsFromForm(r, u.SubID); err != nil {
		s.logger.Error("admin: set template assignments failed", "id", id, "err", err)
	}
	if err := s.routing.Set(u.SubID, routingEnabled, routingB64); err != nil {
		s.logger.Error("admin: set routing failed", "id", id, "err", err)
	}

	if updated, err := s.users.Get(id); err != nil {
		s.logger.Error("admin: get user after update failed", "id", id, "err", err)
	} else if err := s.enqueueUpdateForAllAssignments(id, updated); err != nil {
		s.logger.Error("admin: enqueue update sync jobs failed", "id", id, "err", err)
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/users/%d", id), http.StatusFound)
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID64(r)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	u, err := s.users.Get(id)
	if errors.Is(err, users.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.logger.Error("admin: get user failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	assignments, err := s.users.Inbounds(id)
	if err != nil {
		s.logger.Error("admin: list user inbounds failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// One delete call removes the client from every inbound and drops its
	// panel-side record in one step — deliberately not N per-inbound
	// unassigns, since detaching the last inbound doesn't reliably leave
	// nothing behind (confirmed empirically: some panel states leave an
	// orphaned zero-inbound client instead of auto-removing it).
	if len(assignments) > 0 {
		if _, err := s.syncJobs.Enqueue(id, 0, sync.OpDelete, payloadFor(u)); err != nil {
			s.logger.Error("admin: enqueue delete on delete failed", "id", id, "err", err)
		}
	}

	if err := s.assignments.DeleteAll(u.SubID); err != nil {
		s.logger.Error("admin: clean up template assignments on delete failed", "id", id, "err", err)
	}
	if err := s.routing.DeleteAll(u.SubID); err != nil {
		s.logger.Error("admin: clean up routing profile on delete failed", "id", id, "err", err)
	}

	if err := s.users.Delete(id); err != nil && !errors.Is(err, users.ErrNotFound) {
		s.logger.Error("admin: delete user failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

func (s *Server) handleUserToggle(w http.ResponseWriter, r *http.Request) {
	id, err := parseID64(r)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	u, err := s.users.Get(id)
	if errors.Is(err, users.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.logger.Error("admin: get user failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	newEnabled := !u.Enabled
	if err := s.users.SetEnabled(id, newEnabled); err != nil {
		s.logger.Error("admin: set enabled failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	u.Enabled = newEnabled

	if err := s.enqueueUpdateForAllAssignments(id, u); err != nil {
		s.logger.Error("admin: enqueue update sync jobs failed", "id", id, "err", err)
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/users/%d", id), http.StatusFound)
}

func (s *Server) handleUserResetTraffic(w http.ResponseWriter, r *http.Request) {
	id, err := parseID64(r)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	u, err := s.users.Get(id)
	if errors.Is(err, users.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.logger.Error("admin: get user failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	assignments, err := s.users.Inbounds(id)
	if err != nil {
		s.logger.Error("admin: list user inbounds failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	payload := payloadFor(u)
	for _, a := range assignments {
		if _, err := s.syncJobs.Enqueue(id, a.InboundID, sync.OpResetTraffic, payload); err != nil {
			s.logger.Error("admin: enqueue reset traffic failed", "id", id, "inbound_id", a.InboundID, "err", err)
		}
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/users/%d", id), http.StatusFound)
}

func (s *Server) handleUserRegenerateUUID(w http.ResponseWriter, r *http.Request) {
	id, err := parseID64(r)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	updated, err := s.users.RegenerateCredentials(id)
	if errors.Is(err, users.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.logger.Error("admin: regenerate credentials failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := s.enqueueUpdateForAllAssignments(id, updated); err != nil {
		s.logger.Error("admin: enqueue update sync jobs failed", "id", id, "err", err)
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/users/%d", id), http.StatusFound)
}

func (s *Server) handleUserSetInbounds(w http.ResponseWriter, r *http.Request) {
	id, err := parseID64(r)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	u, err := s.users.Get(id)
	if errors.Is(err, users.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.logger.Error("admin: get user failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	live, err := s.inbounds.ListInbounds(r.Context())
	if err != nil {
		s.logger.Error("admin: list live inbounds failed", "err", err)
		http.Error(w, "panel unreachable, try again", http.StatusBadGateway)
		return
	}
	byID := make(map[int]xui.Inbound, len(live))
	for _, ib := range live {
		byID[ib.ID] = ib
	}

	var desired []users.Desired
	for _, raw := range r.Form["inbound_id"] {
		n, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		ib, ok := byID[n]
		if !ok {
			continue
		}
		desired = append(desired, users.Desired{InboundID: n, InboundTag: ib.Remark, Protocol: ib.Protocol})
	}

	added, removed, err := s.users.SetInbounds(id, desired)
	if err != nil {
		s.logger.Error("admin: set inbounds failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	payload := payloadFor(u)
	for _, a := range added {
		if _, err := s.syncJobs.Enqueue(id, a.InboundID, sync.OpAssign, payload); err != nil {
			s.logger.Error("admin: enqueue assign failed", "id", id, "inbound_id", a.InboundID, "err", err)
		}
	}
	for _, rmv := range removed {
		if _, err := s.syncJobs.Enqueue(id, rmv.InboundID, sync.OpUnassign, payload); err != nil {
			s.logger.Error("admin: enqueue unassign failed", "id", id, "inbound_id", rmv.InboundID, "err", err)
		}
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/users/%d", id), http.StatusFound)
}

func (s *Server) bulkApply(id int64, action string) error {
	u, err := s.users.Get(id)
	if err != nil {
		return err
	}

	switch action {
	case "enable":
		if err := s.users.SetEnabled(id, true); err != nil {
			return err
		}
		u.Enabled = true
		return s.enqueueUpdateForAllAssignments(id, u)
	case "disable":
		if err := s.users.SetEnabled(id, false); err != nil {
			return err
		}
		u.Enabled = false
		return s.enqueueUpdateForAllAssignments(id, u)
	case "reset_traffic":
		assignments, err := s.users.Inbounds(id)
		if err != nil {
			return err
		}
		payload := payloadFor(u)
		for _, a := range assignments {
			if _, err := s.syncJobs.Enqueue(id, a.InboundID, sync.OpResetTraffic, payload); err != nil {
				return err
			}
		}
		return nil
	case "delete":
		assignments, err := s.users.Inbounds(id)
		if err != nil {
			return err
		}
		if len(assignments) > 0 {
			if _, err := s.syncJobs.Enqueue(id, 0, sync.OpDelete, payloadFor(u)); err != nil {
				return err
			}
		}
		return s.users.Delete(id)
	default:
		return fmt.Errorf("unknown bulk action %q", action)
	}
}

func (s *Server) handleUsersBulk(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	action := r.FormValue("action")

	for _, raw := range r.Form["ids"] {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		if err := s.bulkApply(id, action); err != nil {
			s.logger.Error("admin: bulk action failed", "id", id, "action", action, "err", err)
		}
	}

	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

type syncLogRow struct {
	ID        int64
	UserID    int64
	Email     string
	InboundID int
	Op        string
	Status    string
	Attempts  int
	LastError string
	UpdatedAt string
}

type syncLogPageData struct {
	Jobs []syncLogRow
}

func (s *Server) handleSyncLog(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	jobs, err := s.syncJobs.ListRecent(200)
	if err != nil {
		s.logger.Error("admin: list recent sync jobs failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rows := make([]syncLogRow, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, syncLogRow{
			ID: j.ID, UserID: j.UserID, Email: j.Payload.Email, InboundID: j.InboundID,
			Op: j.Op, Status: j.Status, Attempts: j.Attempts, LastError: j.LastError,
			UpdatedAt: time.Unix(0, j.UpdatedAt).Format("2006-01-02 15:04:05"),
		})
	}

	_ = render(w, "page-sync-log", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: syncLogPageData{Jobs: rows},
	})
}

func (s *Server) handleSyncRetry(w http.ResponseWriter, r *http.Request) {
	id, err := parseID64(r)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := s.syncJobs.Retry(id); err != nil && !errors.Is(err, sync.ErrNotFound) {
		s.logger.Error("admin: retry sync job failed", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/sync", http.StatusFound)
}
