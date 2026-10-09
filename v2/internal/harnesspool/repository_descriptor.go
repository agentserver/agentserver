package harnesspool

import (
	"encoding/base64"
	"encoding/json"
	"github.com/agentserver/agentserver/v2/internal/workspaceauthority"
)

func workspaceRepositoryDescriptor(binding *workspaceauthority.Binding) string {
	if binding == nil || binding.Repository == nil {
		return ""
	}
	raw, _ := json.Marshal(binding.Repository)
	return base64.RawURLEncoding.EncodeToString(raw)
}
