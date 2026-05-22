package server

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/agentserver/agentserver/internal/db"
)

type leaseRequest struct {
	Limit        int    `json:"limit"`
	LeaseSeconds int    `json:"leaseSeconds"`
	Owner        string `json:"owner"`
}

type leaseResponseItem struct {
	ID             string  `json:"id"`
	WorkspaceID    string  `json:"workspaceId"`
	SeriesID       string  `json:"seriesId"`
	RunID          string  `json:"runId"`
	Prompt         string  `json:"prompt"`
	Script         *string `json:"script,omitempty"`
	Timezone       string  `json:"timezone"`
	Recurrence     *string `json:"recurrence,omitempty"`
	ProcessAfter   string  `json:"processAfter"`
	TimeoutSeconds int     `json:"timeoutSeconds"`
}

func (s *Server) handleInternalLeaseScheduledTasks(w http.ResponseWriter, r *http.Request) {
	var req leaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Limit <= 0 {
		req.Limit = 10
	}
	if req.LeaseSeconds <= 0 {
		req.LeaseSeconds = 600
	}

	leased, err := s.DB.LeaseDueScheduledTasks(req.Limit, req.LeaseSeconds, req.Owner)
	if err != nil {
		log.Printf("lease scheduled tasks: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Partial-batch failure semantics: LeaseDueScheduledTasks has already
	// committed; if CreateScheduledTaskRun or the last_run_id stamp fails
	// mid-batch we return 500 and leave behind orphaned run rows + tasks
	// stuck in 'running' for the rest of the batch. The dispatcher recovers
	// when lease_until expires (leaseSeconds from now), at which point the
	// next tick re-claims them.
	out := make([]leaseResponseItem, 0, len(leased))
	for _, t := range leased {
		runID := "run_" + uuid.New().String()
		if err := s.DB.CreateScheduledTaskRun(&db.ScheduledTaskRun{
			ID:        runID,
			TaskID:    t.ID,
			SeriesID:  t.SeriesID,
			StartedAt: time.Now(),
		}); err != nil {
			log.Printf("create scheduled task run for %s: %v", t.ID, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if _, err := s.DB.Exec(`UPDATE scheduled_tasks SET last_run_id = $2 WHERE id = $1`, t.ID, runID); err != nil {
			log.Printf("stamp last_run_id %s: %v", t.ID, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		out = append(out, leaseResponseItem{
			ID:             t.ID,
			WorkspaceID:    t.WorkspaceID,
			SeriesID:       t.SeriesID,
			RunID:          runID,
			Prompt:         t.Prompt,
			Script:         t.Script,
			Timezone:       t.Timezone,
			Recurrence:     t.Recurrence,
			ProcessAfter:   t.ProcessAfter.UTC().Format(time.RFC3339),
			TimeoutSeconds: t.TimeoutSeconds,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type resultRequest struct {
	TaskID          string          `json:"taskId"`
	RunID           string          `json:"runId"`
	Status          string          `json:"status"` // succeeded|failed|timeout|skipped
	ExitCode        int             `json:"exitCode"`
	DurationMS      int64           `json:"durationMs"`
	Summary         string          `json:"summary"`
	TranscriptURI   string          `json:"transcriptUri"`
	CostUSD         *float64        `json:"costUsd,omitempty"`
	NumTurns        *int            `json:"numTurns,omitempty"`
	BroadcastTo     []string        `json:"broadcastTo"`
	BroadcastErrors json.RawMessage `json:"broadcastErrors"`
}

func (s *Server) handleInternalScheduledTaskResult(w http.ResponseWriter, r *http.Request) {
	var req resultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	task, err := s.DB.GetScheduledTaskByID(req.TaskID)
	if err != nil {
		log.Printf("get scheduled task %s: %v", req.TaskID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var nextAfter *time.Time
	var newID string
	if task.Recurrence != nil && *task.Recurrence != "" {
		loc, err := time.LoadLocation(task.Timezone)
		if err != nil {
			loc = time.UTC
		}
		sched, err := cron.ParseStandard(*task.Recurrence)
		if err == nil {
			n := sched.Next(time.Now().In(loc)).UTC()
			nextAfter = &n
			newID = "sch_" + uuid.New().String()
		}
		// else: leave nextAfter nil → FinalizeRunAndAdvance treats as one-shot complete
	}

	in := db.FinalizeRunInput{
		RunID:           req.RunID,
		TaskID:          req.TaskID,
		Status:          req.Status,
		ExitCode:        req.ExitCode,
		DurationMS:      req.DurationMS,
		Summary:         req.Summary,
		TranscriptURI:   req.TranscriptURI,
		CostUSD:         req.CostUSD,
		NumTurns:        req.NumTurns,
		BroadcastTo:     req.BroadcastTo,
		BroadcastErrors: req.BroadcastErrors,
	}
	if err := s.DB.FinalizeRunAndAdvance(in, nextAfter, newID); err != nil {
		log.Printf("finalize scheduled task run %s: %v", req.RunID, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nextRunId": newID})
}

// --------------------------------------------------------------------------
// Internal workspace-scoped endpoints — no member check; auth is
// X-Internal-Secret only. Called from codex-app-gateway's loopback proxy.
// --------------------------------------------------------------------------

// handleInternalCreateScheduledTask is POST /api/internal/workspaces/{wid}/scheduled-tasks.
func (s *Server) handleInternalCreateScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
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
		CreatorKind:    "mcp",
		Prompt:         req.Prompt,
		Script:         req.Script,
		Timezone:       tz,
		Recurrence:     req.Recurrence,
		ProcessAfter:   when,
		Status:         "pending",
		TimeoutSeconds: 600,
	}
	if err := s.DB.CreateScheduledTask(task); err != nil {
		log.Printf("internal create scheduled task: %v", err)
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

// handleInternalListScheduledTasks is GET /api/internal/workspaces/{wid}/scheduled-tasks.
func (s *Server) handleInternalListScheduledTasks(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
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

// handleInternalCancelScheduledTask is POST /api/internal/workspaces/{wid}/scheduled-tasks/{seriesId}/cancel.
func (s *Server) handleInternalCancelScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	sid := chi.URLParam(r, "seriesId")
	n, err := s.DB.CancelScheduledSeries(wid, sid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": n})
}

// handleInternalPauseScheduledTask is POST /api/internal/workspaces/{wid}/scheduled-tasks/{seriesId}/pause.
func (s *Server) handleInternalPauseScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	sid := chi.URLParam(r, "seriesId")
	n, err := s.DB.PauseScheduledSeries(wid, sid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paused": n})
}

// handleInternalResumeScheduledTask is POST /api/internal/workspaces/{wid}/scheduled-tasks/{seriesId}/resume.
func (s *Server) handleInternalResumeScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	sid := chi.URLParam(r, "seriesId")
	n, err := s.DB.ResumeScheduledSeries(wid, sid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resumed": n})
}

// handleInternalUpdateScheduledTask is PATCH /api/internal/workspaces/{wid}/scheduled-tasks/{seriesId}.
func (s *Server) handleInternalUpdateScheduledTask(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "wid")
	sid := chi.URLParam(r, "seriesId")
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
		tz := req.Timezone
		if tz == "" {
			tz = "UTC"
		}
		t, err := db.ParseZonedToUTC(req.ProcessAfter, tz)
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
