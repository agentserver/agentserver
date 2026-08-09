package coredb

import "testing"

func TestValidateObserveManagedSandboxAllowsAsynchronousCreating(t *testing.T) {
	command := ObserveManagedSandboxCommand{
		SandboxID: "90000000-0000-4000-8000-000000000001", Generation: 1, ExpectedVersion: 1,
		ObservedState: ManagedSandboxCreating, ProviderSessionRef: "tae-session-1",
	}
	if err := validateObserveManagedSandbox(command); err != nil {
		t.Fatalf("validate creating observation: %v", err)
	}
	command.ProviderSessionRef = ""
	if err := validateObserveManagedSandbox(command); err == nil {
		t.Fatal("creating observation without provider session reference was accepted")
	}
}

func TestManagedSandboxObservationAllowsReadyToFailedProviderLoss(t *testing.T) {
	state := ManagedSandbox{
		ID: "90000000-0000-4000-8000-000000000001", Generation: 1,
		DesiredState: ManagedSandboxDesiredReady, ObservedState: ManagedSandboxReady,
	}
	command := ObserveManagedSandboxCommand{
		SandboxID: state.ID, Generation: state.Generation, ExpectedVersion: 1,
		ObservedState: ManagedSandboxFailed, ErrorCode: "provider_deleted",
		ErrorDigest: func() *[32]byte { var digest [32]byte; digest[0] = 1; return &digest }(),
	}
	if err := validateObserveManagedSandbox(command); err != nil {
		t.Fatalf("validate failed observation: %v", err)
	}
	if err := validateManagedSandboxObservationTransition(state, command); err != nil {
		t.Fatalf("ready to failed transition: %v", err)
	}
}
