package executorgateway

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/agentserver/agentserver/v2/internal/runcapability"
	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

func repositoryFromCapability(claims runcapability.Claims) (*workspacerepository.Binding, error) {
	if claims.WorkspaceRepositoryID == "" {
		return nil, nil
	}
	if claims.WorkspaceRepositoryDescriptor == "" {
		return nil, errors.New("workspace repository descriptor is missing")
	}
	raw, err := base64.RawURLEncoding.DecodeString(claims.WorkspaceRepositoryDescriptor)
	if err != nil {
		return nil, errors.New("workspace repository descriptor is invalid")
	}
	var binding workspacerepository.Binding
	if json.Unmarshal(raw, &binding) != nil || binding.CheckoutID != claims.WorkspaceRepositoryID || binding.Validate() != nil {
		return nil, errors.New("workspace repository descriptor is invalid")
	}
	return &binding, nil
}

func RepositoryBindingFromCapability(claims runcapability.Claims) (*workspacerepository.Binding, error) {
	return repositoryFromCapability(claims)
}
