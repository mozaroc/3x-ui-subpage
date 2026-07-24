package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/config"
)

// settingsPageData feeds the settings page: Values holds each known
// section's raw JSON, keyed by settings key ("server", "xui", ...) — the
// page's structured form fields are populated from this JSON client-side
// (see settings.html's inline script), and kept in sync with it as the
// single source of truth submitted back on save. ThemeSlugs backs the
// theme.active dropdown with whatever themes actually exist right now.
type settingsPageData struct {
	Values     map[string]string
	ThemeSlugs []string
	Error      string
}

func (s *Server) loadSettingsPageData() (settingsPageData, error) {
	stored, err := config.ListSettings(s.db)
	if err != nil {
		return settingsPageData{}, err
	}

	values := make(map[string]string, len(config.KnownSettingsKeys()))
	for _, key := range config.KnownSettingsKeys() {
		value := stored[key]
		if value == "" {
			value = "{}"
		}
		values[key] = value
	}

	slugs, err := s.themes.ListSlugs()
	if err != nil {
		return settingsPageData{}, err
	}

	return settingsPageData{Values: values, ThemeSlugs: slugs}, nil
}

func (s *Server) handleSettingsList(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	data, err := s.loadSettingsPageData()
	if err != nil {
		s.logger.Error("admin: load settings page failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-settings", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: data,
	})
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	value := r.FormValue("value")

	if err := config.SaveSetting(s.db, key, []byte(value)); err != nil {
		s.renderSettingsError(w, r, err.Error())
		return
	}

	http.Redirect(w, r, "/admin/settings", http.StatusFound)
}

func (s *Server) renderSettingsError(w http.ResponseWriter, r *http.Request, errMsg string) {
	sess, _ := sessionFromContext(r)

	data, err := s.loadSettingsPageData()
	if err != nil {
		s.logger.Error("admin: load settings page failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data.Error = errMsg

	w.WriteHeader(http.StatusBadRequest)
	_ = render(w, "page-settings", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: data,
	})
}
