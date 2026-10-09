package executorgateway

import (
	"github.com/agentserver/agentserver/v2/internal/runcapability"
	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

func repositoryFromCapability(claims runcapability.Claims) (*workspacerepository.Binding, error) {
	return claims.RepositoryBinding()
}

func RepositoryBindingFromCapability(claims runcapability.Claims) (*workspacerepository.Binding, error) {
	return repositoryFromCapability(claims)
}
