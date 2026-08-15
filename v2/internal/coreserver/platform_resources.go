package coreserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
)

const maximumPlatformResourceRequestBytes = int64(64 * 1024)

// PlatformResourceCommands is the closed management surface exposed to the
// Platform application. Keeping the contract here prevents the public gateway
// from becoming a generic Core reverse proxy.
type PlatformResourceCommands interface {
	ListWorkspaces(context.Context, string) (corecontract.ListWorkspacesResponse, error)
	GetWorkspace(context.Context, string, string) (corecontract.WorkspaceState, error)
	CreateWorkspace(context.Context, string, corecontract.CreateWorkspaceRequest) (corecontract.CreateWorkspaceResponse, error)
	UpdateWorkspace(context.Context, string, string, corecontract.UpdateWorkspaceRequest) (corecontract.UpdateWorkspaceResponse, error)
	ArchiveWorkspace(context.Context, string, string, corecontract.ArchiveWorkspaceRequest) (corecontract.ArchiveWorkspaceResponse, error)
	GetManagedSandboxSetting(context.Context, string, string) (corecontract.GetWorkspaceManagedSandboxSettingResponse, error)
	UpdateManagedSandboxSetting(context.Context, string, string, corecontract.UpdateWorkspaceManagedSandboxSettingRequest) (corecontract.UpdateWorkspaceManagedSandboxSettingResponse, error)
	ListMembers(context.Context, string, string) (corecontract.ListWorkspaceMembersResponse, error)
	AddMember(context.Context, string, string, corecontract.AddWorkspaceMemberRequest) (corecontract.AddWorkspaceMemberResponse, error)
	UpdateMember(context.Context, string, string, string, corecontract.UpdateWorkspaceMemberRequest) (corecontract.UpdateWorkspaceMemberResponse, error)
	RemoveMember(context.Context, string, string, string) (corecontract.RemoveWorkspaceMemberResponse, error)
}

type PlatformResourceHandler struct {
	workload WorkloadAuthorizer
	users    UserTokenAuthorizer
	commands PlatformResourceCommands
}

func NewPlatformResourceHandler(workload WorkloadAuthorizer, users UserTokenAuthorizer, commands PlatformResourceCommands) (*PlatformResourceHandler, error) {
	if workload == nil || users == nil || commands == nil {
		return nil, errors.New("platform workload, user authorizer, and resource commands are required")
	}
	return &PlatformResourceHandler{workload: workload, users: users, commands: commands}, nil
}

func (handler *PlatformResourceHandler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(corecontract.WorkspaceCollectionRoutePattern, handler.workspaceCollection)
	mux.HandleFunc(corecontract.WorkspaceResourceRoutePattern, handler.workspaceResource)
	mux.HandleFunc(corecontract.WorkspaceArchiveRoutePattern, handler.archiveWorkspace)
	mux.HandleFunc(corecontract.WorkspaceManagedSandboxRoutePattern, handler.managedSandboxSetting)
	mux.HandleFunc(corecontract.WorkspaceMembersCollectionPattern, handler.memberCollection)
	mux.HandleFunc(corecontract.WorkspaceMemberResourceRoutePattern, handler.memberResource)
	return mux
}

func (handler *PlatformResourceHandler) managedSandboxSetting(response http.ResponseWriter, request *http.Request) {
	platformNoStore(response)
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "managed sandbox setting does not accept query parameters", "")
		return
	}
	workspaceID := request.PathValue("workspaceId")
	switch request.Method {
	case http.MethodGet:
		actorID, ok := handler.authorize(response, request, "workspaces.get")
		if !ok || !requireEmptyPlatformBody(response, request, "managed sandbox setting read") {
			return
		}
		result, err := handler.commands.GetManagedSandboxSetting(request.Context(), workspaceID, actorID)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case http.MethodPatch:
		actorID, ok := handler.authorize(response, request, "workspaces.update")
		if !ok {
			return
		}
		var input corecontract.UpdateWorkspaceManagedSandboxSettingRequest
		if !decodePlatformResourceJSON(response, request, &input) {
			return
		}
		if !managedsandboxprofile.ValidRegion(input.Region) {
			writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "region must be cn, boe, i18n-bd, or i18n-tt", "")
			return
		}
		result, err := handler.commands.UpdateManagedSandboxSetting(request.Context(), workspaceID, actorID, input)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	default:
		response.Header().Set("Allow", "GET, PATCH")
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "managed sandbox setting requires GET or PATCH", "")
	}
}

func (handler *PlatformResourceHandler) workspaceCollection(response http.ResponseWriter, request *http.Request) {
	platformNoStore(response)
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace collection does not accept query parameters", "")
		return
	}
	switch request.Method {
	case http.MethodGet:
		actorID, ok := handler.authorize(response, request, "workspaces.list")
		if !ok || !requireEmptyPlatformBody(response, request, "workspace list") {
			return
		}
		result, err := handler.commands.ListWorkspaces(request.Context(), actorID)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case http.MethodPost:
		actorID, ok := handler.authorize(response, request, "workspaces.create")
		if !ok {
			return
		}
		var input corecontract.CreateWorkspaceRequest
		if !decodePlatformResourceJSON(response, request, &input) {
			return
		}
		if !requireWorkspaceManagedLarkMode(response, input.ManagedLarkCredentialMode) {
			return
		}
		result, err := handler.commands.CreateWorkspace(request.Context(), actorID, input)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		status := http.StatusOK
		if result.Created {
			status = http.StatusCreated
		}
		writeJSON(response, status, result)
	default:
		response.Header().Set("Allow", "GET, POST")
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "workspace collection requires GET or POST", "")
	}
}

func (handler *PlatformResourceHandler) workspaceResource(response http.ResponseWriter, request *http.Request) {
	platformNoStore(response)
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace resource does not accept query parameters", "")
		return
	}
	workspaceID := request.PathValue("workspaceId")
	switch request.Method {
	case http.MethodGet:
		actorID, ok := handler.authorize(response, request, "workspaces.get")
		if !ok || !requireEmptyPlatformBody(response, request, "workspace read") {
			return
		}
		result, err := handler.commands.GetWorkspace(request.Context(), workspaceID, actorID)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case http.MethodPatch:
		actorID, ok := handler.authorize(response, request, "workspaces.update")
		if !ok {
			return
		}
		var input corecontract.UpdateWorkspaceRequest
		if !decodePlatformResourceJSON(response, request, &input) {
			return
		}
		if !requireWorkspaceManagedLarkMode(response, input.ManagedLarkCredentialMode) {
			return
		}
		result, err := handler.commands.UpdateWorkspace(request.Context(), workspaceID, actorID, input)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	default:
		response.Header().Set("Allow", "GET, PATCH")
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "workspace resource requires GET or PATCH", "")
	}
}

func (handler *PlatformResourceHandler) archiveWorkspace(response http.ResponseWriter, request *http.Request) {
	platformNoStore(response)
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "workspace archive requires POST", "")
		return
	}
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace archive does not accept query parameters", "")
		return
	}
	actorID, ok := handler.authorize(response, request, "workspaces.archive")
	if !ok {
		return
	}
	var input corecontract.ArchiveWorkspaceRequest
	if !decodePlatformResourceJSON(response, request, &input) {
		return
	}
	result, err := handler.commands.ArchiveWorkspace(request.Context(), request.PathValue("workspaceId"), actorID, input)
	if err != nil {
		handler.writeError(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *PlatformResourceHandler) memberCollection(response http.ResponseWriter, request *http.Request) {
	platformNoStore(response)
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace members do not accept query parameters", "")
		return
	}
	workspaceID := request.PathValue("workspaceId")
	switch request.Method {
	case http.MethodGet:
		actorID, ok := handler.authorize(response, request, "members.list")
		if !ok || !requireEmptyPlatformBody(response, request, "member list") {
			return
		}
		result, err := handler.commands.ListMembers(request.Context(), workspaceID, actorID)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case http.MethodPost:
		actorID, ok := handler.authorize(response, request, "members.add")
		if !ok {
			return
		}
		var input corecontract.AddWorkspaceMemberRequest
		if !decodePlatformResourceJSON(response, request, &input) {
			return
		}
		result, err := handler.commands.AddMember(request.Context(), workspaceID, actorID, input)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		status := http.StatusOK
		if result.Created {
			status = http.StatusCreated
		}
		writeJSON(response, status, result)
	default:
		response.Header().Set("Allow", "GET, POST")
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "workspace members require GET or POST", "")
	}
}

func (handler *PlatformResourceHandler) memberResource(response http.ResponseWriter, request *http.Request) {
	platformNoStore(response)
	if request.URL.RawQuery != "" {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "workspace member does not accept query parameters", "")
		return
	}
	workspaceID, memberID := request.PathValue("workspaceId"), request.PathValue("memberId")
	switch request.Method {
	case http.MethodPatch:
		actorID, ok := handler.authorize(response, request, "members.update")
		if !ok {
			return
		}
		var input corecontract.UpdateWorkspaceMemberRequest
		if !decodePlatformResourceJSON(response, request, &input) {
			return
		}
		result, err := handler.commands.UpdateMember(request.Context(), workspaceID, memberID, actorID, input)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	case http.MethodDelete:
		actorID, ok := handler.authorize(response, request, "members.remove")
		if !ok || !requireEmptyPlatformBody(response, request, "member removal") {
			return
		}
		result, err := handler.commands.RemoveMember(request.Context(), workspaceID, memberID, actorID)
		if err != nil {
			handler.writeError(response, request, err)
			return
		}
		writeJSON(response, http.StatusOK, result)
	default:
		response.Header().Set("Allow", "PATCH, DELETE")
		writePublicRunError(response, http.StatusMethodNotAllowed, "method_not_allowed", "workspace member requires PATCH or DELETE", "")
	}
}

func (handler *PlatformResourceHandler) authorize(response http.ResponseWriter, request *http.Request, action string) (string, bool) {
	if err := handler.workload.AuthorizeWorkload(request, action); err != nil {
		writePublicRunError(response, http.StatusForbidden, "forbidden", "calling workload is not authorized", "")
		return "", false
	}
	actorID, err := handler.users.AuthorizeUser(request, action)
	if err == nil {
		return actorID, true
	}
	if errors.Is(err, ErrUserAuthUnavailable) {
		writePublicRunError(response, http.StatusServiceUnavailable, "authorization_unavailable", "user authorization is temporarily unavailable", "")
		return "", false
	}
	response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-platform-api"`)
	writePublicRunError(response, http.StatusUnauthorized, "unauthorized", "a valid agentserver-platform-api access token is required", "")
	return "", false
}

func (handler *PlatformResourceHandler) writeError(response http.ResponseWriter, request *http.Request, err error) {
	if request.Context().Err() != nil {
		return
	}
	var stateError *coredb.StateError
	if !errors.As(err, &stateError) || stateError.Code == coredb.ErrorDatabase {
		writePublicRunError(response, http.StatusInternalServerError, "internal_error", "core could not complete the platform resource request", "")
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
	writePublicRunError(response, status, string(stateError.Code), stateError.Message, "")
}

func platformNoStore(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
}

func requireWorkspaceManagedLarkMode(response http.ResponseWriter, mode string) bool {
	if managedcredential.ValidMode(mode) {
		return true
	}
	writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "managedLarkCredentialMode must be webhook_swap or process_env", "")
	return false
}

func requireEmptyPlatformBody(response http.ResponseWriter, request *http.Request, operation string) bool {
	if request.ContentLength == 0 && len(request.TransferEncoding) == 0 {
		return true
	}
	writePublicRunError(response, http.StatusBadRequest, "invalid_argument", operation+" requires an empty request", "")
	return false
}

func decodePlatformResourceJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || len(parameters) != 0 {
		writePublicRunError(response, http.StatusUnsupportedMediaType, "invalid_argument", "Content-Type must be exactly application/json", "")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumPlatformResourceRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "request body is not valid platform resource JSON", "")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writePublicRunError(response, http.StatusBadRequest, "invalid_argument", "request body contains trailing data", "")
		return false
	}
	return true
}

// StateStorePlatformResourceCommands is the Core DB adapter for the public
// contract. It intentionally contains no authorization policy; both OAuth and
// membership are rechecked by the handler and transactional StateStore calls.
type StateStorePlatformResourceCommands struct {
	Store                          *coredb.StateStore
	AvailableManagedSandboxRegions []string
}

func (commands StateStorePlatformResourceCommands) ListWorkspaces(ctx context.Context, actorID string) (corecontract.ListWorkspacesResponse, error) {
	items, err := commands.Store.ListPlatformWorkspaces(ctx, actorID)
	if err != nil {
		return corecontract.ListWorkspacesResponse{}, err
	}
	result := make([]corecontract.WorkspaceState, len(items))
	for index := range items {
		result[index] = contractPlatformWorkspace(items[index])
	}
	return corecontract.ListWorkspacesResponse{Workspaces: result}, nil
}

func (commands StateStorePlatformResourceCommands) GetWorkspace(ctx context.Context, workspaceID, actorID string) (corecontract.WorkspaceState, error) {
	workspace, err := commands.Store.GetPlatformWorkspace(ctx, workspaceID, actorID)
	return contractPlatformWorkspace(workspace), err
}

func (commands StateStorePlatformResourceCommands) CreateWorkspace(ctx context.Context, actorID string, input corecontract.CreateWorkspaceRequest) (corecontract.CreateWorkspaceResponse, error) {
	result, err := commands.Store.CreatePlatformWorkspace(ctx, coredb.CreatePlatformWorkspaceCommand{
		WorkspaceID: input.WorkspaceID, ActorID: actorID, Name: input.Name,
		ManagedLarkCredentialMode: input.ManagedLarkCredentialMode,
	})
	return corecontract.CreateWorkspaceResponse{Workspace: contractPlatformWorkspace(result.Workspace), Created: result.Created}, err
}

func (commands StateStorePlatformResourceCommands) UpdateWorkspace(ctx context.Context, workspaceID, actorID string, input corecontract.UpdateWorkspaceRequest) (corecontract.UpdateWorkspaceResponse, error) {
	auditEventID, err := newCredentialEventID()
	if err != nil {
		return corecontract.UpdateWorkspaceResponse{}, err
	}
	result, err := commands.Store.UpdatePlatformWorkspace(ctx, coredb.UpdatePlatformWorkspaceCommand{
		WorkspaceID: workspaceID, ActorID: actorID, Name: input.Name,
		ManagedLarkCredentialMode: input.ManagedLarkCredentialMode,
		ExpectedVersion:           input.ExpectedVersion, AuditEventID: auditEventID,
	})
	return corecontract.UpdateWorkspaceResponse{Workspace: contractPlatformWorkspace(result.Workspace), Changed: result.Changed}, err
}

func (commands StateStorePlatformResourceCommands) ArchiveWorkspace(ctx context.Context, workspaceID, actorID string, input corecontract.ArchiveWorkspaceRequest) (corecontract.ArchiveWorkspaceResponse, error) {
	result, err := commands.Store.ArchivePlatformWorkspace(ctx, coredb.ArchivePlatformWorkspaceCommand{WorkspaceID: workspaceID, ActorID: actorID, ExpectedVersion: input.ExpectedVersion})
	return corecontract.ArchiveWorkspaceResponse{Workspace: contractPlatformWorkspace(result.Workspace), Changed: result.Changed}, err
}

func (commands StateStorePlatformResourceCommands) GetManagedSandboxSetting(ctx context.Context, workspaceID, actorID string) (corecontract.GetWorkspaceManagedSandboxSettingResponse, error) {
	setting, err := commands.Store.GetWorkspaceManagedSandboxSetting(ctx, workspaceID, actorID)
	regions := append([]string(nil), commands.AvailableManagedSandboxRegions...)
	if len(regions) == 0 {
		regions = managedsandboxprofile.Regions()
	}
	return corecontract.GetWorkspaceManagedSandboxSettingResponse{
		Setting: contractWorkspaceManagedSandboxSetting(setting), AvailableRegions: regions,
	}, err
}

func (commands StateStorePlatformResourceCommands) UpdateManagedSandboxSetting(ctx context.Context, workspaceID, actorID string, input corecontract.UpdateWorkspaceManagedSandboxSettingRequest) (corecontract.UpdateWorkspaceManagedSandboxSettingResponse, error) {
	available := commands.AvailableManagedSandboxRegions
	if len(available) > 0 {
		found := false
		for _, region := range available {
			found = found || region == input.Region
		}
		if !found {
			return corecontract.UpdateWorkspaceManagedSandboxSettingResponse{}, &coredb.StateError{
				Code: coredb.ErrorInvalidState, Operation: "UpdateWorkspaceManagedSandboxSetting",
				Resource: "workspace", ResourceID: workspaceID,
				Message: "managed sandbox region has no active deployment profile",
			}
		}
	}
	auditEventID, err := newCredentialEventID()
	if err != nil {
		return corecontract.UpdateWorkspaceManagedSandboxSettingResponse{}, err
	}
	result, err := commands.Store.UpdateWorkspaceManagedSandboxSetting(ctx, coredb.UpdateWorkspaceManagedSandboxSettingCommand{
		WorkspaceID: workspaceID, ActorID: actorID, Region: input.Region,
		ExpectedVersion: input.ExpectedVersion, AuditEventID: auditEventID,
	})
	return corecontract.UpdateWorkspaceManagedSandboxSettingResponse{
		Setting: contractWorkspaceManagedSandboxSetting(result.Setting), Changed: result.Changed,
	}, err
}

func (commands StateStorePlatformResourceCommands) ListMembers(ctx context.Context, workspaceID, actorID string) (corecontract.ListWorkspaceMembersResponse, error) {
	items, err := commands.Store.ListWorkspaceMembers(ctx, workspaceID, actorID)
	if err != nil {
		return corecontract.ListWorkspaceMembersResponse{}, err
	}
	result := make([]corecontract.WorkspaceMemberState, len(items))
	for index := range items {
		result[index] = contractWorkspaceMember(items[index])
	}
	return corecontract.ListWorkspaceMembersResponse{Members: result}, nil
}

func (commands StateStorePlatformResourceCommands) AddMember(ctx context.Context, workspaceID, actorID string, input corecontract.AddWorkspaceMemberRequest) (corecontract.AddWorkspaceMemberResponse, error) {
	result, err := commands.Store.AddWorkspaceMember(ctx, coredb.AddWorkspaceMemberCommand{WorkspaceID: workspaceID, ActorID: actorID, UserID: input.UserID, Role: input.Role})
	return corecontract.AddWorkspaceMemberResponse{Member: contractWorkspaceMember(result.Member), Created: result.Created}, err
}

func (commands StateStorePlatformResourceCommands) UpdateMember(ctx context.Context, workspaceID, memberID, actorID string, input corecontract.UpdateWorkspaceMemberRequest) (corecontract.UpdateWorkspaceMemberResponse, error) {
	result, err := commands.Store.UpdateWorkspaceMember(ctx, coredb.UpdateWorkspaceMemberCommand{WorkspaceID: workspaceID, ActorID: actorID, UserID: memberID, Role: input.Role, ExpectedVersion: input.ExpectedVersion})
	return corecontract.UpdateWorkspaceMemberResponse{Member: contractWorkspaceMember(result.Member), Changed: result.Changed}, err
}

func (commands StateStorePlatformResourceCommands) RemoveMember(ctx context.Context, workspaceID, memberID, actorID string) (corecontract.RemoveWorkspaceMemberResponse, error) {
	result, err := commands.Store.RemoveWorkspaceMember(ctx, coredb.RemoveWorkspaceMemberCommand{WorkspaceID: workspaceID, ActorID: actorID, UserID: memberID})
	return corecontract.RemoveWorkspaceMemberResponse{UserID: result.UserID, Removed: result.Removed}, err
}

func contractPlatformWorkspace(workspace coredb.PlatformWorkspace) corecontract.WorkspaceState {
	return corecontract.WorkspaceState{
		WorkspaceID: workspace.ID, Name: workspace.Name, Status: workspace.Status,
		CurrentUserRole: workspace.Role, ManagedLarkCredentialMode: workspace.ManagedLarkCredentialMode,
		Version: workspace.Version, CreatedAt: workspace.CreatedAt, UpdatedAt: workspace.UpdatedAt,
	}
}

func contractWorkspaceMember(member coredb.WorkspaceMember) corecontract.WorkspaceMemberState {
	return corecontract.WorkspaceMemberState{UserID: member.UserID, Role: member.Role, Version: member.Version, CreatedAt: member.CreatedAt, UpdatedAt: member.UpdatedAt}
}

func contractWorkspaceManagedSandboxSetting(setting coredb.WorkspaceManagedSandboxSetting) corecontract.WorkspaceManagedSandboxSettingState {
	return corecontract.WorkspaceManagedSandboxSettingState{
		WorkspaceID: setting.WorkspaceID, Region: setting.Region, Version: setting.Version,
		UpdatedBy: setting.UpdatedBy, CreatedAt: setting.CreatedAt, UpdatedAt: setting.UpdatedAt,
	}
}

var _ PlatformResourceCommands = StateStorePlatformResourceCommands{}
