package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/apps"
)

type applicationsListPageData struct {
	Apps  []apps.App
	Error string
}

func (s *Server) handleApplicationsList(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	all, err := s.apps.ListAll()
	if err != nil {
		s.logger.Error("admin: list applications failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-applications-list", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: applicationsListPageData{Apps: all},
	})
}

type applicationFormPageData struct {
	App          apps.App
	PlatformsCSV string
	IsNew        bool
	Error        string
}

func (s *Server) handleApplicationForm(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	idStr := chi.URLParam(r, "id")
	if idStr == "" {
		_ = render(w, "page-application-form", PageData{
			Username: sess.Username, CSRFToken: sess.CSRFToken,
			Data: applicationFormPageData{IsNew: true, App: apps.App{Visible: true}},
		})
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	app, err := s.apps.Get(id)
	if errors.Is(err, apps.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.logger.Error("admin: get application failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-application-form", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: applicationFormPageData{App: app, PlatformsCSV: strings.Join(app.Platforms, ", ")},
	})
}

func appFromForm(r *http.Request) apps.App {
	sortOrder, _ := strconv.Atoi(r.FormValue("sort_order"))

	var platforms []string
	for _, p := range strings.Split(r.FormValue("platforms"), ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			platforms = append(platforms, p)
		}
	}

	return apps.App{
		Name:         r.FormValue("name"),
		Icon:         r.FormValue("icon"),
		Description:  r.FormValue("description"),
		Platforms:    platforms,
		Download:     r.FormValue("download"),
		Deeplink:     r.FormValue("deeplink"),
		Instructions: r.FormValue("instructions"),
		Visible:      r.FormValue("visible") == "1",
		SortOrder:    sortOrder,
	}
}

func (s *Server) handleApplicationCreate(w http.ResponseWriter, r *http.Request) {
	if _, err := s.apps.Create(appFromForm(r)); err != nil {
		s.logger.Error("admin: create application failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/applications", http.StatusFound)
}

func (s *Server) handleApplicationUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := s.apps.Update(id, appFromForm(r)); err != nil {
		s.logger.Error("admin: update application failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/applications", http.StatusFound)
}

func (s *Server) handleApplicationDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := s.apps.Delete(id); err != nil && !errors.Is(err, apps.ErrNotFound) {
		s.logger.Error("admin: delete application failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/applications", http.StatusFound)
}
