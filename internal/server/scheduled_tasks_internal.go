package server

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

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
