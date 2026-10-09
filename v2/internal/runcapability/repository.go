package runcapability

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

func (c Claims) RepositoryBinding() (*workspacerepository.Binding, error) {
	invalid := errors.New("run repository descriptor is invalid")
	if c.WorkspaceRepositoryID == "" {
		if c.WorkspaceRepositoryDescriptor != "" {
			return nil, invalid
		}
		return nil, nil
	}
	if len(c.WorkspaceRepositoryDescriptor) > 12*1024 {
		return nil, invalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(c.WorkspaceRepositoryDescriptor)
	if err != nil || len(raw) == 0 {
		return nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var b workspacerepository.Binding
	if d.Decode(&b) != nil || b.Validate() != nil {
		return nil, invalid
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) || b.CheckoutID != c.WorkspaceRepositoryID || b.CheckoutID != c.SessionID || b.EnvironmentID != c.WorkspaceEnvironmentID || b.EnvironmentID != c.ManagedSandboxEnvironmentID || b.Region != c.ManagedSandboxRegion || b.ManagedSettingVersion != c.ManagedSandboxSettingVersion {
		return nil, invalid
	}
	return &b, nil
}
