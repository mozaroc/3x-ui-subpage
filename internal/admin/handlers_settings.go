package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/config"
)

type settingsRow struct {
	Key   string
	Value string
}

type settingsPageData struct {
	Rows  []settingsRow
	Error string
}

func (s *Server) handleSettingsList(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	stored, err := config.ListSettings(s.db)
	if err != nil {
		s.logger.Error("admin: list settings failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rows := make([]settingsRow, 0, len(config.KnownSettingsKeys()))
	for _, key := range config.KnownSettingsKeys() {
		value := stored[key]
		if value == "" {
			value = "{}"
		}
		rows = append(rows, settingsRow{Key: key, Value: value})
	}

	_ = render(w, "page-settings", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: settingsPageData{Rows: rows},
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

	stored, _ := config.ListSettings(s.db)
	rows := make([]settingsRow, 0, len(config.KnownSettingsKeys()))
	for _, key := range config.KnownSettingsKeys() {
		value := stored[key]
		if value == "" {
			value = "{}"
		}
		rows = append(rows, settingsRow{Key: key, Value: value})
	}

	w.WriteHeader(http.StatusBadRequest)
	_ = render(w, "page-settings", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: settingsPageData{Rows: rows, Error: errMsg},
	})
}
