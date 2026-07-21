package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/assignment"
)

type assignmentsListPageData struct {
	Assignments []assignment.Assignment
	Error       string
}

func (s *Server) handleAssignmentsList(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r)

	list, err := s.assignments.List()
	if err != nil {
		s.logger.Error("admin: list assignments failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = render(w, "page-assignments-list", PageData{
		Username: sess.Username, CSRFToken: sess.CSRFToken,
		Data: assignmentsListPageData{Assignments: list},
	})
}

func (s *Server) handleAssignmentSave(w http.ResponseWriter, r *http.Request) {
	subID := r.FormValue("sub_id")
	profile := r.FormValue("profile")

	if subID == "" || profile == "" {
		sess, _ := sessionFromContext(r)
		list, _ := s.assignments.List()
		w.WriteHeader(http.StatusBadRequest)
		_ = render(w, "page-assignments-list", PageData{
			Username: sess.Username, CSRFToken: sess.CSRFToken,
			Data: assignmentsListPageData{Assignments: list, Error: "sub_id and profile are both required"},
		})
		return
	}

	if err := s.assignments.Set(subID, profile); err != nil {
		s.logger.Error("admin: set assignment failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/assignments", http.StatusFound)
}

func (s *Server) handleAssignmentDelete(w http.ResponseWriter, r *http.Request) {
	subID := chi.URLParam(r, "subID")

	if err := s.assignments.Delete(subID); err != nil {
		s.logger.Error("admin: delete assignment failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/assignments", http.StatusFound)
}
