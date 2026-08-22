package corecontract

const ResolveRunLaunchStatePath = "/internal/v2/run-launch-states:resolve"

type ResolveRunLaunchStateRequest struct {
	WorkspaceID               string `json:"workspaceId"`
	SessionID                 string `json:"sessionId"`
	RunID                     string `json:"runId"`
	RunAttemptID              string `json:"runAttemptId"`
	HolderID                  string `json:"holderId"`
	RunAttemptGeneration      int64  `json:"runAttemptGeneration"`
	ExpectedRunVersion        int64  `json:"expectedRunVersion"`
	ExpectedRunAttemptVersion int64  `json:"expectedRunAttemptVersion"`
}

type RunLaunchObjectPointer struct {
	ObjectID  string `json:"objectId"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
	MediaType string `json:"mediaType"`
}

type RunLaunchCheckpointState struct {
	CheckpointID               string                 `json:"checkpointId"`
	RunID                      string                 `json:"runId"`
	RunAttemptID               string                 `json:"runAttemptId"`
	RunAttemptGeneration       int64                  `json:"runAttemptGeneration"`
	ThreadID                   string                 `json:"threadId"`
	TurnID                     string                 `json:"turnId"`
	ManifestDigest             string                 `json:"manifestDigest"`
	CatalogDigest              string                 `json:"catalogDigest"`
	Catalog                    BrainToolCatalogState  `json:"catalog"`
	Object                     RunLaunchObjectPointer `json:"object"`
	CodexRuntimeManifestDigest string                 `json:"codexRuntimeManifestDigest"`
	CheckpointAllowlistVersion int64                  `json:"checkpointAllowlistVersion"`
}

type RunLaunchExecutorPolicyState struct {
	Version       string   `json:"version"`
	ContextDigest string   `json:"contextDigest"`
	AllowedTools  []string `json:"allowedTools"`
}

type RunLaunchLLMGatewayState struct {
	GatewayID     string `json:"gatewayId"`
	ConfigVersion int64  `json:"configVersion"`
	GrantUserID   string `json:"grantUserId"`
	Model         string `json:"model"`
}

type RunLaunchLarkEgressState struct {
	GrantID      string `json:"grantId"`
	GrantVersion int64  `json:"grantVersion"`
	GrantUserID  string `json:"grantUserId"`
	PolicySHA256 string `json:"policySha256"`
}

type RunLaunchManagedSandboxState struct {
	SettingVersion int64  `json:"settingVersion"`
	Region         string `json:"region"`
	EnvironmentID  string `json:"environmentId"`
}

type ResolveRunLaunchStateResponse struct {
	WorkspaceID          string `json:"workspaceId"`
	SessionID            string `json:"sessionId"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	HolderID             string `json:"holderId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	RunVersion           int64  `json:"runVersion"`
	RunAttemptVersion    int64  `json:"runAttemptVersion"`
	// PermissionMode and its version are pointers so a legacy launch row can
	// remain distinguishable from an explicit read-only selection.
	PermissionMode        *string                       `json:"permissionMode,omitempty"`
	PermissionModeVersion *int64                        `json:"permissionModeVersion,omitempty"`
	Prompt                RunLaunchObjectPointer        `json:"prompt"`
	PreviousCheckpoint    *RunLaunchCheckpointState     `json:"previousCheckpoint,omitempty"`
	ExecutorPolicy        RunLaunchExecutorPolicyState  `json:"executorPolicy"`
	LLMGateway            *RunLaunchLLMGatewayState     `json:"llmGateway,omitempty"`
	LarkEgress            *RunLaunchLarkEgressState     `json:"larkEgress,omitempty"`
	ManagedSandbox        *RunLaunchManagedSandboxState `json:"managedSandbox,omitempty"`
}
