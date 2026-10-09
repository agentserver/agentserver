package corecontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

const WorkspaceRepositoryRoutePattern = "/v2/workspaces/{workspaceId}/repository"

func WorkspaceRepositoryPath(workspaceID string) string {
	return WorkspacePath(workspaceID) + "/repository"
}

type WorkspaceRepositorySettingState struct {
	WorkspaceID string                      `json:"workspaceId"`
	Source      *workspacerepository.Source `json:"source"`
	Version     int64                       `json:"version"`
	UpdatedBy   string                      `json:"updatedBy,omitempty"`
	UpdatedAt   *time.Time                  `json:"updatedAt,omitempty"`
}
type GetWorkspaceRepositoryResponse struct {
	Setting WorkspaceRepositorySettingState `json:"setting"`
}
type UpdateWorkspaceRepositoryRequest struct {
	Source          *workspacerepository.Source `json:"source"`
	ExpectedVersion int64                       `json:"expectedVersion"`
}

// Clearing a setting requires an explicit source:null and CAS version. A typo
// or an empty PATCH must never be interpreted as a request to clear it.
func (r *UpdateWorkspaceRepositoryRequest) UnmarshalJSON(raw []byte) error {
	var input struct {
		Source          json.RawMessage `json:"source"`
		ExpectedVersion *int64          `json:"expectedVersion"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&input); err != nil {
		return err
	}
	if len(input.Source) == 0 || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 || *input.ExpectedVersion >= 1<<53-1 {
		return errors.New("source and a nonnegative JSON-safe expectedVersion are required")
	}
	var source *workspacerepository.Source
	d = json.NewDecoder(bytes.NewReader(input.Source))
	d.DisallowUnknownFields()
	if err := d.Decode(&source); err != nil {
		return err
	}
	*r = UpdateWorkspaceRepositoryRequest{Source: source, ExpectedVersion: *input.ExpectedVersion}
	return nil
}

type UpdateWorkspaceRepositoryResponse struct {
	Setting WorkspaceRepositorySettingState `json:"setting"`
	Changed bool                            `json:"changed"`
}
