package coredb

import (
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

func TestValidateAcquireExecutorConnection(t *testing.T) {
	valid := validAcquireExecutorConnectionCommand()
	if milliseconds, err := validateAcquireExecutorConnection(valid); err != nil || milliseconds != 45_000 {
		t.Fatalf("validateAcquireExecutorConnection() = %d, %v", milliseconds, err)
	}
	filesystemRead := validAcquireExecutorConnectionCommand()
	filesystemRead.Environments[0].OuterProfileVersion = executorFilesystemReadProfileVersion
	if _, err := validateAcquireExecutorConnection(filesystemRead); err != nil {
		t.Fatalf("filesystem-read environment profile rejected: %v", err)
	}

	tests := []struct {
		name      string
		mutate    func(*AcquireExecutorConnectionCommand)
		wantError string
	}{
		{name: "zero executor", mutate: func(command *AcquireExecutorConnectionCommand) { command.ExecutorID = zeroUUID }, wantError: "executor_id"},
		{name: "zero runtime digest", mutate: func(command *AcquireExecutorConnectionCommand) { command.RuntimeManifestSHA256 = [32]byte{} }, wantError: "runtime_manifest_sha256"},
		{name: "no environments", mutate: func(command *AcquireExecutorConnectionCommand) { command.Environments = nil }, wantError: "environments"},
		{name: "duplicate environment", mutate: func(command *AcquireExecutorConnectionCommand) {
			command.Environments = append(command.Environments, command.Environments[0])
		}, wantError: "duplicate env_id"},
		{name: "wrong method order", mutate: func(command *AcquireExecutorConnectionCommand) {
			command.Environments[0].ProcessMethods[0], command.Environments[0].ProcessMethods[1] = command.Environments[0].ProcessMethods[1], command.Environments[0].ProcessMethods[0]
		}, wantError: "process_methods"},
		{name: "partial filesystem profile", mutate: func(command *AcquireExecutorConnectionCommand) {
			command.Environments[0].OuterProfileVersion = "filesystem-read-v1"
		}, wantError: "outer_profile_version"},
		{name: "oversize lease", mutate: func(command *AcquireExecutorConnectionCommand) {
			command.LeaseTTL = MaxExecutorConnectionTTL + time.Millisecond
		}, wantError: "lease_ttl"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := validAcquireExecutorConnectionCommand()
			test.mutate(&command)
			_, err := validateAcquireExecutorConnection(command)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validateAcquireExecutorConnection() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestValidateExecutorConnectionHolder(t *testing.T) {
	valid := RenewExecutorConnectionCommand{
		ExecutorID:        "10000000-0000-4000-8000-000000000001",
		SessionID:         "20000000-0000-4000-8000-000000000002",
		GatewayInstanceID: "30000000-0000-4000-8000-000000000003",
		Generation:        2,
		LeaseTTL:          45 * time.Second,
	}
	if _, err := validateRenewExecutorConnection(valid); err != nil {
		t.Fatalf("validateRenewExecutorConnection() error = %v", err)
	}
	valid.Generation = 0
	if _, err := validateRenewExecutorConnection(valid); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("zero generation error = %v", err)
	}
}

func TestEnvironmentDeclarationHashIsSetOrderedAndFieldSensitive(t *testing.T) {
	command := validAcquireExecutorConnectionCommand()
	second := command.Environments[0]
	second.ID = "60000000-0000-4000-8000-000000000006"
	command.Environments = append(command.Environments, second)
	forward := hashExecutorEnvironmentDeclarations(command.Environments)
	reversed := hashExecutorEnvironmentDeclarations([]ExecutorEnvironmentDeclaration{command.Environments[1], command.Environments[0]})
	if forward != reversed {
		t.Fatal("environment set hash depends on declaration order")
	}
	changed := cloneExecutorDeclarations(command.Environments)
	changed[0].InsecureDev = true
	if hashExecutorEnvironmentDeclarations(changed) == forward {
		t.Fatal("environment set hash ignored insecure_dev")
	}
	changed = cloneExecutorDeclarations(command.Environments)
	changed[0].OuterProfileVersion = executorFilesystemReadProfileVersion
	if hashExecutorEnvironmentDeclarations(changed) == forward {
		t.Fatal("environment set hash ignored outer_profile_version")
	}
}

func validAcquireExecutorConnectionCommand() AcquireExecutorConnectionCommand {
	return AcquireExecutorConnectionCommand{
		ExecutorID:               "10000000-0000-4000-8000-000000000001",
		ConnectionID:             "20000000-0000-4000-8000-000000000002",
		SessionID:                "30000000-0000-4000-8000-000000000003",
		GatewayInstanceID:        "40000000-0000-4000-8000-000000000004",
		AgentxVersion:            "0.1.0",
		RuntimeManifestSHA256:    sha256.Sum256([]byte("runtime-manifest")),
		ExecProtocolSourceSHA256: sha256.Sum256([]byte("exec-protocol")),
		LeaseTTL:                 45 * time.Second,
		Environments: []ExecutorEnvironmentDeclaration{{
			ID:                  "50000000-0000-4000-8000-000000000005",
			Platform:            "linux-arm64",
			CodexRelease:        "0.146.0",
			CodexCommit:         strings.Repeat("a", 40),
			CodexSHA256:         sha256.Sum256([]byte("codex")),
			OuterProfileVersion: "process-v1",
			ProcessMethods:      append([]string(nil), executorProcessMethods...),
		}},
	}
}
