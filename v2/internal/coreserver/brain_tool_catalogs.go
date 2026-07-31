package coreserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type BrainToolCatalogCommands interface {
	FreezeBrainToolCatalog(context.Context, corecontract.FreezeBrainToolCatalogRequest) (corecontract.FreezeBrainToolCatalogResponse, error)
	BindBrainThreadCatalog(context.Context, corecontract.BindBrainThreadCatalogRequest) (corecontract.BindBrainThreadCatalogResponse, error)
}

type BrainToolCatalogHandler struct {
	authorizer WorkloadAuthorizer
	commands   BrainToolCatalogCommands
}

func NewBrainToolCatalogHandler(authorizer WorkloadAuthorizer, commands BrainToolCatalogCommands) (*BrainToolCatalogHandler, error) {
	if authorizer == nil {
		return nil, errors.New("workload authorizer is required")
	}
	if commands == nil {
		return nil, errors.New("brain tool catalog commands are required")
	}
	return &BrainToolCatalogHandler{authorizer: authorizer, commands: commands}, nil
}

func (handler *BrainToolCatalogHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	if request.URL.Path == corecontract.FreezeBrainToolCatalogPath {
		handler.freeze(response, request)
		return
	}
	catalogID, ok := parseBindBrainThreadCatalogPath(request.URL.Path)
	if !ok {
		writeError(response, http.StatusNotFound, corecontract.ErrorResponse{Code: "not_found", Message: "internal command endpoint not found"})
		return
	}
	handler.bind(response, request, catalogID)
}

func (handler *BrainToolCatalogHandler) freeze(response http.ResponseWriter, request *http.Request) {
	if !handler.authorize(response, request, "brain-tool-catalogs.freeze") {
		return
	}
	var command corecontract.FreezeBrainToolCatalogRequest
	if !decodeCommandWithLimit(response, request, &command, maxRunAttemptEventCommandBytes) {
		return
	}
	result, err := handler.commands.FreezeBrainToolCatalog(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *BrainToolCatalogHandler) bind(response http.ResponseWriter, request *http.Request, catalogID string) {
	if !handler.authorize(response, request, "brain-tool-catalogs.bind-thread") {
		return
	}
	var command corecontract.BindBrainThreadCatalogRequest
	if !decodeCommand(response, request, &command) {
		return
	}
	if command.CatalogID != catalogID {
		writePathIdentityError(response, "catalogId")
		return
	}
	result, err := handler.commands.BindBrainThreadCatalog(request.Context(), command)
	if err != nil {
		writeCommandError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *BrainToolCatalogHandler) authorize(response http.ResponseWriter, request *http.Request, action string) bool {
	if err := handler.authorizer.AuthorizeWorkload(request, action); err != nil {
		writeError(response, http.StatusForbidden, corecontract.ErrorResponse{Code: "forbidden", Message: "workload is not authorized for this command"})
		return false
	}
	return true
}

func parseBindBrainThreadCatalogPath(path string) (string, bool) {
	if !strings.HasPrefix(path, corecontract.BrainToolCatalogPathPrefix) {
		return "", false
	}
	const suffix = ":bindThread"
	remainder := strings.TrimPrefix(path, corecontract.BrainToolCatalogPathPrefix)
	if !strings.HasSuffix(remainder, suffix) || len(remainder) <= len(suffix) || strings.Contains(remainder, "/") {
		return "", false
	}
	return strings.TrimSuffix(remainder, suffix), true
}
