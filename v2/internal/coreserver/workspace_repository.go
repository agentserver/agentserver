package coreserver

import (
	"context"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type WorkspaceRepositoryCommands interface {
	GetRepository(context.Context, string, string) (corecontract.GetWorkspaceRepositoryResponse, error)
	UpdateRepository(context.Context, string, string, corecontract.UpdateWorkspaceRepositoryRequest) (corecontract.UpdateWorkspaceRepositoryResponse, error)
}

func (h *PlatformResourceHandler) repository(w http.ResponseWriter, r *http.Request) {
	platformNoStore(w)
	if r.URL.RawQuery != "" {
		writePublicRunError(w, http.StatusBadRequest, "invalid_argument", "repository setting does not accept query parameters", "")
		return
	}
	commands, ok := h.commands.(WorkspaceRepositoryCommands)
	if !ok {
		writePublicRunError(w, http.StatusServiceUnavailable, "unavailable", "workspace repository configuration is unavailable", "")
		return
	}
	switch r.Method {
	case http.MethodGet:
		actor, ok := h.authorize(w, r, "workspaces.get")
		if !ok || !requireEmptyPlatformBody(w, r, "repository read") {
			return
		}
		out, err := commands.GetRepository(r.Context(), r.PathValue("workspaceId"), actor)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPatch:
		actor, ok := h.authorize(w, r, "workspaces.update")
		if !ok {
			return
		}
		var input corecontract.UpdateWorkspaceRepositoryRequest
		if !decodePlatformResourceJSON(w, r, &input) {
			return
		}
		out, err := commands.UpdateRepository(r.Context(), r.PathValue("workspaceId"), actor, input)
		if err != nil {
			h.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	default:
		w.Header().Set("Allow", "GET, PATCH")
		writePublicRunError(w, http.StatusMethodNotAllowed, "method_not_allowed", "repository setting requires GET or PATCH", "")
	}
}

func repositoryState(s coredb.WorkspaceRepositorySetting) corecontract.WorkspaceRepositorySettingState {
	return corecontract.WorkspaceRepositorySettingState{WorkspaceID: s.WorkspaceID, Source: s.Source, Version: s.Version, UpdatedBy: s.UpdatedBy, UpdatedAt: s.UpdatedAt}
}
func (c StateStorePlatformResourceCommands) GetRepository(ctx context.Context, wid, actor string) (corecontract.GetWorkspaceRepositoryResponse, error) {
	s, err := c.Store.GetWorkspaceRepository(ctx, wid, actor)
	return corecontract.GetWorkspaceRepositoryResponse{Setting: repositoryState(s)}, err
}
func (c StateStorePlatformResourceCommands) UpdateRepository(ctx context.Context, wid, actor string, input corecontract.UpdateWorkspaceRepositoryRequest) (corecontract.UpdateWorkspaceRepositoryResponse, error) {
	id, err := newCredentialEventID()
	if err != nil {
		return corecontract.UpdateWorkspaceRepositoryResponse{}, err
	}
	result, err := c.Store.UpdateWorkspaceRepository(ctx, coredb.UpdateWorkspaceRepositoryCommand{WorkspaceID: wid, ActorID: actor, Source: input.Source, ExpectedVersion: input.ExpectedVersion, AuditEventID: id})
	return corecontract.UpdateWorkspaceRepositoryResponse{Setting: repositoryState(result.Setting), Changed: result.Changed}, err
}
