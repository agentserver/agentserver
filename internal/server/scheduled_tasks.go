package server

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
)

type scheduledTaskRequest struct {
	Prompt       string  `json:"prompt"`
	ProcessAfter string  `json:"processAfter"`
	Recurrence   *string `json:"recurrence,omitempty"`
	Script       *string `json:"script,omitempty"`
	Timezone     string  `json:"timezone,omitempty"`
}

type scheduledTaskResponse struct {
	TaskID     string  `json:"taskId"`
	SeriesID   string  `json:"seriesId"`
	RunsAt     string  `json:"runsAt"`
	Recurrence *string `json:"recurrence,omitempty"`
	Status     string  `json:"status"`
	Timezone   string  `json:"timezone"`
}

func (s *Server) handleCreateScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}

	var req scheduledTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" || req.ProcessAfter == "" {
		http.Error(w, "prompt and processAfter required", http.StatusBadRequest)
		return
	}
	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	when, err := db.ParseZonedToUTC(req.ProcessAfter, tz)
	if err != nil {
		http.Error(w, "invalid processAfter: "+err.Error(), http.StatusBadRequest)
		return
	}

	id := "sch_" + uuid.New().String()
	task := &db.ScheduledTask{
		ID:             id,
		WorkspaceID:    wid,
		SeriesID:       id,
		CreatorKind:    "rest",
		Prompt:         req.Prompt,
		Script:         req.Script,
		Timezone:       tz,
		Recurrence:     req.Recurrence,
		ProcessAfter:   when,
		Status:         "pending",
		TimeoutSeconds: 600,
	}
	if uid := auth.UserIDFromContext(r.Context()); uid != "" {
		task.CreatedBy.String, task.CreatedBy.Valid = uid, true
	}
	if err := s.DB.CreateScheduledTask(task); err != nil {
		log.Printf("create scheduled task: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, scheduledTaskResponse{
		TaskID:     id,
		SeriesID:   id,
		RunsAt:     when.UTC().Format(time.RFC3339),
		Recurrence: req.Recurrence,
		Status:     "pending",
		Timezone:   tz,
	})
}

func (s *Server) handleListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	rows, err := s.DB.ListScheduledTasksByWorkspace(wid, r.URL.Query().Get("status"))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]scheduledTaskResponse, 0, len(rows))
	for _, t := range rows {
		out = append(out, scheduledTaskResponse{
			TaskID:     t.SeriesID,
			SeriesID:   t.SeriesID,
			RunsAt:     t.ProcessAfter.UTC().Format(time.RFC3339),
			Recurrence: t.Recurrence,
			Status:     t.Status,
			Timezone:   t.Timezone,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	t, err := s.DB.GetScheduledTaskBySeries(wid, sid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if t == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	runs, _ := s.DB.ListScheduledTaskRuns(t.ID, 20)
	writeJSON(w, http.StatusOK, map[string]any{
		"task": scheduledTaskResponse{
			TaskID:     t.SeriesID,
			SeriesID:   t.SeriesID,
			RunsAt:     t.ProcessAfter.UTC().Format(time.RFC3339),
			Recurrence: t.Recurrence,
			Status:     t.Status,
			Timezone:   t.Timezone,
		},
		"runs": runs,
	})
}

func (s *Server) handleCancelScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	n, err := s.DB.CancelScheduledSeries(wid, sid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": n})
}

func (s *Server) handlePauseScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	n, _ := s.DB.PauseScheduledSeries(wid, sid)
	writeJSON(w, http.StatusOK, map[string]any{"paused": n})
}

func (s *Server) handleResumeScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	n, _ := s.DB.ResumeScheduledSeries(wid, sid)
	writeJSON(w, http.StatusOK, map[string]any{"resumed": n})
}

func (s *Server) handleUpdateScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	var req scheduledTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	upd := db.ScheduledTaskUpdate{}
	if req.Prompt != "" {
		p := req.Prompt
		upd.Prompt = &p
	}
	if req.Recurrence != nil {
		upd.Recurrence = req.Recurrence
	}
	if req.Script != nil {
		upd.Script = req.Script
	}
	if req.ProcessAfter != "" {
		t, err := db.ParseZonedToUTC(req.ProcessAfter, scheduledTaskDefaultStr(req.Timezone, "UTC"))
		if err != nil {
			http.Error(w, "invalid processAfter", http.StatusBadRequest)
			return
		}
		upd.ProcessAfter = &t
	}
	n, err := s.DB.UpdateScheduledSeries(wid, sid, upd)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

func (s *Server) handleGetScheduledTaskRuns(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	sid := chi.URLParam(r, "seriesId")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	t, _ := s.DB.GetScheduledTaskBySeries(wid, sid)
	if t == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	runs, _ := s.DB.ListScheduledTaskRuns(t.ID, 50)
	writeJSON(w, http.StatusOK, runs)
}

func scheduledTaskDefaultStr(s, dflt string) string {
	if s == "" {
		return dflt
	}
	return s
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
