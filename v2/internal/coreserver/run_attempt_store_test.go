package coreserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestStateStoreRunAttemptCommandsMapCompleteControlBoundary(t *testing.T) {
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	store := &recordingRunAttemptStore{now: now}
	commands := StateStoreRunAttemptCommands{Store: store}
	record := corecontract.TransitionRecord{
		EventID:            "71000000-0000-4000-8000-000000000001",
		ProducerInstanceID: "72000000-0000-4000-8000-000000000001",
		ProducerSeq:        1,
		OutboxID:           "73000000-0000-4000-8000-000000000001",
	}

	claimed, err := commands.ClaimRunAttempt(t.Context(), corecontract.ClaimRunAttemptRequest{
		RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder",
		ExpectedRunVersion: 1, LeaseTTLMillis: 30_000, Record: record,
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.claim.LeaseTTL != 30*time.Second || claimed.Run.RunID != testRunID || claimed.RunAttempt.RunAttemptID != testRunAttemptID || !claimed.Created {
		t.Fatalf("claim store/response = %+v / %+v", store.claim, claimed)
	}

	renewed, err := commands.RenewRunAttempt(t.Context(), corecontract.RenewRunAttemptRequest{
		SessionID: "40000000-0000-4000-8000-000000000004", RunID: testRunID,
		RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3, LeaseTTLMillis: 45_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.renew.LeaseTTL != 45*time.Second || store.renew.SessionID == "" || renewed.SessionLease.Generation != 3 || renewed.AttemptLease.Generation != 3 {
		t.Fatalf("renew store/response = %+v / %+v", store.renew, renewed)
	}

	accepted, err := commands.MarkTurnAccepted(t.Context(), corecontract.MarkTurnAcceptedRequest{
		RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3,
		ExpectedRunVersion: 2, ExpectedRunAttemptVersion: 1, Record: record,
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.accept.ExpectedAttemptVersion != 1 || !accepted.Changed || accepted.Run.Status != coredb.RunStatusRunning || accepted.RunAttempt.Status != coredb.AttemptStatusRunning {
		t.Fatalf("turn accepted store/response = %+v / %+v", store.accept, accepted)
	}

	digestText := strings.Repeat("ab", 32)
	appended, err := commands.AppendAttemptEvents(t.Context(), corecontract.AppendAttemptEventsRequest{
		RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3,
		OutboxID: "73000000-0000-4000-8000-000000000002",
		Events: []corecontract.AttemptEvent{
			{
				EventID: "71000000-0000-4000-8000-000000000002", ProducerInstanceID: record.ProducerInstanceID,
				ProducerSeq: 2, Source: coredb.EventSourceBrain, Kind: "model.delta", SchemaVersion: 1,
				Payload: json.RawMessage(`{"text":"ok"}`),
			},
			{
				EventID: "71000000-0000-4000-8000-000000000003", ProducerInstanceID: record.ProducerInstanceID,
				ProducerSeq: 3, Source: coredb.EventSourceExecutor, Kind: "execution.output", SchemaVersion: 1,
				Object: &corecontract.EventObjectPointer{
					ObjectID: "74000000-0000-4000-8000-000000000001", SHA256: digestText, Size: 1024, MediaType: "application/octet-stream",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.append.Events) != 2 || string(store.append.Events[0].Payload) != `{"text":"ok"}` || store.append.Events[1].Object == nil ||
		store.append.Events[1].Object.SHA256[0] != 0xab || appended.NewCount != 2 || appended.Events[1].RunSeq != 11 {
		t.Fatalf("append store/response = %+v / %+v", store.append, appended)
	}
}

func TestStateStoreRunAttemptCommandsRejectInvalidTransportValuesBeforeStore(t *testing.T) {
	store := &recordingRunAttemptStore{}
	commands := StateStoreRunAttemptCommands{Store: store}
	if _, err := commands.ClaimRunAttempt(t.Context(), corecontract.ClaimRunAttemptRequest{RunID: testRunID, LeaseTTLMillis: coredb.MaxLeaseTTL.Milliseconds() + 1}); err == nil {
		t.Fatal("oversized lease TTL reached the store")
	}
	if store.claimCalls != 0 {
		t.Fatal("invalid claim called the store")
	}
	_, err := commands.AppendAttemptEvents(t.Context(), corecontract.AppendAttemptEventsRequest{
		Events: []corecontract.AttemptEvent{{Object: &corecontract.EventObjectPointer{SHA256: strings.Repeat("AB", 32)}}},
	})
	if err == nil || store.appendCalls != 0 {
		t.Fatalf("uppercase object digest error/calls = %v/%d", err, store.appendCalls)
	}
}

type recordingRunAttemptStore struct {
	now time.Time

	claim       coredb.ClaimQueuedRunCommand
	renew       coredb.RenewRunAttemptLeasesCommand
	accept      coredb.MarkTurnAcceptedCommand
	append      coredb.AppendAttemptEventsCommand
	claimCalls  int
	appendCalls int
}

func (store *recordingRunAttemptStore) ClaimQueuedRun(_ context.Context, command coredb.ClaimQueuedRunCommand) (coredb.ClaimQueuedRunResult, error) {
	store.claim = command
	store.claimCalls++
	return coredb.ClaimQueuedRunResult{
		Run: coredb.Run{
			ID: command.RunID, WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: "40000000-0000-4000-8000-000000000005",
			ActorID: "40000000-0000-4000-8000-000000000006", Status: coredb.RunStatusStarting, CurrentAttemptGeneration: 3,
			NextEventSeq: 3, Version: 2, CreatedAt: store.now, UpdatedAt: store.now,
		},
		Attempt: coredb.RunAttempt{
			ID: command.AttemptID, RunID: command.RunID, Generation: 3, Status: coredb.AttemptStatusLeased,
			HolderID: command.HolderID, Version: 1, CreatedAt: store.now, UpdatedAt: store.now,
		},
		SessionLease: coredb.Lease{HolderID: command.HolderID, Generation: 3, ExpiresAt: store.now.Add(command.LeaseTTL), AcquiredAt: store.now, RenewedAt: store.now},
		AttemptLease: coredb.Lease{HolderID: command.HolderID, Generation: 3, ExpiresAt: store.now.Add(command.LeaseTTL), AcquiredAt: store.now, RenewedAt: store.now},
		Created:      true,
	}, nil
}

func (store *recordingRunAttemptStore) RenewRunAttemptLeases(_ context.Context, command coredb.RenewRunAttemptLeasesCommand) (coredb.RenewRunAttemptLeasesResult, error) {
	store.renew = command
	lease := coredb.Lease{HolderID: command.HolderID, Generation: command.Generation, ExpiresAt: store.now.Add(command.LeaseTTL), AcquiredAt: store.now, RenewedAt: store.now}
	return coredb.RenewRunAttemptLeasesResult{SessionLease: lease, AttemptLease: lease}, nil
}

func (store *recordingRunAttemptStore) MarkTurnAccepted(_ context.Context, command coredb.MarkTurnAcceptedCommand) (coredb.MarkTurnAcceptedResult, error) {
	store.accept = command
	turnStarted := store.now
	return coredb.MarkTurnAcceptedResult{
		Run: coredb.Run{
			ID: command.RunID, WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: "40000000-0000-4000-8000-000000000005",
			ActorID: "40000000-0000-4000-8000-000000000006", Status: coredb.RunStatusRunning, CurrentAttemptGeneration: command.Generation,
			NextEventSeq: 4, Version: 3, CreatedAt: store.now, UpdatedAt: store.now,
		},
		Attempt: coredb.RunAttempt{
			ID: command.AttemptID, RunID: command.RunID, Generation: command.Generation, Status: coredb.AttemptStatusRunning,
			TurnStartedAt: &turnStarted, HolderID: command.HolderID, Version: 2, CreatedAt: store.now, UpdatedAt: store.now,
		},
		Changed: true,
	}, nil
}

func (store *recordingRunAttemptStore) AppendAttemptEvents(_ context.Context, command coredb.AppendAttemptEventsCommand) (coredb.AppendAttemptEventsResult, error) {
	store.append = command
	store.appendCalls++
	events := make([]coredb.AppendedEvent, len(command.Events))
	for index, event := range command.Events {
		events[index] = coredb.AppendedEvent{
			EventID: event.EventID, ProducerInstanceID: event.ProducerInstanceID, ProducerSeq: event.ProducerSeq,
			RunSeq: int64(10 + index), Duplicate: false,
		}
	}
	return coredb.AppendAttemptEventsResult{Events: events, NewCount: len(events)}, nil
}
