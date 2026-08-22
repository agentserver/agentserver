package coredb

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestValidateAppendAttemptEventsBounds(t *testing.T) {
	valid := AppendAttemptEventsCommand{
		RunID:      stateTestUUID(1000),
		AttemptID:  stateTestUUID(1001),
		HolderID:   "holder",
		Generation: 1,
		OutboxID:   stateTestUUID(1002),
		Events: []AttemptEvent{{
			EventID:            stateTestUUID(1003),
			ProducerInstanceID: stateTestUUID(1004),
			ProducerSeq:        1,
			Source:             EventSourceBrain,
			Kind:               "model.item",
			SchemaVersion:      1,
			Payload:            []byte(`{"ok":true}`),
		}},
	}
	if err := validateAppendAttemptEvents(valid); err != nil {
		t.Fatalf("valid command error = %v", err)
	}

	tests := []struct {
		name      string
		mutate    func(*AppendAttemptEventsCommand)
		wantError string
	}{
		{
			name: "mixed producers",
			mutate: func(command *AppendAttemptEventsCommand) {
				second := command.Events[0]
				second.EventID = stateTestUUID(1010)
				second.ProducerInstanceID = stateTestUUID(1011)
				second.ProducerSeq = 2
				command.Events = append(command.Events, second)
			},
			wantError: "same producer_instance_id",
		},
		{
			name: "non-monotonic producer sequence",
			mutate: func(command *AppendAttemptEventsCommand) {
				second := command.Events[0]
				second.EventID = stateTestUUID(1012)
				command.Events = append(command.Events, second)
			},
			wantError: "strictly increasing",
		},
		{
			name: "payload too large",
			mutate: func(command *AppendAttemptEventsCommand) {
				command.Events[0].Payload = append([]byte(`{"value":"`), bytes.Repeat([]byte("x"), MaxInlineEventPayloadBytes)...)
				command.Events[0].Payload = append(command.Events[0].Payload, []byte(`"}`)...)
			},
			wantError: "exceeds",
		},
		{
			name: "payload is not object",
			mutate: func(command *AppendAttemptEventsCommand) {
				command.Events[0].Payload = []byte(`[]`)
			},
			wantError: "JSON object",
		},
		{
			name: "payload and object",
			mutate: func(command *AppendAttemptEventsCommand) {
				command.Events[0].Object = &ObjectPointer{
					ObjectID: stateTestUUID(1013), Size: 1, MediaType: "text/plain",
				}
			},
			wantError: "exactly one",
		},
		{
			name: "invalid source",
			mutate: func(command *AppendAttemptEventsCommand) {
				command.Events[0].Source = "telemetry"
			},
			wantError: "source",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := valid
			command.Events = append([]AttemptEvent(nil), valid.Events...)
			test.mutate(&command)
			err := validateAppendAttemptEvents(command)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("validateAppendAttemptEvents() error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestLeaseAndOutboxBounds(t *testing.T) {
	if _, err := durationMilliseconds("lease", time.Millisecond, MaxLeaseTTL); err != nil {
		t.Fatalf("minimum duration error = %v", err)
	}
	if _, err := durationMilliseconds("lease", MaxLeaseTTL, MaxLeaseTTL); err != nil {
		t.Fatalf("maximum duration error = %v", err)
	}
	if _, err := durationMilliseconds("lease", time.Microsecond, MaxLeaseTTL); err == nil {
		t.Fatal("sub-millisecond duration accepted")
	}
	if _, err := durationMilliseconds("lease", MaxLeaseTTL+time.Millisecond, MaxLeaseTTL); err == nil {
		t.Fatal("over-maximum duration accepted")
	}
	if _, err := validateClaimOutbox(ClaimOutboxCommand{Owner: "relay", Limit: MaxOutboxClaimBatch, LockTTL: time.Minute}); err != nil {
		t.Fatalf("maximum outbox claim error = %v", err)
	}
	if _, err := validateClaimOutbox(ClaimOutboxCommand{Owner: "relay", Limit: MaxOutboxClaimBatch + 1, LockTTL: time.Minute}); err == nil {
		t.Fatal("oversized outbox claim accepted")
	}
	if _, err := validateClaimRunDispatches(ClaimRunDispatchesCommand{Owner: "pool", Limit: MaxOutboxClaimBatch, LockTTL: time.Minute}); err != nil {
		t.Fatalf("maximum run dispatch claim error = %v", err)
	}
	if _, err := validateClaimRunDispatches(ClaimRunDispatchesCommand{Owner: "pool", Limit: 0, LockTTL: time.Minute}); err == nil {
		t.Fatal("empty run dispatch claim accepted")
	}
	validRenewal := RenewRunAttemptLeasesCommand{
		SessionID: stateTestUUID(1200), RunID: stateTestUUID(1201), AttemptID: stateTestUUID(1202),
		HolderID: "holder", Generation: 1, LeaseTTL: time.Minute,
	}
	if _, err := validateRenewRunAttemptLeases(validRenewal); err != nil {
		t.Fatalf("valid atomic lease renewal error = %v", err)
	}
	validRenewal.SessionID = ""
	if _, err := validateRenewRunAttemptLeases(validRenewal); err == nil {
		t.Fatal("atomic lease renewal accepted an empty session identity")
	}
}

func TestDecodeQueuedRunDispatchPayloadIsStrict(t *testing.T) {
	payload := fmt.Sprintf(`{"workspaceId":%q,"sessionId":%q,"runId":%q,"runVersion":1}`,
		stateTestUUID(1300), stateTestUUID(1301), stateTestUUID(1302))
	decoded, err := decodeQueuedRunDispatchPayload([]byte(payload))
	if err != nil || decoded.RunVersion != 1 {
		t.Fatalf("decodeQueuedRunDispatchPayload() = %+v, %v", decoded, err)
	}
	if _, err := decodeQueuedRunDispatchPayload([]byte(payload[:len(payload)-1] + `,"unexpected":true}`)); err == nil {
		t.Fatal("run dispatch payload with an unknown field was accepted")
	}
}

func TestRunDispatchCompletionStatesFailClosed(t *testing.T) {
	for _, status := range []string{RunStatusQueued, RunStatusStarting, "future_state", ""} {
		if runDispatchCanComplete(status) {
			t.Errorf("runDispatchCanComplete(%q) = true", status)
		}
	}
	for _, status := range []string{
		RunStatusRunning, RunStatusFinalizing, RunStatusCompleted, RunStatusFailed,
		RunStatusInterrupted, RunStatusCancelling, RunStatusCancelled,
	} {
		if !runDispatchCanComplete(status) {
			t.Errorf("runDispatchCanComplete(%q) = false", status)
		}
	}
}

func TestRunLaunchAuthorityValidationAndPolicyNormalization(t *testing.T) {
	policyDigest := sha256.Sum256([]byte("run-launch-policy"))
	policy, err := normalizeRunExecutorPolicy(RunExecutorPolicy{
		Version: "executor-policy/1", ContextDigest: policyDigest,
		AllowedTools: []string{"shell", "read_file", "list_environments"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(policy.AllowedTools, []string{"list_environments", "read_file", "shell"}) {
		t.Fatalf("normalized allowed tools = %v", policy.AllowedTools)
	}
	if _, err := normalizeRunExecutorPolicy(RunExecutorPolicy{
		Version: "executor-policy/1", ContextDigest: policyDigest,
		AllowedTools: []string{"shell", "shell"},
	}); err == nil || !strings.Contains(err.Error(), "sorted and unique") {
		t.Fatalf("duplicate allowed tool error = %v", err)
	}

	command := ResolveRunLaunchStateCommand{
		WorkspaceID: stateTestUUID(1400), SessionID: stateTestUUID(1401), RunID: stateTestUUID(1402),
		AttemptID: stateTestUUID(1403), HolderID: "pool-holder", Generation: 2,
		ExpectedRunVersion: 3, ExpectedAttemptVersion: 1,
	}
	if err := validateResolveRunLaunchState(command); err != nil {
		t.Fatalf("valid resolve launch state command: %v", err)
	}
	command.Generation = 1 << 53
	if err := validateResolveRunLaunchState(command); err == nil || !strings.Contains(err.Error(), "safe integer") {
		t.Fatalf("unsafe generation error = %v", err)
	}
}

func TestRunPermissionModeIdempotencyAuthorityFailsClosedAcrossLegacyMarker(t *testing.T) {
	incomplete := stateCreateRunCommand(1350, stateTestUUID(1351), stateTestUUID(1352), "permission-mode-incomplete")
	incomplete.PermissionMode = runmanifest.CodexPermissionModeReadOnly
	if err := validateCreateRun(incomplete); err == nil || !strings.Contains(err.Error(), "must be positive when permission_mode is set") {
		t.Fatalf("explicit permission mode without version error = %v", err)
	}
	versionOnly := incomplete
	versionOnly.PermissionMode = ""
	versionOnly.ExpectedPermissionModeVersion = 1
	if err := validateCreateRun(versionOnly); err != nil {
		t.Fatalf("expected version without caller-selected mode was rejected: %v", err)
	}

	explicit := CreateRunCommand{
		PermissionMode:                runmanifest.CodexPermissionModeReadOnly,
		ExpectedPermissionModeVersion: 3,
	}
	if !runPermissionModeInputMatches(runmanifest.CodexPermissionModeReadOnly, 3, true, explicit) {
		t.Fatal("matching explicit permission authority was rejected")
	}
	if runPermissionModeInputMatches("", 0, false, explicit) {
		t.Fatal("explicit retry matched a legacy launch authority")
	}
	if runPermissionModeInputMatches(runmanifest.CodexPermissionModeAuto, 3, true, explicit) ||
		runPermissionModeInputMatches(runmanifest.CodexPermissionModeReadOnly, 4, true, explicit) {
		t.Fatal("explicit retry matched different permission authority")
	}

	// An omitted mode means the component caller delegated the selection to
	// Core. This is the backwards-compatible retry path used by pre-mode
	// callers; the committed run remains authoritative regardless of the
	// session's current preference.
	if !runPermissionModeInputMatches(runmanifest.CodexPermissionModeAuto, 9, true, CreateRunCommand{}) ||
		!runPermissionModeInputMatches("", 0, false, CreateRunCommand{}) {
		t.Fatal("server-resolved permission mode retry was rejected")
	}
}

func TestHasStateErrorCode(t *testing.T) {
	err := commandError(ErrorLeaseLost, "renew", "attempt", stateTestUUID(1100), "expired")
	if !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("HasStateErrorCode(%v) = false", err)
	}
	if HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("HasStateErrorCode(%v, invalid_state) = true", err)
	}
}
