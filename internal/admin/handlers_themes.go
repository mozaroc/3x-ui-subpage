package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/theme"
)

type themesListPageData struct {
	Slugs []string
	Error string
}

func (s *Server) handleThemesList(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	slugs, err := s.themes.ListSlugs()
	if err != nil {
		s.logger.Error("admin: list theme slugs failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-themes-list", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: themesListPageData{Slugs: slugs},
	})
}

func (s *Server) handleThemeLookup(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	if slug == "" {
		http.Redirect(w, r, "/admin/themes", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin/themes/"+url.PathEscape(slug), http.StatusFound)
}

type themeEditPageData struct {
	Slug       string
	Meta       theme.Meta
	ColorsJSON string
	FontsJSON  string
	Files      []string
	IsNew      bool
	Error      string
}

func (s *Server) handleThemeEdit(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)
	slug := chi.URLParam(r, "slug")

	meta, err := s.themes.GetMeta(slug)
	isNew := errors.Is(err, theme.ErrNotFound)
	if err != nil && !isNew {
		s.logger.Error("admin: get theme meta failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var files []string
	if !isNew {
		files, err = s.themes.ListFiles(slug)
		if err != nil {
			s.logger.Error("admin: list theme files failed", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	colorsJSON, _ := json.MarshalIndent(meta.Colors, "", "  ")
	fontsJSON, _ := json.MarshalIndent(meta.Fonts, "", "  ")
	if meta.Colors == nil {
		colorsJSON = []byte("{}")
	}
	if meta.Fonts == nil {
		fontsJSON = []byte("{}")
	}

	_ = render(w, "page-theme-edit", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: themeEditPageData{
			Slug: slug, Meta: meta, ColorsJSON: string(colorsJSON), FontsJSON: string(fontsJSON),
			Files: files, IsNew: isNew,
		},
	})
}

func (s *Server) handleThemeMetaSave(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	var colors, fonts map[string]string
	if err := json.Unmarshal([]byte(r.FormValue("colors")), &colors); err != nil {
		s.renderThemeEditError(w, r, slug, "colors: invalid JSON: "+err.Error())
		return
	}
	if err := json.Unmarshal([]byte(r.FormValue("fonts")), &fonts); err != nil {
		s.renderThemeEditError(w, r, slug, "fonts: invalid JSON: "+err.Error())
		return
	}

	meta := theme.Meta{
		Name:        r.FormValue("name"),
		Description: r.FormValue("description"),
		Logo:        r.FormValue("logo"),
		Favicon:     r.FormValue("favicon"),
		Colors:      colors,
		Fonts:       fonts,
	}

	if err := s.themes.UpsertMeta(slug, meta); err != nil {
		s.logger.Error("admin: upsert theme meta failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/themes/"+url.PathEscape(slug), http.StatusFound)
}

func (s *Server) renderThemeEditError(w http.ResponseWriter, r *http.Request, slug, errMsg string) {
	sess, _ := sessionFromContext(r)
	meta, err := s.themes.GetMeta(slug)
	isNew := errors.Is(err, theme.ErrNotFound)

	var files []string
	if !isNew {
		files, _ = s.themes.ListFiles(slug)
	}

	w.WriteHeader(http.StatusBadRequest)
	_ = render(w, "page-theme-edit", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: themeEditPageData{
			Slug: slug, Meta: meta, ColorsJSON: r.FormValue("colors"), FontsJSON: r.FormValue("fonts"),
			Files: files, IsNew: isNew, Error: errMsg,
		},
	})
}

func (s *Server) handleThemeFileLookup(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Redirect(w, r, "/admin/themes/"+url.PathEscape(slug), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin/themes/"+url.PathEscape(slug)+"/files/"+path, http.StatusFound)
}

type themeFileEditPageData struct {
	Slug    string
	Path    string
	Content string
	IsNew   bool
	Error   string
}

func (s *Server) handleThemeFileEdit(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)
	slug := chi.URLParam(r, "slug")
	path := chi.URLParam(r, "*")

	content, err := s.themes.GetFile(slug, path)
	isNew := errors.Is(err, theme.ErrNotFound)
	if err != nil && !isNew {
		s.logger.Error("admin: get theme file failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-theme-file-edit", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: themeFileEditPageData{Slug: slug, Path: path, Content: string(content), IsNew: isNew},
	})
}

func (s *Server) handleThemeFileSave(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	path := chi.URLParam(r, "*")

	if err := s.themes.PutFile(slug, path, []byte(r.FormValue("content"))); err != nil {
		s.logger.Error("admin: put theme file failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/themes/"+url.PathEscape(slug), http.StatusFound)
}

func (s *Server) handleThemeFileDelete(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	path := chi.URLParam(r, "*")

	if err := s.themes.DeleteFile(slug, path); err != nil && !errors.Is(err, theme.ErrNotFound) {
		s.logger.Error("admin: delete theme file failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/themes/"+url.PathEscape(slug), http.StatusFound)
}
