package coreserver

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/runcursor"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

const (
	userRunWorkspaceID = "10000000-0000-4000-8000-000000000001"
	userRunSessionID   = "20000000-0000-4000-8000-000000000002"
	userRunID          = "30000000-0000-4000-8000-000000000003"
	userRunActorID     = "40000000-0000-4000-8000-000000000004"
)

type recordingUserRunStore struct {
	session   coredb.AuthorizedSession
	created   coredb.CreateRunResult
	cancelled coredb.CancelRunResult
	pages     []coredb.ReadAuthorizedRunEventsResult
	err       error

	authorizeCalls int
	create         coredb.CreateRunCommand
	cancel         coredb.CancelRunCommand
	reads          []coredb.ReadAuthorizedRunEventsCommand
}

func (store *recordingUserRunStore) AuthorizeRunSession(context.Context, string, string, string) (coredb.AuthorizedSession, error) {
	store.authorizeCalls++
	return store.session, store.err
}

func (store *recordingUserRunStore) CreateAuthorizedRun(_ context.Context, command coredb.CreateRunCommand) (coredb.CreateRunResult, error) {
	store.create = command
	return store.created, store.err
}

func (store *recordingUserRunStore) CancelRun(_ context.Context, command coredb.CancelRunCommand) (coredb.CancelRunResult, error) {
	store.cancel = command
	return store.cancelled, store.err
}

func (store *recordingUserRunStore) ReadAuthorizedRunEvents(_ context.Context, command coredb.ReadAuthorizedRunEventsCommand) (coredb.ReadAuthorizedRunEventsResult, error) {
	store.reads = append(store.reads, command)
	if store.err != nil {
		return coredb.ReadAuthorizedRunEventsResult{}, store.err
	}
	if len(store.pages) == 0 {
		return coredb.ReadAuthorizedRunEventsResult{}, errors.New("no fake event page")
	}
	page := store.pages[0]
	store.pages = store.pages[1:]
	return page, nil
}

type recordingPromptStore struct {
	pointer coredb.ObjectPointer
	request UserPromptWriteRequest
}

func (store *recordingPromptStore) PutUserPrompt(_ context.Context, request UserPromptWriteRequest) (coredb.ObjectPointer, error) {
	store.request = request
	return store.pointer, nil
}

type fixedRunPolicy struct{ policy coredb.RunExecutorPolicy }

func (resolver fixedRunPolicy) ResolveUserRunPolicy(context.Context, coredb.AuthorizedSession) (coredb.RunExecutorPolicy, error) {
	return resolver.policy, nil
}

func TestUserRunServiceCreatesAtomicAuthorizedRunAndInitialCursor(t *testing.T) {
	createdAt := time.Date(2026, 7, 31, 15, 0, 0, 0, time.UTC)
	store := &recordingUserRunStore{
		session: coredb.AuthorizedSession{WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID, Role: "developer", SessionVersion: 9},
		created: coredb.CreateRunResult{Created: true, Run: coredb.Run{
			ID: userRunID, WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID,
			Status: coredb.RunStatusQueued, NextEventSeq: 2, Version: 1, CreatedAt: createdAt,
		}},
	}
	prompt := &recordingPromptStore{pointer: coredb.ObjectPointer{
		ObjectID: "50000000-0000-4000-8000-000000000005", SHA256: sha256.Sum256([]byte("hello")),
		Size: 5, MediaType: "text/plain; charset=utf-8",
	}}
	codec, _ := runcursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	identities := []string{
		userRunID,
		"60000000-0000-4000-8000-000000000006",
		"70000000-0000-4000-8000-000000000007",
		"80000000-0000-4000-8000-000000000008",
	}
	service, err := NewUserRunService(UserRunServiceConfig{
		Store: store, Prompts: prompt,
		Policies:    fixedRunPolicy{coredb.RunExecutorPolicy{Version: "policy/1", ContextDigest: sha256.Sum256([]byte("policy"))}},
		CursorCodec: codec, PollInterval: 10 * time.Millisecond,
		NewID: func() (string, error) {
			value := identities[0]
			identities = identities[1:]
			return value, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.CreateUserRun(t.Context(), CreateUserRunCommand{
		ActorID: userRunActorID, WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID,
		IdempotencyKey: "request-1", ClientRunID: "client-run-1", Prompt: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Created || response.RunID != userRunID || response.CreatedAt != createdAt || response.LastEventSequence != 1 {
		t.Fatalf("CreateUserRun() = %+v", response)
	}
	if store.authorizeCalls != 1 || store.create.ExpectedSessionVersion != 0 || store.create.Prompt != prompt.pointer || store.create.Record.ProducerSeq != 1 {
		t.Fatalf("database CreateAuthorizedRun command = %+v", store.create)
	}
	if prompt.request.Prompt != "hello" || prompt.request.RequestHash != store.create.RequestHash || prompt.request.RequestHash == ([32]byte{}) {
		t.Fatalf("prompt write request = %+v", prompt.request)
	}
	sequence, err := codec.Decode(response.Cursor, runcursor.Scope{WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, RunID: userRunID})
	if err != nil || sequence != 1 {
		t.Fatalf("initial cursor = %d, %v", sequence, err)
	}
}

func TestUserRunServiceCancelsThroughTransactionalMembershipBoundary(t *testing.T) {
	store := &recordingUserRunStore{cancelled: coredb.CancelRunResult{
		Run: coredb.Run{
			ID: userRunID, WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID,
			ActorID: userRunActorID, Status: coredb.RunStatusCancelling, Version: 4,
		},
		SessionVersion: 9, Changed: true,
	}}
	identities := []string{
		"61000000-0000-4000-8000-000000000006",
		"71000000-0000-4000-8000-000000000007",
		"81000000-0000-4000-8000-000000000008",
	}
	codec, _ := runcursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	service, err := NewUserRunService(UserRunServiceConfig{
		Store: store, Prompts: &recordingPromptStore{}, Policies: fixedRunPolicy{}, CursorCodec: codec,
		PollInterval: 10 * time.Millisecond,
		NewID: func() (string, error) {
			value := identities[0]
			identities = identities[1:]
			return value, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.CancelUserRun(t.Context(), CancelUserRunCommand{
		ActorID: userRunActorID, WorkspaceID: userRunWorkspaceID, RunID: userRunID,
	})
	if err != nil || response.Status != coredb.RunStatusCancelling || response.Terminal || !response.Changed || response.RunVersion != 4 {
		t.Fatalf("CancelUserRun() = %+v, %v", response, err)
	}
	if store.authorizeCalls != 0 || store.cancel.ActorID != userRunActorID ||
		store.cancel.WorkspaceID != userRunWorkspaceID || store.cancel.RunID != userRunID ||
		store.cancel.Record.EventID != "61000000-0000-4000-8000-000000000006" ||
		store.cancel.Record.ProducerInstanceID != "71000000-0000-4000-8000-000000000007" ||
		store.cancel.Record.ProducerSeq != 1 || store.cancel.Record.OutboxID != "81000000-0000-4000-8000-000000000008" {
		t.Fatalf("transactional cancel command = %+v, authorize calls = %d", store.cancel, store.authorizeCalls)
	}
}

func TestUserRunServiceRejectsImpossibleCancelStoreResult(t *testing.T) {
	store := &recordingUserRunStore{cancelled: coredb.CancelRunResult{
		Run: coredb.Run{
			ID: userRunID, WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID,
			ActorID: userRunActorID, Status: coredb.RunStatusRunning, Version: 4,
		},
		SessionVersion: 9, Changed: true,
	}}
	codec, _ := runcursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	identities := []string{
		"62000000-0000-4000-8000-000000000006",
		"72000000-0000-4000-8000-000000000007",
		"82000000-0000-4000-8000-000000000008",
	}
	service, err := NewUserRunService(UserRunServiceConfig{
		Store: store, Prompts: &recordingPromptStore{}, Policies: fixedRunPolicy{}, CursorCodec: codec,
		PollInterval: 10 * time.Millisecond,
		NewID: func() (string, error) {
			value := identities[0]
			identities = identities[1:]
			return value, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelUserRun(t.Context(), CancelUserRunCommand{
		ActorID: userRunActorID, WorkspaceID: userRunWorkspaceID, RunID: userRunID,
	}); err == nil || !strings.Contains(err.Error(), "invalid cancelled run scope") {
		t.Fatalf("impossible cancel result error = %v", err)
	}
}

func TestUserRunServiceLongPollsCommittedEventsAndAdvancesCursor(t *testing.T) {
	codec, _ := runcursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	scope := runcursor.Scope{WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, RunID: userRunID}
	after, _ := codec.Encode(scope, 1)
	run := coredb.Run{ID: userRunID, WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID, Status: coredb.RunStatusRunning, NextEventSeq: 2, Version: 2}
	store := &recordingUserRunStore{pages: []coredb.ReadAuthorizedRunEventsResult{
		{Run: run, Events: []coredb.RunEvent{}, EarliestSequence: 1, LastSequence: 1},
		{Run: func() coredb.Run {
			value := run
			value.Status = coredb.RunStatusCompleted
			value.NextEventSeq = 3
			return value
		}(), Events: []coredb.RunEvent{{
			EventID: "90000000-0000-4000-8000-000000000009", Seq: 2,
			RunAttemptID: stringPointer("a0000000-0000-4000-8000-00000000000a"), RunAttemptGeneration: int64Pointer(1),
			ProducerInstanceID: "b0000000-0000-4000-8000-00000000000b", ProducerSeq: 1,
			Source: coredb.EventSourceSystem, Kind: runevent.KindRunCompleted, SchemaVersion: 1,
			Payload: []byte(`{}`), CreatedAt: time.Date(2026, 7, 31, 15, 1, 0, 0, time.UTC),
		}}, EarliestSequence: 1, LastSequence: 2},
	}}
	service := newReadUserRunService(t, store, codec)
	response, err := service.ReadUserRunEvents(t.Context(), ReadUserRunEventsQuery{
		ActorID: userRunActorID, WorkspaceID: userRunWorkspaceID, RunID: userRunID,
		After: after, Limit: 128, Wait: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Events) != 1 || len(response.EventCursors) != 1 || response.Events[0].Kind != runevent.KindRunCompleted || len(store.reads) != 2 {
		t.Fatalf("ReadUserRunEvents() = %+v, reads = %+v", response, store.reads)
	}
	sequence, err := codec.Decode(response.NextCursor, scope)
	if err != nil || sequence != 2 || response.LastEventSequence != 2 {
		t.Fatalf("next cursor = %d, %v; response = %+v", sequence, err, response)
	}
}

func TestUserRunServiceResolvesCursorWithoutConsumingEvents(t *testing.T) {
	codec, _ := runcursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	scope := runcursor.Scope{WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, RunID: userRunID}
	after, _ := codec.Encode(scope, 4)
	store := &recordingUserRunStore{pages: []coredb.ReadAuthorizedRunEventsResult{{
		Run:    coredb.Run{ID: userRunID, WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID, Status: coredb.RunStatusRunning, NextEventSeq: 6, Version: 3},
		Events: []coredb.RunEvent{{Seq: 5}}, EarliestSequence: 1, LastSequence: 5,
	}}}
	service := newReadUserRunService(t, store, codec)
	response, err := service.ReadUserRunEvents(t.Context(), ReadUserRunEventsQuery{
		ActorID: userRunActorID, WorkspaceID: userRunWorkspaceID, RunID: userRunID,
		After: after, Limit: 0, Wait: 30 * time.Second,
	})
	if err != nil || len(response.Events) != 0 || len(response.EventCursors) != 0 || response.LastEventSequence != 4 || len(store.reads) != 1 || store.reads[0].Limit != 1 {
		t.Fatalf("resolved response = %+v, error = %v, reads = %+v", response, err, store.reads)
	}
	sequence, err := codec.Decode(response.NextCursor, scope)
	if err != nil || sequence != 4 {
		t.Fatalf("resolved cursor = %d, %v", sequence, err)
	}
}

func TestUserRunServiceReturnsOnlyCommittedLifecycleRebase(t *testing.T) {
	codec, _ := runcursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	scope := runcursor.Scope{WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, RunID: userRunID}
	after, _ := codec.Encode(scope, 1)
	snapshotTime := time.Date(2026, 7, 31, 15, 2, 0, 0, time.UTC)
	store := &recordingUserRunStore{pages: []coredb.ReadAuthorizedRunEventsResult{{
		Run:              coredb.Run{ID: userRunID, WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID, Status: coredb.RunStatusRunning, NextEventSeq: 7, Version: 4, UpdatedAt: time.Now()},
		EarliestSequence: 5, LastSequence: 6,
		Rebase: &coredb.RunEventRebase{
			AfterSequence: 4, RunStatus: coredb.RunStatusStarting, RunVersion: 2, RunUpdatedAt: snapshotTime,
			Snapshot: []byte(`{"messages":[]}`), CreatedAt: time.Now(),
		},
	}}}
	service := newReadUserRunService(t, store, codec)
	_, err := service.ReadUserRunEvents(t.Context(), ReadUserRunEventsQuery{
		ActorID: userRunActorID, WorkspaceID: userRunWorkspaceID, RunID: userRunID, After: after, Limit: 10,
	})
	var expired *UserRunCursorExpiredError
	if !errors.As(err, &expired) || expired.Response.Code != "cursor_expired" || string(expired.Response.Snapshot.State) != `{"messages":[]}` {
		t.Fatalf("cursor error = %#v", err)
	}
	if expired.Response.Snapshot.Status != coredb.RunStatusStarting || expired.Response.Snapshot.RunVersion != 2 ||
		expired.Response.Snapshot.LastEventSequence != 4 || !expired.Response.Snapshot.UpdatedAt.Equal(snapshotTime) {
		t.Fatalf("snapshot metadata = %+v", expired.Response.Snapshot)
	}
	sequence, decodeErr := codec.Decode(expired.Response.RebaseCursor, scope)
	if decodeErr != nil || sequence != 4 || expired.Response.LastEventSequence != 4 {
		t.Fatalf("rebase cursor = %d, %v; response = %+v", sequence, decodeErr, expired.Response)
	}
}

func TestUserRunServiceRejectsForgedOrCrossScopeCursorBeforeDatabase(t *testing.T) {
	codec, _ := runcursor.NewCodec([]byte("0123456789abcdef0123456789abcdef"))
	other, _ := codec.Encode(runcursor.Scope{WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, RunID: "30000000-0000-4000-8000-000000000099"}, 1)
	store := &recordingUserRunStore{}
	service := newReadUserRunService(t, store, codec)
	replacement := "A"
	if other[len(other)-1:] == replacement {
		replacement = "B"
	}
	for _, cursor := range []string{other, other[:len(other)-1] + replacement} {
		if _, err := service.ReadUserRunEvents(t.Context(), ReadUserRunEventsQuery{
			ActorID: userRunActorID, WorkspaceID: userRunWorkspaceID, RunID: userRunID, After: cursor, Limit: 10,
		}); !coredb.HasStateErrorCode(err, coredb.ErrorInvalidArgument) {
			t.Fatalf("cursor error = %v", err)
		}
	}
	if len(store.reads) != 0 {
		t.Fatalf("database was reached for invalid cursor: %+v", store.reads)
	}
}

func newReadUserRunService(t *testing.T, store *recordingUserRunStore, codec *runcursor.Codec) *UserRunService {
	t.Helper()
	service, err := NewUserRunService(UserRunServiceConfig{
		Store: store, Prompts: &recordingPromptStore{}, Policies: fixedRunPolicy{}, CursorCodec: codec,
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func stringPointer(value string) *string { return &value }
func int64Pointer(value int64) *int64    { return &value }
