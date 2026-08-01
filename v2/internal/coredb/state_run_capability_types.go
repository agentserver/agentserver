package coredb

import "time"

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
}

type RunCapabilityIssuanceAuthority struct {
	WorkspaceID        string
	SessionID          string
	RunID              string
	AttemptID          string
	ActorID            string
	HolderID           string
	Generation         int64
	RunVersion         int64
	AttemptVersion     int64
	AttemptCreatedAt   time.Time
	DatabaseTime       time.Time
	ExecutorID         string
	BrainToolCatalogID string
	ToolCatalogDigest  [32]byte
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
}

type AuthorizedRunCapability struct {
	RunVersion     int64
	AttemptVersion int64
	RunStatus      string
	AttemptStatus  string
	DatabaseTime   time.Time
}
