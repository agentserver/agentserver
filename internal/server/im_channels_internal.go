package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handleInternalListIMChannels returns the workspace's IM channels in the
// minimal shape the codex-app-gateway scheduler needs to broadcast results
// ({id, userId}). Used by scheduler.Broadcaster via AgentserverClient.ListChannels.
func (s *Server) handleInternalListIMChannels(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	chs, err := s.DB.ListIMChannels(wid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]map[string]string, 0, len(chs))
	for _, ch := range chs {
		out = append(out, map[string]string{
			"id":     ch.ID,
			"userId": ch.UserID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
