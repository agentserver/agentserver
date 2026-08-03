package coreserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type UserApprovalCommands interface {
	DecideUserApproval(context.Context, DecideUserApprovalCommand) (corecontract.DecideUserApprovalResponse, error)
}

type UserApprovalHandler struct {
	workload WorkloadAuthorizer
	users    UserTokenAuthorizer
	commands UserApprovalCommands
}

func NewUserApprovalHandler(workload WorkloadAuthorizer, users UserTokenAuthorizer, commands UserApprovalCommands) (*UserApprovalHandler, error) {
	if workload == nil || users == nil || commands == nil {
		return nil, errors.New("browser workload authorizer, user token authorizer, and user approval commands are required")
	}
	return &UserApprovalHandler{workload: workload, users: users, commands: commands}, nil
}

func (handler *UserApprovalHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(corecontract.DecideUserApprovalRoutePattern, handler)
	return mux
}

func (handler *UserApprovalHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	approvalID, ok := strings.CutSuffix(request.PathValue("approvalAction"), ":decide")
	if !ok || approvalID == "" {
		writePublicRunError(response, http.StatusNotFound, "not_found", "user approval endpoint not found", "")
		return
	}
	actorID, ok := handler.authorize(response, request)
	if !ok {
		return
	}
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "approval decision does not accept query parameters", "")
		return
	}
	var input corecontract.DecideUserApprovalRequest
	if !decodePublicRunJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.DecideUserApproval(request.Context(), DecideUserApprovalCommand{
		ActorID: actorID, WorkspaceID: request.PathValue("workspaceId"), ApprovalID: approvalID,
		Decision: input.Decision, Nonce: input.Nonce, ContextDigest: input.ContextDigest,
		ExpectedApprovalVersion: input.ExpectedApprovalVersion,
	})
	if err != nil {
		handler.writeServiceError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *UserApprovalHandler) authorize(response http.ResponseWriter, request *http.Request) (string, bool) {
	if err := handler.workload.AuthorizeWorkload(request, "approvals.decide"); err != nil {
		writePublicRunError(response, http.StatusForbidden, "forbidden", "calling workload is not authorized", "")
		return "", false
	}
	actorID, err := handler.users.AuthorizeUser(request, "approvals.decide")
	if err == nil {
		return actorID, true
	}
	if errors.Is(err, ErrUserAuthUnavailable) {
		writePublicRunError(response, http.StatusServiceUnavailable, "authorization_unavailable", "user authorization is temporarily unavailable", "")
		return "", false
	}
	response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-browser-api"`)
	writePublicRunError(response, http.StatusUnauthorized, "unauthorized", "a valid agentserver-browser-api access token is required", "")
	return "", false
}

func (handler *UserApprovalHandler) writeServiceError(response http.ResponseWriter, request *http.Request, err error) {
	if request.Context().Err() != nil {
		return
	}
	var stateError *coredb.StateError
	if !errors.As(err, &stateError) || stateError.Code == coredb.ErrorDatabase {
		writePublicRunError(response, http.StatusInternalServerError, "internal_error", "core could not complete the approval decision", "")
		return
	}
	status := http.StatusConflict
	switch stateError.Code {
	case coredb.ErrorInvalidArgument:
		status = http.StatusBadRequest
	case coredb.ErrorForbidden:
		status = http.StatusForbidden
	case coredb.ErrorNotFound:
		status = http.StatusNotFound
	}
	writePublicRunError(response, status, string(stateError.Code), stateError.Message, stateError.CurrentRunID)
}

type UserApprovalStateStore interface {
	DecideApproval(context.Context, coredb.DecideApprovalCommand) (coredb.DecideApprovalResult, error)
}

type UserApprovalServiceConfig struct {
	Store UserApprovalStateStore
	NewID UserRunIDGenerator
}

type UserApprovalService struct {
	store UserApprovalStateStore
	newID UserRunIDGenerator
}

type DecideUserApprovalCommand struct {
	ActorID                 string
	WorkspaceID             string
	ApprovalID              string
	Decision                string
	Nonce                   string
	ContextDigest           corecontract.CanonicalJSONDigest
	ExpectedApprovalVersion int64
}

func NewUserApprovalService(config UserApprovalServiceConfig) (*UserApprovalService, error) {
	if config.Store == nil {
		return nil, errors.New("user approval store is required")
	}
	if config.NewID == nil {
		config.NewID = newCoreUUID
	}
	return &UserApprovalService{store: config.Store, newID: config.NewID}, nil
}

func (service *UserApprovalService) DecideUserApproval(ctx context.Context, command DecideUserApprovalCommand) (corecontract.DecideUserApprovalResponse, error) {
	if err := validateDecideUserApprovalCommand(command); err != nil {
		return corecontract.DecideUserApprovalResponse{}, publicRunStateError(coredb.ErrorInvalidArgument, "DecideUserApproval", "approval", command.ApprovalID, err.Error())
	}
	contextHash, err := approvalContextDigest(command.ContextDigest)
	if err != nil {
		return corecontract.DecideUserApprovalResponse{}, publicRunStateError(coredb.ErrorInvalidArgument, "DecideUserApproval", "approval", command.ApprovalID, err.Error())
	}
	identities := make([]string, 3)
	seen := make(map[string]struct{}, len(identities))
	for index := range identities {
		identity, err := service.newID()
		if err != nil {
			return corecontract.DecideUserApprovalResponse{}, fmt.Errorf("allocate approval decision transition identity: %w", err)
		}
		if _, duplicate := seen[identity]; duplicate {
			return corecontract.DecideUserApprovalResponse{}, errors.New("approval decision identity generator returned a duplicate identity")
		}
		seen[identity] = struct{}{}
		identities[index] = identity
	}
	result, err := service.store.DecideApproval(ctx, coredb.DecideApprovalCommand{
		ApprovalID: command.ApprovalID, WorkspaceID: command.WorkspaceID, ActorID: command.ActorID,
		Nonce: command.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: command.ExpectedApprovalVersion, Decision: command.Decision,
		Record: coredb.TransitionRecord{EventID: identities[0], ProducerInstanceID: identities[1], ProducerSeq: 1, OutboxID: identities[2]},
	})
	if err != nil {
		return corecontract.DecideUserApprovalResponse{}, err
	}
	if result.Approval.ID != command.ApprovalID || result.Approval.ExecutionID != result.Execution.ID ||
		result.Approval.RunID != result.Execution.RunID || result.Approval.RunAttemptID != result.Execution.RunAttemptID {
		return corecontract.DecideUserApprovalResponse{}, errors.New("core state store returned an invalid approval scope")
	}
	return corecontract.DecideUserApprovalResponse{
		WorkspaceID: command.WorkspaceID, ExecutionID: result.Execution.ID,
		ExecutionStatus: result.Execution.Status, ExecutionVersion: result.Execution.Version,
		Approval: contractApproval(result.Approval), Changed: result.Changed,
	}, nil
}

func validateDecideUserApprovalCommand(command DecideUserApprovalCommand) error {
	for field, value := range map[string]string{
		"actorId": command.ActorID, "workspaceId": command.WorkspaceID,
		"approvalId": command.ApprovalID, "nonce": command.Nonce,
	} {
		if !canonicalPublicUUID(value) {
			return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
		}
	}
	if command.Decision != coredb.ApprovalDecisionApprove && command.Decision != coredb.ApprovalDecisionDeny {
		return errors.New("decision must be approve or deny")
	}
	if command.ExpectedApprovalVersion < 1 || command.ExpectedApprovalVersion >= 1<<53-1 {
		return errors.New("expectedApprovalVersion must be a positive JSON-safe integer")
	}
	return nil
}
