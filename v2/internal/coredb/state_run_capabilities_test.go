package coredb

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestValidateResolveRunCapabilityIssuance(t *testing.T) {
	valid := validResolveRunCapabilityIssuanceCommand()
	if err := validateResolveRunCapabilityIssuance(valid); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*ResolveRunCapabilityIssuanceCommand)
		want   string
	}{
		{name: "workspace", mutate: func(command *ResolveRunCapabilityIssuanceCommand) { command.WorkspaceID = zeroUUID }, want: "workspace_id"},
		{name: "holder", mutate: func(command *ResolveRunCapabilityIssuanceCommand) { command.HolderID = "" }, want: "holder_id"},
		{name: "generation", mutate: func(command *ResolveRunCapabilityIssuanceCommand) { command.Generation = 0 }, want: "positive safe integers"},
		{name: "run version boundary", mutate: func(command *ResolveRunCapabilityIssuanceCommand) { command.ExpectedRunVersion = maxSafeJSONInteger }, want: "room for turn acceptance"},
		{name: "attempt version boundary", mutate: func(command *ResolveRunCapabilityIssuanceCommand) {
			command.ExpectedAttemptVersion = maxSafeJSONInteger
		}, want: "room for turn acceptance"},
		{name: "catalog digest", mutate: func(command *ResolveRunCapabilityIssuanceCommand) { command.ToolCatalogDigest = [sha256.Size]byte{} }, want: "tool_catalog_digest"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := valid
			test.mutate(&command)
			if err := validateResolveRunCapabilityIssuance(command); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateAuthorizeRunCapabilitySeparatesAudiences(t *testing.T) {
	executor := validAuthorizeRunCapabilityCommand()
	if err := validateAuthorizeRunCapability(executor); err != nil {
		t.Fatal(err)
	}
	model := executor
	model.Audience = RunCapabilityAudienceLLMProxy
	model.ExecutorID = ""
	model.ToolCatalogDigest = [sha256.Size]byte{}
	model.ExpectedRunVersion = 0
	model.ExpectedAttemptVersion = 0
	model.LLMGateway = validRunCapabilityLLMGatewayBinding()
	if err := validateAuthorizeRunCapability(model); err != nil {
		t.Fatal(err)
	}

	invalidModel := model
	invalidModel.ExecutorID = executor.ExecutorID
	if err := validateAuthorizeRunCapability(invalidModel); err == nil || !strings.Contains(err.Error(), "executor authority") {
		t.Fatalf("model executor-authority error = %v", err)
	}
	invalidExecutor := executor
	invalidExecutor.ExpectedRunVersion = 0
	if err := validateAuthorizeRunCapability(invalidExecutor); err == nil || !strings.Contains(err.Error(), "expected versions") {
		t.Fatalf("executor missing-version error = %v", err)
	}
	unsupported := executor
	unsupported.Audience = "future"
	if err := validateAuthorizeRunCapability(unsupported); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported audience error = %v", err)
	}
	invalidID := executor
	invalidID.CapabilityID = zeroUUID
	if err := validateAuthorizeRunCapability(invalidID); err == nil || !strings.Contains(err.Error(), "capability_id") {
		t.Fatalf("invalid capability error = %v", err)
	}
}

func validResolveRunCapabilityIssuanceCommand() ResolveRunCapabilityIssuanceCommand {
	return ResolveRunCapabilityIssuanceCommand{
		WorkspaceID: "62000000-0000-4000-8000-000000000001",
		SessionID:   "62000000-0000-4000-8000-000000000002",
		RunID:       "62000000-0000-4000-8000-000000000003",
		AttemptID:   "62000000-0000-4000-8000-000000000004",
		HolderID:    "pool/holder", Generation: 2, ExpectedRunVersion: 3, ExpectedAttemptVersion: 4,
		ExecutorID:         "62000000-0000-4000-8000-000000000005",
		BrainToolCatalogID: "62000000-0000-4000-8000-000000000006",
		ToolCatalogDigest:  sha256.Sum256([]byte("catalog")),
		LLMGateway:         validRunCapabilityLLMGatewayBinding(),
	}
}

func validRunCapabilityLLMGatewayBinding() RunLLMGatewayBinding {
	return RunLLMGatewayBinding{
		GatewayID: "62000000-0000-4000-8000-000000000009", ConfigVersion: 2,
		GrantUserID: "62000000-0000-4000-8000-000000000008", Model: "gpt-5.6-codex",
	}
}

func validAuthorizeRunCapabilityCommand() AuthorizeRunCapabilityCommand {
	issuance := validResolveRunCapabilityIssuanceCommand()
	return AuthorizeRunCapabilityCommand{
		Audience:     RunCapabilityAudienceExecutorMCP,
		CapabilityID: "62000000-0000-4000-8000-000000000007",
		WorkspaceID:  issuance.WorkspaceID, SessionID: issuance.SessionID,
		RunID: issuance.RunID, AttemptID: issuance.AttemptID,
		ActorID:  "62000000-0000-4000-8000-000000000008",
		HolderID: issuance.HolderID, Generation: issuance.Generation,
		ExecutorID: issuance.ExecutorID, ToolCatalogDigest: issuance.ToolCatalogDigest,
		ExpectedRunVersion: 4, ExpectedAttemptVersion: 5,
	}
}
