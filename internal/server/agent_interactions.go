package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/agentserver/agentserver/internal/db"
)

// handleListInteractions returns the audit trail for a workspace.
// GET /api/workspaces/{wid}/agent-interactions
//
//	@Summary   List agent interaction audit trail for a workspace
//	@Tags      Misc
//	@Produce   json
//	@Param     wid     path   string  true  "Workspace ID"
//	@Param     limit   query  int     false "Max entries (1–200, default 50)"
//	@Param     offset  query  int     false "Pagination offset"
//	@Success   200  {array}   AgentInteractionItem
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "not a workspace member"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{wid}/agent-interactions [get]
func (s *Server) handleListInteractions(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 {
			offset = n
		}
	}

	items, err := s.DB.ListInteractions(wid, limit, offset)
	if err != nil {
		log.Printf("list interactions: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if items == nil {
		items = []db.AgentInteraction{}
	}

	result := make([]AgentInteractionItem, len(items))
	for i, item := range items {
		result[i] = AgentInteractionItem{
			ID:         item.ID,
			ActorID:    item.ActorID,
			Action:     item.Action,
			TargetID:   item.TargetID,
			TargetType: item.TargetType,
			CreatedAt:  item.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if len(item.DetailJSON) > 0 && string(item.DetailJSON) != "null" {
			raw := json.RawMessage(item.DetailJSON)
			result[i].Detail = &raw
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// logInteraction is a DRY helper for audit logging.
func (s *Server) logInteraction(workspaceID string, actorID *string, action, targetID, targetType string, detail map[string]any) {
	detailJSON, _ := json.Marshal(detail)
	s.DB.LogInteraction(&db.AgentInteraction{
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		Action:      action,
		TargetID:    targetID,
		TargetType:  targetType,
		DetailJSON:  detailJSON,
	})
}
