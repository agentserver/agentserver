package codexececdge

import (
	"net/http"

	"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleWSProxy(w http.ResponseWriter, r *http.Request) {
	exeID := chi.URLParam(r, "exe_id")
	token := r.URL.Query().Get("token")
	if exeID == "" || token == "" {
		http.Error(w, "missing parameters", http.StatusUnauthorized)
		return
	}
	if err := wsticket.Verify(token, exeID, s.cfg.AgentserverInternalSecret); err != nil {
		s.logger.Warn("wsproxy: bad ticket", "exe_id", exeID, "err", err, "remote", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Phase B continued in Task 6: dial upstream + accept + pipe.
	http.Error(w, "not implemented yet", http.StatusNotImplemented)
}
