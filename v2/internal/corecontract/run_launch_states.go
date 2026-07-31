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
	ThreadID                   string                 `json:"threadId"`
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

type ResolveRunLaunchStateResponse struct {
	WorkspaceID          string                       `json:"workspaceId"`
	SessionID            string                       `json:"sessionId"`
	RunID                string                       `json:"runId"`
	RunAttemptID         string                       `json:"runAttemptId"`
	HolderID             string                       `json:"holderId"`
	RunAttemptGeneration int64                        `json:"runAttemptGeneration"`
	RunVersion           int64                        `json:"runVersion"`
	RunAttemptVersion    int64                        `json:"runAttemptVersion"`
	Prompt               RunLaunchObjectPointer       `json:"prompt"`
	PreviousCheckpoint   *RunLaunchCheckpointState    `json:"previousCheckpoint,omitempty"`
	ExecutorPolicy       RunLaunchExecutorPolicyState `json:"executorPolicy"`
}
