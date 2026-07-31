package coredb

import (
	"bytes"
	"strings"
	"testing"
	"time"
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

func TestHasStateErrorCode(t *testing.T) {
	err := commandError(ErrorLeaseLost, "renew", "attempt", stateTestUUID(1100), "expired")
	if !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("HasStateErrorCode(%v) = false", err)
	}
	if HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("HasStateErrorCode(%v, invalid_state) = true", err)
	}
}
