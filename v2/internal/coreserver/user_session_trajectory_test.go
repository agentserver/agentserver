package coreserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

const (
	trajectoryWorkspaceID = "51000000-0000-4000-8000-000000000005"
	trajectorySessionID   = "61000000-0000-4000-8000-000000000006"
	trajectoryActorID     = "71000000-0000-4000-8000-000000000007"
	trajectoryRunID       = "81000000-0000-4000-8000-000000000008"
	trajectoryAttemptID   = "91000000-0000-4000-8000-000000000009"
	trajectoryExecutionID = "a1000000-0000-4000-8000-00000000000a"
	trajectoryOperationID = "b1000000-0000-4000-8000-00000000000b"
	trajectorySandboxID   = "c1000000-0000-4000-8000-00000000000c"
)

func TestProjectUserSessionTrajectoryConnectsManagedExecutionAndProcessEnvironment(t *testing.T) {
	now := time.Date(2026, 8, 15, 2, 0, 0, 0, time.UTC)
	terminal := now.Add(5 * time.Second)
	source := trajectoryTestSource(now, coredb.RunStatusCompleted)
	source.Events = []coredb.UserSessionTrajectoryEvent{
		trajectoryTestEvent(1, runevent.KindToolCallStarted, `{"toolCallId":"call-1","toolCallName":"shell"}`, now.Add(time.Second)),
		trajectoryTestEvent(2, runevent.KindToolCallArguments, `{"toolCallId":"call-1","delta":"{\"command\":\"lark-cli skills read lark-doc\",\"access_token\":\"must-not-leak\"}"}`, now.Add(2*time.Second)),
		trajectoryTestEvent(3, runevent.KindToolCallResult, `{"messageId":"tool-message-1","toolCallId":"call-1","content":"{\"ok\":true,\"refresh_token\":\"also-secret\"}"}`, now.Add(3*time.Second)),
		trajectoryTestEvent(4, runevent.KindToolCallCompleted, `{"toolCallId":"call-1"}`, now.Add(4*time.Second)),
		trajectoryTestEvent(5, runevent.KindRunCompleted, `{}`, terminal),
	}
	source.Executions = []coredb.Execution{{
		ID: trajectoryExecutionID, RunID: trajectoryRunID, RunAttemptID: trajectoryAttemptID,
		RunAttemptGeneration: 1, AppServerToolCallID: "call-1", ToolName: "shell", ToolVersion: "1",
		PolicyDecision: coredb.PolicyDecisionAllow, Target: coredb.DispatchTarget{Kind: coredb.DispatchTargetTAE, ID: trajectorySandboxID, Generation: 3},
		Status: coredb.ExecutionStatusSucceeded, TerminalAt: &terminal, CreatedAt: now.Add(time.Second), UpdatedAt: terminal,
	}}
	source.Operations = []coredb.ExecutionOperation{{
		ID: trajectoryOperationID, ExecutionID: trajectoryExecutionID, Ordinal: 0, Kind: "run_command", EffectClass: coredb.OperationEffectRead,
		Status: coredb.OperationStatusSucceeded, Target: coredb.DispatchTarget{Kind: coredb.DispatchTargetTAE, ID: trajectorySandboxID, Generation: 3},
		TerminalAt: &terminal, CreatedAt: now.Add(2 * time.Second), UpdatedAt: terminal,
	}}
	source.Sandboxes = []coredb.ManagedSandbox{{
		ID: trajectorySandboxID, WorkspaceID: trajectoryWorkspaceID, SessionID: trajectorySessionID,
		ProviderKind: "tae", ProviderRegion: "sg", ProviderPSM: "bytedance.sandbox.agentserver",
		Generation: 3, ObservedState: coredb.ManagedSandboxReady, LastObservedAt: &terminal,
		CreatedAt: now, UpdatedAt: terminal,
	}}
	source.Activities = []coredb.UserSessionTrajectorySandboxActivity{{
		SandboxID: trajectorySandboxID, TargetGeneration: 3, RunID: trajectoryRunID,
		RunAttemptID: trajectoryAttemptID, RunAttemptGeneration: 1, CreatedAt: now, UpdatedAt: terminal,
	}}
	source.CredentialUses = []coredb.UserSessionTrajectoryCredentialUse{{
		BindingDisplayName: "My Lark access_token=display-name-secret",
		Event: coredb.WorkspaceCredentialUseEvent{
			EventID: "d1000000-0000-4000-8000-00000000000d", At: now.Add(2500 * time.Millisecond), Stage: "process_env",
			WorkspaceID: trajectoryWorkspaceID, SessionID: trajectorySessionID, ActorID: trajectoryActorID,
			RunID: trajectoryRunID, RunAttemptID: trajectoryAttemptID, RunAttemptGeneration: 1,
			ExecutionID: trajectoryExecutionID, OperationID: trajectoryOperationID, SandboxID: trajectorySandboxID,
			TargetGeneration: 3, ProviderKind: "lark", BindingID: "e1000000-0000-4000-8000-00000000000e",
			CredentialVersion: 2, Decision: "allow", ReasonCode: "selected",
		},
	}}

	records, err := projectUserSessionTrajectory(source)
	if err != nil {
		t.Fatal(err)
	}
	byID := trajectoryRecordsByID(records)
	tool := byID["tool:"+trajectoryRunID+":call-1"]
	if tool.ParentID != "attempt:"+trajectoryAttemptID+":1" || tool.Status != "succeeded" ||
		strings.Contains(tool.Input+tool.Output, "must-not-leak") || strings.Contains(tool.Input+tool.Output, "also-secret") ||
		!strings.Contains(tool.Input, "<redacted>") || !strings.Contains(tool.Output, "<redacted>") {
		t.Fatalf("tool trajectory = %+v", tool)
	}
	execution := byID["execution:"+trajectoryExecutionID]
	operation := byID["operation:"+trajectoryOperationID]
	if execution.ParentID != tool.ID || operation.ParentID != execution.ID || operation.SandboxID != trajectorySandboxID {
		t.Fatalf("managed execution hierarchy = tool=%+v execution=%+v operation=%+v", tool, execution, operation)
	}
	credential := byID["credential:d1000000-0000-4000-8000-00000000000d"]
	if credential.ParentID != operation.ID || credential.Status != "succeeded" ||
		trajectoryDetail(credential, "mode") != "process_env" || trajectoryDetail(credential, "webhookUsed") != "false" ||
		trajectoryDetail(credential, "egressAuthorizerUsed") != "false" ||
		strings.Contains(trajectoryDetail(credential, "binding"), "display-name-secret") ||
		!strings.Contains(trajectoryDetail(credential, "binding"), "<redacted>") {
		t.Fatalf("process_env credential trajectory = %+v", credential)
	}
	sandbox := byID["sandbox:"+trajectorySandboxID+":"+trajectoryAttemptID]
	if sandbox.Status != "succeeded" || trajectoryDetail(sandbox, "region") != "sg" || trajectoryDetail(sandbox, "psm") != "bytedance.sandbox.agentserver" {
		t.Fatalf("sandbox trajectory = %+v", sandbox)
	}
}

func TestProjectUserSessionTrajectoryMarksObjectBackedEventAsTruncatedPlaceholder(t *testing.T) {
	now := time.Date(2026, 8, 15, 4, 0, 0, 0, time.UTC)
	source := trajectoryTestSource(now, coredb.RunStatusRunning)
	event := trajectoryTestEvent(1, runevent.KindToolCallResult, `{}`, now.Add(time.Second))
	event.Event.Payload = nil
	event.Event.Object = &coredb.ObjectPointer{
		ObjectID: "d1000000-0000-4000-8000-00000000000d",
		Size:     4096, MediaType: "application/json",
	}
	source.Events = []coredb.UserSessionTrajectoryEvent{event}

	records, err := projectUserSessionTrajectory(source)
	if err != nil {
		t.Fatal(err)
	}
	placeholder := trajectoryRecordsByID(records)["event:"+event.Event.EventID]
	if placeholder.Kind != "event" || placeholder.Status != "info" || !placeholder.OutputTruncated ||
		trajectoryDetail(placeholder, "payload") != "object" || trajectoryDetail(placeholder, "bytes") != "4096" ||
		!trajectorySourceHasObjectPayload(source.Events) {
		t.Fatalf("object-backed trajectory placeholder = %+v", placeholder)
	}
}

func TestProjectUserSessionTrajectoryMakesModelFailureReadableAndRedacted(t *testing.T) {
	now := time.Date(2026, 8, 15, 3, 0, 0, 0, time.UTC)
	source := trajectoryTestSource(now, coredb.RunStatusFailed)
	source.Attempts[0].Status = coredb.AttemptStatusFailed
	source.Runs[0].UpdatedAt = now.Add(time.Second)
	source.Events = []coredb.UserSessionTrajectoryEvent{
		trajectoryTestEvent(1, runevent.KindRunFailed,
			`{"code":"turn_failed","message":"category=model_overloaded message=Selected model is at capacity Authorization: Bearer must-not-leak stderr_sha256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
			now.Add(time.Second)),
	}
	records, err := projectUserSessionTrajectory(source)
	if err != nil {
		t.Fatal(err)
	}
	byID := trajectoryRecordsByID(records)
	run := byID["run:"+trajectoryRunID]
	model := byID["model:"+trajectoryRunID+":terminal"]
	if run.Failure == nil || run.Failure.Category != "model_overloaded" || model.Failure == nil ||
		!strings.Contains(model.Failure.Message, "Selected model is at capacity") || strings.Contains(model.Failure.Message, "must-not-leak") ||
		!strings.Contains(model.Failure.Message, "<redacted>") || model.Failure.Fingerprint != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("model failure trajectory = run=%+v model=%+v", run, model)
	}
}

func TestProjectUserSessionTrajectoryMarksUnfinishedToolAsOutputIncomplete(t *testing.T) {
	now := time.Date(2026, 8, 15, 3, 30, 0, 0, time.UTC)
	source := trajectoryTestSource(now, coredb.RunStatusFailed)
	source.Events = []coredb.UserSessionTrajectoryEvent{
		trajectoryTestEvent(1, runevent.KindToolCallStarted, `{"toolCallId":"call-incomplete","toolCallName":"shell"}`, now.Add(time.Second)),
		trajectoryTestEvent(2, runevent.KindToolCallArguments, `{"toolCallId":"call-incomplete","delta":"{\"command\":\"lark-cli skills read lark-doc references/lark-doc-fetch.md\"}"}`, now.Add(2*time.Second)),
		trajectoryTestEvent(3, runevent.KindRunFailed, `{"code":"turn_failed","message":"stock app-server ended before tool completion"}`, now.Add(3*time.Second)),
	}
	records, err := projectUserSessionTrajectory(source)
	if err != nil {
		t.Fatal(err)
	}
	tool := trajectoryRecordsByID(records)["tool:"+trajectoryRunID+":call-incomplete"]
	if tool.Status != "unknown" || tool.Failure == nil || tool.Failure.Category != "output_incomplete" ||
		tool.Failure.Component != "stock-app-server" || tool.Failure.Phase != "tool_result" || !tool.Failure.Retryable {
		t.Fatalf("incomplete tool trajectory = %+v", tool)
	}
}

func TestCompleteTrajectoryRecordClampsCrossComponentClockSkew(t *testing.T) {
	started := time.Date(2026, 8, 15, 2, 6, 3, 665502000, time.UTC)
	record := corecontract.UserSessionTrajectoryRecord{StartedAt: started}

	completeTrajectoryRecord(&record, started.Add(-7973*time.Microsecond))

	if record.CompletedAt == nil || !record.CompletedAt.Equal(started) ||
		record.DurationMillis == nil || *record.DurationMillis != 0 {
		t.Fatalf("clamped trajectory timing = completedAt=%v duration=%v, want %s/0", record.CompletedAt, record.DurationMillis, started)
	}
}

func TestProjectUserSessionTrajectoryTreatsLifecycleTransitionAsCompletedPoint(t *testing.T) {
	now := time.Date(2026, 8, 15, 2, 6, 10, 495001000, time.UTC)
	source := trajectoryTestSource(now.Add(-time.Second), coredb.RunStatusRunning)
	source.Events = []coredb.UserSessionTrajectoryEvent{
		trajectoryTestEvent(1, "run.finalizing", `{}`, now),
	}

	records, err := projectUserSessionTrajectory(source)
	if err != nil {
		t.Fatal(err)
	}
	record := trajectoryRecordsByID(records)["event:"+source.Events[0].Event.EventID]

	if record.Status != "info" || record.CompletedAt == nil || !record.CompletedAt.Equal(now) ||
		record.DurationMillis == nil || *record.DurationMillis != 0 {
		t.Fatalf("finalizing trajectory event = %+v, want completed informational point", record)
	}
}

func trajectoryTestSource(now time.Time, runStatus string) coredb.ReadUserSessionTrajectoryResult {
	return coredb.ReadUserSessionTrajectoryResult{
		Session: coredb.UserSession{ID: trajectorySessionID, WorkspaceID: trajectoryWorkspaceID, CreatorID: trajectoryActorID, Status: coredb.UserSessionStatusActive, CreatedAt: now, UpdatedAt: now},
		Runs: []coredb.Run{{
			ID: trajectoryRunID, WorkspaceID: trajectoryWorkspaceID, SessionID: trajectorySessionID, ActorID: trajectoryActorID,
			Status: runStatus, CurrentAttemptGeneration: 1, CreatedAt: now, UpdatedAt: now,
		}},
		Attempts: []coredb.RunAttempt{{
			ID: trajectoryAttemptID, RunID: trajectoryRunID, Generation: 1, Status: coredb.AttemptStatusSucceeded,
			CreatedAt: now, UpdatedAt: now,
		}},
	}
}

func trajectoryTestEvent(sequence int64, kind, payload string, createdAt time.Time) coredb.UserSessionTrajectoryEvent {
	generation := int64(1)
	return coredb.UserSessionTrajectoryEvent{
		RunID: trajectoryRunID,
		Event: coredb.RunEvent{
			EventID: "f1000000-0000-4000-8000-" + transcriptSequenceSuffix(sequence), Seq: sequence,
			RunAttemptID: stringPointer(trajectoryAttemptID), RunAttemptGeneration: &generation,
			ProducerInstanceID: "f2000000-0000-4000-8000-000000000002", ProducerSeq: sequence,
			Source: coredb.EventSourceBrain, Kind: kind, SchemaVersion: runevent.CurrentSchemaVersion,
			Payload: json.RawMessage(payload), CreatedAt: createdAt,
		},
	}
}

func trajectoryRecordsByID(records []projectedTrajectoryRecord) map[string]corecontract.UserSessionTrajectoryRecord {
	result := make(map[string]corecontract.UserSessionTrajectoryRecord, len(records))
	for _, record := range records {
		result[record.record.ID] = record.record
	}
	return result
}

func trajectoryDetail(record corecontract.UserSessionTrajectoryRecord, name string) string {
	for _, detail := range record.Details {
		if detail.Name == name {
			return detail.Value
		}
	}
	return ""
}
