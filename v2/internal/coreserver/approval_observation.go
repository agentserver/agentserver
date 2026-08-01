package coreserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	maximumApprovalObserveWaitMillis = int64(25_000)
	approvalObservePollInterval      = 100 * time.Millisecond
)

type ApprovalObservationCommands interface {
	ObserveApproval(context.Context, corecontract.ObserveApprovalRequest) (corecontract.ObserveApprovalResponse, error)
}

// ApprovalObservationHandler is the harness-pool-only bounded long poll. It
// deliberately polls through the store command instead of relying on process
// memory: every sample revalidates the live holder/generation and lets
// PostgreSQL time atomically drive expiry.
type ApprovalObservationHandler struct {
	authorizer WorkloadAuthorizer
	commands   ApprovalObservationCommands
	poll       time.Duration
}

func NewApprovalObservationHandler(authorizer WorkloadAuthorizer, commands ApprovalObservationCommands) (*ApprovalObservationHandler, error) {
	if authorizer == nil || commands == nil {
		return nil, errors.New("approval observation authorizer and commands are required")
	}
	return &ApprovalObservationHandler{authorizer: authorizer, commands: commands, poll: approvalObservePollInterval}, nil
}

func (handler *ApprovalObservationHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(corecontract.ApprovalActionRoutePattern, handler)
	return mux
}

func (handler *ApprovalObservationHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	approvalID, ok := strings.CutSuffix(request.PathValue("approvalAction"), ":observe")
	if !ok || approvalID == "" {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "approval observation endpoint not found"})
		return
	}
	if err := handler.authorizer.AuthorizeWorkload(request, "approvals.observe"); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized to observe approvals"})
		return
	}
	if request.URL.RawQuery != "" {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "approval observation does not accept query parameters"})
		return
	}
	var command corecontract.ObserveApprovalRequest
	if !decodeCommandWithLimit(response, request, &command, maxExecutionCommandBytes) || !approvalPathMatches(response, approvalID, command.ApprovalID) {
		return
	}
	if command.WaitMillis < 0 || command.WaitMillis > maximumApprovalObserveWaitMillis {
		writeError(response, http.StatusBadRequest, corecontract.ErrorResponse{Code: "invalid_argument", Message: "waitMs must be between 0 and 25000"})
		return
	}
	result, err := handler.observe(request.Context(), command)
	if err != nil {
		if request.Context().Err() == nil {
			writeCommandError(response, err)
		}
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *ApprovalObservationHandler) observe(ctx context.Context, command corecontract.ObserveApprovalRequest) (corecontract.ObserveApprovalResponse, error) {
	result, err := handler.commands.ObserveApproval(ctx, command)
	if err != nil || result.OutcomeAvailable || command.WaitMillis == 0 {
		return result, err
	}
	wait := time.Duration(command.WaitMillis) * time.Millisecond
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(min(handler.poll, wait))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return corecontract.ObserveApprovalResponse{}, ctx.Err()
		case <-deadline.C:
			return result, nil
		case <-ticker.C:
			result, err = handler.commands.ObserveApproval(ctx, command)
			if err != nil || result.OutcomeAvailable {
				return result, err
			}
		}
	}
}

var _ http.Handler = (*ApprovalObservationHandler)(nil)
