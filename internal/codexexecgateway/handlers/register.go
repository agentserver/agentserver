// Package handlers contains HTTP handler functions for the codex-exec gateway.
// It must not import the parent codexexecgateway package to avoid import cycles;
// shared DTOs are imported from execmodel instead.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/execmodel"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Store is the subset of storage required by the register handler.
type Store interface {
	CreateExecutor(ctx context.Context, e execmodel.Executor) error
	DeleteExecutor(ctx context.Context, exeID string) error
}

// registerRequest is the body of POST /api/codex-exec/register. Per
// v0.54.0, executor-level description and default_cwd are dropped —
// the LLM-visible per-binding name + description live on
// workspace_executors (set by Bind).
type registerRequest struct {
	DisplayName string `json:"display_name"`
}

type registerResponse struct {
	ExeID string `json:"exe_id"`
}

// Register returns an http.HandlerFunc that creates a new executor row
// and returns its id. Auth on /agentx/environment/{env_id}/register goes
// through Agent Identity JWT or ChatGPT access_token validated by
// agentserver, and the inbound ws verifies a short-lived HMAC ticket
// minted at register time. So this endpoint no longer mints any
// per-executor bearer.
func Register(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.Header.Get("X-User-Id")
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing user"})
			return
		}
		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		exe := execmodel.Executor{
			ExeID:        "exe_" + uuid.NewString(),
			UserID:       userID,
			DisplayName:  req.DisplayName,
			RegisteredAt: time.Now().UTC(),
		}
		if err := store.CreateExecutor(r.Context(), exe); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create executor"})
			return
		}
		writeJSON(w, http.StatusCreated, registerResponse{ExeID: exe.ExeID})
	}
}

// DeleteExecutor handles DELETE /api/codex-exec/executors/{exe_id}.
// Idempotent — absent id returns 204 same as present. Surfaces 500
// only on DB error.
func DeleteExecutor(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		exeID := chi.URLParam(r, "exe_id")
		if exeID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exe_id required"})
			return
		}
		if err := store.DeleteExecutor(r.Context(), exeID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
