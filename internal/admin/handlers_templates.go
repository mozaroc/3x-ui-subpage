package admin

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/templatestore"
)

// emptyProtocolPlaceholder stands in for protocol="" in URL path segments,
// which can't otherwise represent an empty path element.
const emptyProtocolPlaceholder = "-"

func encodeProtocol(protocol string) string {
	if protocol == "" {
		return emptyProtocolPlaceholder
	}
	return protocol
}

func decodeProtocol(protocol string) string {
	if protocol == emptyProtocolPlaceholder {
		return ""
	}
	return protocol
}

type templatesListPageData struct {
	Rows  []templatestore.Row
	Error string
}

func (s *Server) handleTemplatesList(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	rows, err := s.templates.List()
	if err != nil {
		s.logger.Error("admin: list templates failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-templates-list", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: templatesListPageData{Rows: rows},
	})
}

func (s *Server) handleTemplateLookup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	format, profile, protocol := q.Get("format"), q.Get("profile"), q.Get("protocol")
	if format == "" || profile == "" {
		http.Redirect(w, r, "/admin/templates", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin/templates/"+url.PathEscape(format)+"/"+url.PathEscape(profile)+"/"+url.PathEscape(encodeProtocol(protocol)), http.StatusFound)
}

type templateFormPageData struct {
	Row   templatestore.Row
	IsNew bool
	Error string
}

func (s *Server) templateKeyFromRoute(r *http.Request) (format, profile, protocol string) {
	return chi.URLParam(r, "format"), chi.URLParam(r, "profile"), decodeProtocol(chi.URLParam(r, "protocol"))
}

func (s *Server) handleTemplateEdit(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)
	format, profile, protocol := s.templateKeyFromRoute(r)

	row, err := s.templates.Get(format, profile, protocol)
	isNew := errors.Is(err, templatestore.ErrNotFound)
	if isNew {
		row = templatestore.Row{Format: format, Profile: profile, Protocol: protocol}
	} else if err != nil {
		s.logger.Error("admin: get template failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-template-form", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: templateFormPageData{Row: row, IsNew: isNew},
	})
}

func (s *Server) handleTemplateSave(w http.ResponseWriter, r *http.Request) {
	format, profile, protocol := s.templateKeyFromRoute(r)

	if err := s.templates.Put(format, profile, protocol, r.FormValue("content")); err != nil {
		s.logger.Error("admin: put template failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/templates/"+url.PathEscape(format)+"/"+url.PathEscape(profile)+"/"+url.PathEscape(encodeProtocol(protocol)), http.StatusFound)
}

func (s *Server) handleTemplateDelete(w http.ResponseWriter, r *http.Request) {
	format, profile, protocol := s.templateKeyFromRoute(r)

	if err := s.templates.Delete(format, profile, protocol); err != nil {
		s.logger.Error("admin: delete template failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/templates", http.StatusFound)
}
