package coreserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const maxRunAttemptEventCommandBytes = 18 * 1024 * 1024

type RunAttemptCommands interface {
	ClaimRunAttempt(context.Context, corecontract.ClaimRunAttemptRequest) (corecontract.ClaimRunAttemptResponse, error)
	RenewRunAttempt(context.Context, corecontract.RenewRunAttemptRequest) (corecontract.RenewRunAttemptResponse, error)
	InterruptRunAttempt(context.Context, corecontract.InterruptRunAttemptRequest) (corecontract.InterruptRunAttemptResponse, error)
	AbandonRunAttempt(context.Context, corecontract.AbandonRunAttemptRequest) (corecontract.AbandonRunAttemptResponse, error)
	MarkTurnAccepted(context.Context, corecontract.MarkTurnAcceptedRequest) (corecontract.MarkTurnAcceptedResponse, error)
	BeginRunFinalization(context.Context, corecontract.BeginRunFinalizationRequest) (corecontract.BeginRunFinalizationResponse, error)
	CommitCheckpoint(context.Context, corecontract.CommitCheckpointRequest) (corecontract.CommitCheckpointResponse, error)
	AppendAttemptEvents(context.Context, corecontract.AppendAttemptEventsRequest) (corecontract.AppendAttemptEventsResponse, error)
}

type RunAttemptHandler struct {
	authorizer WorkloadAuthorizer
	commands   RunAttemptCommands
}

func NewRunAttemptHandler(authorizer WorkloadAuthorizer, commands RunAttemptCommands) (*RunAttemptHandler, error) {
	if authorizer == nil {
		return nil, errors.New("workload authorizer is required")
	}
	if commands == nil {
		return nil, errors.New("run attempt commands are required")
	}
	return &RunAttemptHandler{authorizer: authorizer, commands: commands}, nil
}

func (handler *RunAttemptHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	if request.URL.Path == corecontract.ClaimRunAttemptPath {
		handler.claim(response, request)
		return
	}
	attemptID, action, ok := parseRunAttemptAction(request.URL.Path)
	if !ok {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	switch action {
	case "renew":
		handler.renew(response, request, attemptID)
	case "interrupt":
		handler.interrupt(response, request, attemptID)
	case "abandon":
		handler.abandon(response, request, attemptID)
	case "turn-accepted":
		handler.turnAccepted(response, request, attemptID)
	case "begin-finalization":
		handler.beginFinalization(response, request, attemptID)
	case "commit-checkpoint":
		handler.commitCheckpoint(response, request, attemptID)
	case "append-events":
		handler.appendEvents(response, request, attemptID)
	default:
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
	}
}

func (handler *RunAttemptHandler) abandon(response http.ResponseWriter, request *http.Request, attemptID string) {
	if !handler.authorize(response, request, "run-attempts.abandon") {
		return
	}
	var command corecontract.AbandonRunAttemptRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	if command.RunAttemptID != attemptID {
		writePathIdentityError(response, "runAttemptId")
		return
	}
	result, err := handler.commands.AbandonRunAttempt(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunAttemptHandler) interrupt(response http.ResponseWriter, request *http.Request, attemptID string) {
	if !handler.authorize(response, request, "run-attempts.interrupt") {
		return
	}
	var command corecontract.InterruptRunAttemptRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	if command.RunAttemptID != attemptID {
		writePathIdentityError(response, "runAttemptId")
		return
	}
	result, err := handler.commands.InterruptRunAttempt(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunAttemptHandler) claim(response http.ResponseWriter, request *http.Request) {
	if !handler.authorize(response, request, "run-attempts.claim") {
		return
	}
	var command corecontract.ClaimRunAttemptRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	result, err := handler.commands.ClaimRunAttempt(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunAttemptHandler) renew(response http.ResponseWriter, request *http.Request, attemptID string) {
	if !handler.authorize(response, request, "run-attempts.renew") {
		return
	}
	var command corecontract.RenewRunAttemptRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	if command.RunAttemptID != attemptID {
		writePathIdentityError(response, "runAttemptId")
		return
	}
	result, err := handler.commands.RenewRunAttempt(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunAttemptHandler) turnAccepted(response http.ResponseWriter, request *http.Request, attemptID string) {
	if !handler.authorize(response, request, "run-attempts.turn-accepted") {
		return
	}
	var command corecontract.MarkTurnAcceptedRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	if command.RunAttemptID != attemptID {
		writePathIdentityError(response, "runAttemptId")
		return
	}
	result, err := handler.commands.MarkTurnAccepted(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunAttemptHandler) appendEvents(response http.ResponseWriter, request *http.Request, attemptID string) {
	if !handler.authorize(response, request, "run-attempts.events.append") {
		return
	}
	var command corecontract.AppendAttemptEventsRequest
	if !decodeCommandWithLimit(response, request, &command, maxRunAttemptEventCommandBytes) {
		return
	}
	if command.RunAttemptID != attemptID {
		writePathIdentityError(response, "runAttemptId")
		return
	}
	result, err := handler.commands.AppendAttemptEvents(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunAttemptHandler) beginFinalization(response http.ResponseWriter, request *http.Request, attemptID string) {
	if !handler.authorize(response, request, "run-attempts.begin-finalization") {
		return
	}
	var command corecontract.BeginRunFinalizationRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	if command.RunAttemptID != attemptID {
		writePathIdentityError(response, "runAttemptId")
		return
	}
	result, err := handler.commands.BeginRunFinalization(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunAttemptHandler) commitCheckpoint(response http.ResponseWriter, request *http.Request, attemptID string) {
	if !handler.authorize(response, request, "run-attempts.commit-checkpoint") {
		return
	}
	var command corecontract.CommitCheckpointRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	if command.RunAttemptID != attemptID {
		writePathIdentityError(response, "runAttemptId")
		return
	}
	result, err := handler.commands.CommitCheckpoint(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *RunAttemptHandler) authorize(response http.ResponseWriter, request *http.Request, action string) bool {
	if err := handler.authorizer.AuthorizeWorkload(request, action); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for this command"})
		return false
	}
	return true
}

func parseRunAttemptAction(path string) (attemptID, action string, ok bool) {
	if !strings.HasPrefix(path, corecontract.RunAttemptPathPrefix) {
		return "", "", false
	}
	remainder := strings.TrimPrefix(path, corecontract.RunAttemptPathPrefix)
	if remainder == "" {
		return "", "", false
	}
	if !strings.Contains(remainder, "/") {
		const renewSuffix = ":renew"
		if strings.HasSuffix(remainder, renewSuffix) && len(remainder) > len(renewSuffix) {
			return strings.TrimSuffix(remainder, renewSuffix), "renew", true
		}
		const interruptSuffix = ":interrupt"
		if strings.HasSuffix(remainder, interruptSuffix) && len(remainder) > len(interruptSuffix) {
			return strings.TrimSuffix(remainder, interruptSuffix), "interrupt", true
		}
		const abandonSuffix = ":abandon"
		if strings.HasSuffix(remainder, abandonSuffix) && len(remainder) > len(abandonSuffix) {
			return strings.TrimSuffix(remainder, abandonSuffix), "abandon", true
		}
		const turnAcceptedSuffix = ":turnAccepted"
		if strings.HasSuffix(remainder, turnAcceptedSuffix) && len(remainder) > len(turnAcceptedSuffix) {
			return strings.TrimSuffix(remainder, turnAcceptedSuffix), "turn-accepted", true
		}
		const beginFinalizationSuffix = ":beginFinalization"
		if strings.HasSuffix(remainder, beginFinalizationSuffix) && len(remainder) > len(beginFinalizationSuffix) {
			return strings.TrimSuffix(remainder, beginFinalizationSuffix), "begin-finalization", true
		}
		const commitCheckpointSuffix = ":commitCheckpoint"
		if strings.HasSuffix(remainder, commitCheckpointSuffix) && len(remainder) > len(commitCheckpointSuffix) {
			return strings.TrimSuffix(remainder, commitCheckpointSuffix), "commit-checkpoint", true
		}
		return "", "", false
	}
	parts := strings.Split(remainder, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "events:append" {
		return parts[0], "append-events", true
	}
	return "", "", false
}
