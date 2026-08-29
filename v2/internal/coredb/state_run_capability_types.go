package coredb

import (
	"time"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/workspaceauthority"
)

const (
	RunCapabilityAudienceExecutorMCP = "executor-mcp"
	RunCapabilityAudienceLLMProxy    = "llmproxy"
)

type ResolveRunCapabilityIssuanceCommand struct {
	WorkspaceID            string
	SessionID              string
	RunID                  string
	AttemptID              string
	HolderID               string
	Generation             int64
	ExpectedRunVersion     int64
	ExpectedAttemptVersion int64
	ExecutorID             string
	BrainToolCatalogID     string
	ToolCatalogDigest      [32]byte
	LLMGateway             RunLLMGatewayBinding
	ManagedSandbox         RunManagedSandboxBinding
	Workspace              *workspaceauthority.Binding
	// PermissionMode is the manifest projection supplied by the harness. Core
	// compares it with the mode frozen in run_launch_states before issuing a
	// capability; it is never trusted as authority by itself.
	PermissionMode        runmanifest.CodexPermissionMode
	PermissionModeVersion int64
}

type RunCapabilityIssuanceAuthority struct {
	WorkspaceID            string
	SessionID              string
	RunID                  string
	AttemptID              string
	ActorID                string
	HolderID               string
	Generation             int64
	RunVersion             int64
	AttemptVersion         int64
	AttemptCreatedAt       time.Time
	DatabaseTime           time.Time
	ExecutorID             string
	BrainToolCatalogID     string
	ToolCatalogDigest      [32]byte
	LLMGateway             RunLLMGatewayBinding
	ManagedSandbox         RunManagedSandboxBinding
	Workspace              *workspaceauthority.Binding
	PermissionMode         runmanifest.CodexPermissionMode
	PermissionModeVersion  int64
	PermissionModeExplicit bool
}

type AuthorizeRunCapabilityCommand struct {
	Audience               string
	CapabilityID           string
	WorkspaceID            string
	SessionID              string
	RunID                  string
	AttemptID              string
	ActorID                string
	HolderID               string
	Generation             int64
	ExecutorID             string
	ToolCatalogDigest      [32]byte
	ExpectedRunVersion     int64
	ExpectedAttemptVersion int64
	LLMGateway             RunLLMGatewayBinding
	ManagedSandbox         RunManagedSandboxBinding
	Workspace              *workspaceauthority.Binding
	PermissionMode         runmanifest.CodexPermissionMode
	PermissionModeVersion  int64
}

type AuthorizedRunCapability struct {
	RunVersion     int64
	AttemptVersion int64
	RunStatus      string
	AttemptStatus  string
	DatabaseTime   time.Time
	LLMGateway     *LLMGatewayLiveAuthority
}
