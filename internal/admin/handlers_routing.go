package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/routing"
)

// routingRuleTypes is the fixed set of rule types offered in the admin
// form's <select>, matching the original spec's routing-rule requirement:
// GEOIP, geosite, domains, wildcard domains, regex, CIDR, IP ranges,
// process names, protocol rules, DNS rules, and custom administrator rules.
var routingRuleTypes = []string{
	"geoip", "geosite", "domain", "domain_suffix", "domain_keyword",
	"regex", "cidr", "ip_range", "process", "protocol", "port", "dns", "custom",
}

type routingListPageData struct {
	Rules []routing.Rule
	Error string
}

func (s *Server) handleRoutingList(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	rules, err := s.routing.List()
	if err != nil {
		s.logger.Error("admin: list routing rules failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-routing-list", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: routingListPageData{Rules: rules},
	})
}

type routingFormPageData struct {
	Rule  routing.Rule
	Types []string
	IsNew bool
	Error string
}

func (s *Server) handleRoutingForm(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	idStr := chi.URLParam(r, "id")
	if idStr == "" {
		_ = render(w, "page-routing-form", PageData{
			Username: sess.Username, CSRFToken: sess.CSRFToken,
			Data: routingFormPageData{IsNew: true, Types: routingRuleTypes, Rule: routing.Rule{Profile: routing.DefaultProfile, Enabled: true}},
		})
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	rule, err := s.routing.Get(id)
	if errors.Is(err, routing.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.logger.Error("admin: get routing rule failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-routing-form", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: routingFormPageData{Rule: rule, Types: routingRuleTypes},
	})
}

func routingRuleFromForm(r *http.Request) routing.Rule {
	sortOrder, _ := strconv.Atoi(r.FormValue("sort_order"))
	return routing.Rule{
		Profile:   r.FormValue("profile"),
		SortOrder: sortOrder,
		Type:      r.FormValue("type"),
		Value:     r.FormValue("value"),
		Outbound:  r.FormValue("outbound"),
		Enabled:   r.FormValue("enabled") == "1",
	}
}

func (s *Server) handleRoutingCreate(w http.ResponseWriter, r *http.Request) {
	if _, err := s.routing.Create(routingRuleFromForm(r)); err != nil {
		s.logger.Error("admin: create routing rule failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/routing", http.StatusFound)
}

func (s *Server) handleRoutingUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := s.routing.Update(id, routingRuleFromForm(r)); err != nil {
		s.logger.Error("admin: update routing rule failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/routing", http.StatusFound)
}

func (s *Server) handleRoutingDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := s.routing.Delete(id); err != nil {
		s.logger.Error("admin: delete routing rule failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/routing", http.StatusFound)
}
