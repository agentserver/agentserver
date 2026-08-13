package coredb

import (
	"testing"
	"time"
)

func TestValidateWorkspaceCredentialUseEventAcceptsProcessEnvironmentStage(t *testing.T) {
	event := WorkspaceCredentialUseEvent{
		EventID:              "10000000-0000-4000-8000-000000000001",
		At:                   time.Now().UTC(),
		Stage:                "process_env",
		CapabilityID:         "20000000-0000-4000-8000-000000000002",
		WorkspaceID:          "30000000-0000-4000-8000-000000000003",
		SessionID:            "40000000-0000-4000-8000-000000000004",
		ActorID:              "50000000-0000-4000-8000-000000000005",
		EnvironmentID:        "60000000-0000-4000-8000-000000000006",
		RunID:                "70000000-0000-4000-8000-000000000007",
		RunAttemptID:         "80000000-0000-4000-8000-000000000008",
		RunAttemptGeneration: 1,
		ExecutionID:          "90000000-0000-4000-8000-000000000009",
		OperationID:          "a0000000-0000-4000-8000-00000000000a",
		SandboxID:            "b0000000-0000-4000-8000-00000000000b",
		TargetGeneration:     1,
		ProviderKind:         "lark",
		BindingID:            "c0000000-0000-4000-8000-00000000000c",
		AuthorityVersion:     1,
		CredentialVersion:    4,
		TAEPSM:               "bytedance.sandbox.agentserver",
		Host:                 "open.larkoffice.com",
		Path:                 "/",
		Method:               "PROCESS_ENV",
		Decision:             "allow",
		ReasonCode:           "allowed",
	}

	if err := validateWorkspaceCredentialUseEvent(event); err != nil {
		t.Fatalf("validate process_env credential use event: %v", err)
	}
}
