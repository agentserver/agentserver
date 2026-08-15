package coreserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/runevent"
	"github.com/agentserver/agentserver/v2/internal/safediagnostic"
	"github.com/agentserver/agentserver/v2/internal/trajectorycursor"
)

const (
	defaultUserTrajectoryLimit = 100
	maximumUserTrajectoryLimit = 200
	trajectoryTextPreviewBytes = 16 * 1024
	trajectoryDetailBytes      = 1024
)

const (
	trajectoryRankRun = iota * 10
	trajectoryRankAttempt
	trajectoryRankModel
	trajectoryRankAssistant
	trajectoryRankTool
	trajectoryRankApproval
	trajectoryRankExecution
	trajectoryRankOperation
	trajectoryRankSandbox
	trajectoryRankCredential
	trajectoryRankCheckpoint
	trajectoryRankEvent
)

type projectedTrajectoryRecord struct {
	record       corecontract.UserSessionTrajectoryRecord
	runCreatedAt time.Time
	anchorSeq    int64
	rank         int
}

type trajectoryMessageBuilder struct {
	record    projectedTrajectoryRecord
	contents  []byte
	truncated bool
	completed bool
}

type trajectoryToolBuilder struct {
	record          projectedTrajectoryRecord
	arguments       []byte
	result          []byte
	inputTruncated  bool
	outputTruncated bool
	completed       bool
}

type trajectoryTerminal struct {
	sequence int64
	event    runevent.Event
	payload  runevent.RunTerminalPayload
}

func (commands StateStoreUserSessionCommands) GetTrajectory(
	ctx context.Context,
	workspaceID, sessionID, actorID, before string,
	limit int,
) (corecontract.GetUserSessionTrajectoryResponse, error) {
	if commands.Store == nil || commands.TrajectoryCursors == nil {
		return corecontract.GetUserSessionTrajectoryResponse{}, errors.New("user trajectory state and cursor authority are required")
	}
	if limit == 0 {
		limit = defaultUserTrajectoryLimit
	}
	if limit < 1 || limit > maximumUserTrajectoryLimit {
		return corecontract.GetUserSessionTrajectoryResponse{}, &coredb.StateError{
			Code: coredb.ErrorInvalidArgument, Operation: "GetUserSessionTrajectory",
			Resource: "session", ResourceID: sessionID, Message: "trajectory limit must be between 1 and 200",
		}
	}
	scope := trajectorycursor.Scope{WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID}
	var position *trajectorycursor.Position
	if before != "" {
		decoded, err := commands.TrajectoryCursors.Decode(before, scope)
		if err != nil {
			return corecontract.GetUserSessionTrajectoryResponse{}, &coredb.StateError{
				Code: coredb.ErrorInvalidArgument, Operation: "GetUserSessionTrajectory",
				Resource: "session", ResourceID: sessionID, Message: "invalid trajectory cursor",
			}
		}
		position = &decoded
	}
	query := coredb.ReadUserSessionTrajectoryQuery{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
	}
	if position != nil {
		query.Before = &coredb.UserSessionTrajectoryRunPosition{
			RunID: position.RunID, RunCreatedAt: position.RunCreatedAt,
		}
	}
	source, err := commands.Store.ReadUserSessionTrajectory(ctx, query)
	if err != nil {
		return corecontract.GetUserSessionTrajectoryResponse{}, err
	}
	if position != nil && !trajectorySourceContainsPositionRun(source.Runs, *position) {
		return corecontract.GetUserSessionTrajectoryResponse{}, &coredb.StateError{
			Code: coredb.ErrorInvalidArgument, Operation: "GetUserSessionTrajectory",
			Resource: "session", ResourceID: sessionID, Message: "invalid trajectory cursor",
		}
	}
	projected, err := projectUserSessionTrajectory(source)
	if err != nil {
		return corecontract.GetUserSessionTrajectoryResponse{}, err
	}
	if position != nil {
		filtered := projected[:0]
		for _, candidate := range projected {
			if compareTrajectoryPosition(candidate, *position) < 0 {
				filtered = append(filtered, candidate)
			}
		}
		projected = filtered
	}
	hasMore := source.HasOlderRuns
	if len(projected) > limit {
		hasMore = true
		projected = projected[len(projected)-limit:]
	}
	response := corecontract.GetUserSessionTrajectoryResponse{
		SchemaVersion: 1, WorkspaceID: source.Session.WorkspaceID,
		SessionID: source.Session.ID, ActiveRunID: source.Session.ActiveRunID,
		Records: make([]corecontract.UserSessionTrajectoryRecord, len(projected)),
		HasMore: hasMore, Truncated: source.Truncated || trajectorySourceHasObjectPayload(source.Events), ReadAt: time.Now().UTC(),
	}
	for index := range projected {
		response.Records[index] = projected[index].record
	}
	if hasMore && len(projected) != 0 {
		first := projected[0]
		response.NextBefore, err = commands.TrajectoryCursors.Encode(trajectorycursor.Position{
			Scope: scope, RunID: first.record.RunID, RunCreatedAt: first.runCreatedAt,
			AnchorSeq: first.anchorSeq, Rank: first.rank, RecordID: first.record.ID,
		})
		if err != nil {
			return corecontract.GetUserSessionTrajectoryResponse{}, fmt.Errorf("encode trajectory cursor: %w", err)
		}
	}
	return response, nil
}

func trajectorySourceHasObjectPayload(events []coredb.UserSessionTrajectoryEvent) bool {
	for _, event := range events {
		if event.Event.Object != nil {
			return true
		}
	}
	return false
}

func trajectorySourceContainsPositionRun(runs []coredb.Run, position trajectorycursor.Position) bool {
	for _, run := range runs {
		if run.ID == position.RunID && run.CreatedAt.UTC().UnixMicro() == position.RunCreatedAt.UTC().UnixMicro() {
			return true
		}
	}
	return false
}

func projectUserSessionTrajectory(source coredb.ReadUserSessionTrajectoryResult) ([]projectedTrajectoryRecord, error) {
	runs := make(map[string]coredb.Run, len(source.Runs))
	for _, run := range source.Runs {
		runs[run.ID] = run
	}
	attemptAnchors := make(map[string]int64)
	executionAnchors := make(map[string]int64)
	operationAnchors := make(map[string]int64)
	terminals := make(map[string]trajectoryTerminal)
	messages := make(map[string]*trajectoryMessageBuilder)
	tools := make(map[string]*trajectoryToolBuilder)
	approvals := make(map[string]*projectedTrajectoryRecord)
	generic := make([]projectedTrajectoryRecord, 0)

	for _, sourceEvent := range source.Events {
		run, ok := runs[sourceEvent.RunID]
		if !ok {
			return nil, errors.New("trajectory event escaped selected run set")
		}
		event, err := contractUserSessionTranscriptEvent(source.Session.WorkspaceID, source.Session.ID, coredb.UserSessionTranscriptEvent(sourceEvent))
		if err != nil {
			return nil, fmt.Errorf("contract trajectory event %s: %w", sourceEvent.Event.EventID, err)
		}
		if event.RunAttemptID != nil {
			setFirstTrajectoryAnchor(attemptAnchors, *event.RunAttemptID, event.Seq)
		}
		if event.Object != nil {
			generic = append(generic, objectBackedTrajectoryEvent(run, event))
			continue
		}
		payloadFields := trajectoryPayloadFields(event.Payload)
		if executionID := payloadString(payloadFields, "executionId"); executionID != "" {
			setFirstTrajectoryAnchor(executionAnchors, executionID, event.Seq)
		}
		if operationID := payloadString(payloadFields, "operationId"); operationID != "" {
			setFirstTrajectoryAnchor(operationAnchors, operationID, event.Seq)
		}

		if !runevent.IsKnownKind(event.Kind) {
			if shouldProjectGenericTrajectoryEvent(event.Kind) {
				generic = append(generic, genericTrajectoryEvent(run, event))
			}
			continue
		}
		payload, err := runevent.DecodeSemanticPayload(event)
		if err != nil {
			return nil, fmt.Errorf("decode trajectory event %s: %w", event.EventID, err)
		}
		switch event.Kind {
		case runevent.KindAssistantMessageStarted, runevent.KindAssistantReasoningStarted:
			value := payload.(runevent.MessageStartedPayload)
			builder := trajectoryMessage(messages, run, event, value.MessageID)
			builder.record.record.Status = "running"
		case runevent.KindAssistantMessageDelta, runevent.KindAssistantReasoningDelta:
			value := payload.(runevent.MessageDeltaPayload)
			builder := trajectoryMessage(messages, run, event, value.MessageID)
			appendTrajectoryPreview(&builder.contents, []byte(value.Delta), &builder.truncated)
		case runevent.KindAssistantMessageCompleted, runevent.KindAssistantReasoningDone:
			value := payload.(runevent.MessageCompletedPayload)
			builder := trajectoryMessage(messages, run, event, value.MessageID)
			builder.completed = true
			builder.record.record.Status = "succeeded"
			completeTrajectoryRecord(&builder.record.record, event.CreatedAt)
		case runevent.KindToolCallStarted:
			value := payload.(runevent.ToolCallStartedPayload)
			builder := trajectoryTool(tools, run, event, value.ToolCallID)
			builder.record.record.Title = value.ToolCallName
			builder.record.record.Summary = "Tool call started"
		case runevent.KindToolCallArguments:
			value := payload.(runevent.ToolCallArgumentsPayload)
			builder := trajectoryTool(tools, run, event, value.ToolCallID)
			appendTrajectoryPreview(&builder.arguments, []byte(value.Delta), &builder.inputTruncated)
		case runevent.KindToolCallProgress:
			value := payload.(runevent.ToolCallProgressPayload)
			builder := trajectoryTool(tools, run, event, value.ToolCallID)
			if value.Message != "" {
				builder.record.record.Summary = trajectorySafeString(value.Message, trajectoryDetailBytes)
			}
		case runevent.KindToolCallCompleted:
			value := payload.(runevent.ToolCallCompletedPayload)
			builder := trajectoryTool(tools, run, event, value.ToolCallID)
			builder.completed = true
			builder.record.record.Status = "succeeded"
			builder.record.record.Summary = "Tool call completed"
			completeTrajectoryRecord(&builder.record.record, event.CreatedAt)
		case runevent.KindToolCallResult:
			value := payload.(runevent.ToolCallResultPayload)
			builder := trajectoryTool(tools, run, event, value.ToolCallID)
			appendTrajectoryPreview(&builder.result, []byte(value.Content), &builder.outputTruncated)
			if value.Presentation != nil && value.Presentation.Command != nil {
				command := value.Presentation.Command
				builder.record.record.Details = appendTrajectoryDetail(builder.record.record.Details, "command", command.Command)
				if len(builder.result) == 0 {
					appendTrajectoryPreview(&builder.result, []byte(command.Output), &builder.outputTruncated)
				}
				if command.Status != "" {
					builder.record.record.Summary = trajectorySafeString(command.Status, trajectoryDetailBytes)
				}
			}
		case runevent.KindRunCompleted, runevent.KindRunFailed, runevent.KindRunInterrupted,
			runevent.KindRunCancelling, runevent.KindRunCancelled:
			terminals[run.ID] = trajectoryTerminal{sequence: event.Seq, event: event, payload: payload.(runevent.RunTerminalPayload)}
		case runevent.KindApprovalRequested, runevent.KindApprovalApproved, runevent.KindApprovalDenied,
			runevent.KindApprovalExpired, runevent.KindApprovalCancelled, runevent.KindApprovalConsumed:
			value := payload.(runevent.ApprovalPayload)
			key := "approval:" + value.ApprovalID
			record := approvals[key]
			if record == nil {
				created := projectedTrajectoryRecord{
					record: corecontract.UserSessionTrajectoryRecord{
						ID: key, ParentID: "execution:" + value.ExecutionID, Kind: "approval",
						Status: "pending", Title: "Approval", Summary: "Approval requested",
						RunID: run.ID, RunAttemptID: value.RunAttemptID,
						RunAttemptGeneration: value.RunAttemptGeneration,
						ExecutionID:          value.ExecutionID, StartedAt: event.CreatedAt,
						Details: []corecontract.UserSessionTrajectoryDetail{{Name: "tool", Value: value.ToolName}},
					},
					runCreatedAt: run.CreatedAt, anchorSeq: event.Seq, rank: trajectoryRankApproval,
				}
				record = &created
				approvals[key] = record
			}
			record.record.Status = normalizeTrajectoryStatus("approval", value.Status)
			record.record.Summary = "Approval " + strings.ReplaceAll(value.Status, "_", " ")
			if trajectoryStatusTerminal(record.record.Status) {
				completeTrajectoryRecord(&record.record, event.CreatedAt)
			}
		}
	}

	result := make([]projectedTrajectoryRecord, 0, len(source.Runs)+len(source.Attempts)+len(source.Executions)+len(source.Operations)+len(messages)+len(tools))
	for _, run := range source.Runs {
		record := projectedTrajectoryRecord{
			record: corecontract.UserSessionTrajectoryRecord{
				ID: "run:" + run.ID, Kind: "run", Status: normalizeTrajectoryStatus("run", run.Status),
				Title: "Run", Summary: "Run " + strings.ReplaceAll(run.Status, "_", " "),
				RunID: run.ID, StartedAt: run.CreatedAt,
				Details: []corecontract.UserSessionTrajectoryDetail{{Name: "generation", Value: strconv.FormatInt(run.CurrentAttemptGeneration, 10)}},
			},
			runCreatedAt: run.CreatedAt, anchorSeq: 0, rank: trajectoryRankRun,
		}
		if trajectoryStatusTerminal(record.record.Status) {
			completeTrajectoryRecord(&record.record, run.UpdatedAt)
		}
		if terminal, ok := terminals[run.ID]; ok {
			if terminal.payload.Message != "" || terminal.payload.Code != "" {
				record.record.Failure = trajectoryFailure(terminal.payload.Code, terminal.payload.Message, "harness-worker", "run")
				if record.record.Status == "failed" {
					record.record.Summary = record.record.Failure.Message
				}
			}
		}
		result = append(result, record)
		if terminal, ok := terminals[run.ID]; ok && record.record.Failure != nil && trajectoryModelFailure(record.record.Failure.Category) {
			failure := *record.record.Failure
			result = append(result, projectedTrajectoryRecord{
				record: corecontract.UserSessionTrajectoryRecord{
					ID: "model:" + run.ID + ":terminal", ParentID: trajectoryAttemptParent(run.ID, terminal.event.RunAttemptID, terminal.event.RunAttemptGeneration),
					Kind: "model", Status: "failed", Title: "Model request", Summary: failure.Message,
					RunID: run.ID, RunAttemptID: pointerString(terminal.event.RunAttemptID),
					RunAttemptGeneration: pointerInt64(terminal.event.RunAttemptGeneration),
					StartedAt:            terminal.event.CreatedAt, CompletedAt: timePointer(terminal.event.CreatedAt),
					DurationMillis: trajectoryInt64Pointer(0), Details: []corecontract.UserSessionTrajectoryDetail{}, Failure: &failure,
				},
				runCreatedAt: run.CreatedAt, anchorSeq: terminal.sequence, rank: trajectoryRankModel,
			})
		}
	}
	for _, attempt := range source.Attempts {
		run, ok := runs[attempt.RunID]
		if !ok {
			continue
		}
		started := attempt.CreatedAt
		if attempt.TurnStartedAt != nil {
			started = *attempt.TurnStartedAt
		}
		record := projectedTrajectoryRecord{
			record: corecontract.UserSessionTrajectoryRecord{
				ID:       "attempt:" + attempt.ID + ":" + strconv.FormatInt(attempt.Generation, 10),
				ParentID: "run:" + run.ID, Kind: "attempt", Status: normalizeTrajectoryStatus("attempt", attempt.Status),
				Title:   "Attempt " + strconv.FormatInt(attempt.Generation, 10),
				Summary: "Attempt " + strings.ReplaceAll(attempt.Status, "_", " "), RunID: run.ID,
				RunAttemptID: attempt.ID, RunAttemptGeneration: attempt.Generation,
				StartedAt: started, Details: []corecontract.UserSessionTrajectoryDetail{},
			},
			runCreatedAt: run.CreatedAt, anchorSeq: attemptAnchors[attempt.ID], rank: trajectoryRankAttempt,
		}
		if trajectoryStatusTerminal(record.record.Status) {
			completeTrajectoryRecord(&record.record, attempt.UpdatedAt)
		}
		result = append(result, record)
	}
	for _, builder := range messages {
		safe := safediagnostic.Sanitize(builder.contents, trajectoryTextPreviewBytes)
		builder.record.record.Output = safe.Value
		builder.record.record.OutputTruncated = builder.truncated || safe.Truncated
		if !builder.completed {
			if terminal, ok := terminals[builder.record.record.RunID]; ok {
				builder.record.record.Status = "unknown"
				builder.record.record.Summary = "Assistant output ended without a confirmed completion"
				completeTrajectoryRecord(&builder.record.record, terminal.event.CreatedAt)
				builder.record.record.Failure = trajectoryFailure("output_incomplete", builder.record.record.Summary, "stock-app-server", "assistant_output")
			}
		}
		if builder.record.record.Summary == "" {
			builder.record.record.Summary = "Assistant output"
		}
		result = append(result, builder.record)
	}
	for _, builder := range tools {
		input := safediagnostic.Sanitize(builder.arguments, trajectoryTextPreviewBytes)
		output := safediagnostic.Sanitize(builder.result, trajectoryTextPreviewBytes)
		builder.record.record.Input, builder.record.record.Output = input.Value, output.Value
		builder.record.record.InputTruncated = builder.inputTruncated || input.Truncated
		builder.record.record.OutputTruncated = builder.outputTruncated || output.Truncated
		if !builder.completed {
			if terminal, ok := terminals[builder.record.record.RunID]; ok {
				builder.record.record.Status = "unknown"
				builder.record.record.Summary = "Tool call ended without a confirmed completion"
				completeTrajectoryRecord(&builder.record.record, terminal.event.CreatedAt)
				builder.record.record.Failure = trajectoryFailure("output_incomplete", builder.record.record.Summary, "stock-app-server", "tool_result")
			}
		}
		result = append(result, builder.record)
	}
	for _, record := range approvals {
		result = append(result, *record)
	}
	result = append(result, generic...)

	for _, execution := range source.Executions {
		run, ok := runs[execution.RunID]
		if !ok {
			continue
		}
		parent := "tool:" + execution.RunID + ":" + execution.AppServerToolCallID
		record := projectedTrajectoryRecord{
			record: corecontract.UserSessionTrajectoryRecord{
				ID: "execution:" + execution.ID, ParentID: parent, Kind: "execution",
				Status: normalizeTrajectoryStatus("execution", execution.Status), Title: execution.ToolName,
				Summary: "Execution " + strings.ReplaceAll(execution.Status, "_", " "),
				RunID:   execution.RunID, RunAttemptID: execution.RunAttemptID,
				RunAttemptGeneration: execution.RunAttemptGeneration, ToolCallID: execution.AppServerToolCallID,
				ExecutionID: execution.ID, StartedAt: execution.CreatedAt,
				Details: []corecontract.UserSessionTrajectoryDetail{
					{Name: "policy", Value: execution.PolicyDecision},
					{Name: "target", Value: execution.Target.Kind},
					{Name: "toolVersion", Value: execution.ToolVersion},
				},
			},
			runCreatedAt: run.CreatedAt, anchorSeq: executionAnchors[execution.ID], rank: trajectoryRankExecution,
		}
		if execution.Target.ID != "" {
			record.record.Details = appendTrajectoryDetail(record.record.Details, "targetId", execution.Target.ID)
			record.record.TargetGeneration = execution.Target.Generation
			if execution.Target.Kind == coredb.DispatchTargetTAE {
				record.record.SandboxID = execution.Target.ID
			}
		}
		if execution.TerminalAt != nil {
			completeTrajectoryRecord(&record.record, *execution.TerminalAt)
		}
		if record.record.Status == "failed" || record.record.Status == "unknown" {
			record.record.Failure = trajectoryFailure("execution_"+execution.Status, "Execution ended "+execution.Status, "executor-gateway", "execution")
		}
		result = append(result, record)
	}
	executions := make(map[string]coredb.Execution, len(source.Executions))
	for _, execution := range source.Executions {
		executions[execution.ID] = execution
	}
	for _, operation := range source.Operations {
		execution, ok := executions[operation.ExecutionID]
		if !ok {
			continue
		}
		run, ok := runs[execution.RunID]
		if !ok {
			continue
		}
		record := projectedTrajectoryRecord{
			record: corecontract.UserSessionTrajectoryRecord{
				ID: "operation:" + operation.ID, ParentID: "execution:" + operation.ExecutionID,
				Kind: "operation", Status: normalizeTrajectoryStatus("operation", operation.Status),
				Title:   strings.ReplaceAll(operation.Kind, "_", " "),
				Summary: "Operation " + strings.ReplaceAll(operation.Status, "_", " "),
				RunID:   execution.RunID, RunAttemptID: execution.RunAttemptID,
				RunAttemptGeneration: execution.RunAttemptGeneration, ToolCallID: execution.AppServerToolCallID,
				ExecutionID: execution.ID, OperationID: operation.ID, StartedAt: operation.CreatedAt,
				Details: []corecontract.UserSessionTrajectoryDetail{
					{Name: "effect", Value: operation.EffectClass},
					{Name: "ordinal", Value: strconv.Itoa(operation.Ordinal)},
					{Name: "target", Value: operation.Target.Kind},
				},
			},
			runCreatedAt: run.CreatedAt, anchorSeq: operationAnchors[operation.ID], rank: trajectoryRankOperation,
		}
		if operation.Target.ID != "" {
			record.record.TargetGeneration = operation.Target.Generation
			if operation.Target.Kind == coredb.DispatchTargetTAE {
				record.record.SandboxID = operation.Target.ID
			}
		}
		if operation.AcknowledgedAt != nil {
			record.record.Details = appendTrajectoryDetail(record.record.Details, "acknowledgedAt", operation.AcknowledgedAt.UTC().Format(time.RFC3339Nano))
		}
		if operation.TerminalAt != nil {
			completeTrajectoryRecord(&record.record, *operation.TerminalAt)
		}
		if record.record.Status == "failed" || record.record.Status == "unknown" {
			code := "operation_" + operation.Status
			message := "Operation ended " + operation.Status
			if operation.Status == coredb.OperationStatusUnknown {
				code, message = "output_incomplete", "The dispatched operation has no confirmed terminal result"
			}
			record.record.Failure = trajectoryFailure(code, message, "executor-gateway", "operation")
		}
		result = append(result, record)
	}

	sandboxes := make(map[string]coredb.ManagedSandbox, len(source.Sandboxes))
	for _, sandbox := range source.Sandboxes {
		sandboxes[sandbox.ID] = sandbox
	}
	for _, activity := range source.Activities {
		sandbox, ok := sandboxes[activity.SandboxID]
		run, runOK := runs[activity.RunID]
		if !ok || !runOK {
			continue
		}
		status := normalizeTrajectoryStatus("sandbox", sandbox.ObservedState)
		record := projectedTrajectoryRecord{
			record: corecontract.UserSessionTrajectoryRecord{
				ID:       "sandbox:" + sandbox.ID + ":" + activity.RunAttemptID,
				ParentID: "attempt:" + activity.RunAttemptID + ":" + strconv.FormatInt(activity.RunAttemptGeneration, 10),
				Kind:     "sandbox", Status: status, Title: "TAE sandbox",
				Summary: "Sandbox " + strings.ReplaceAll(sandbox.ObservedState, "_", " "),
				RunID:   activity.RunID, RunAttemptID: activity.RunAttemptID,
				RunAttemptGeneration: activity.RunAttemptGeneration,
				SandboxID:            sandbox.ID, TargetGeneration: activity.TargetGeneration,
				StartedAt: activity.CreatedAt,
				Details: []corecontract.UserSessionTrajectoryDetail{
					{Name: "provider", Value: sandbox.ProviderKind},
					{Name: "region", Value: sandbox.ProviderRegion},
					{Name: "psm", Value: sandbox.ProviderPSM},
				},
			},
			runCreatedAt: run.CreatedAt, anchorSeq: attemptAnchors[activity.RunAttemptID], rank: trajectoryRankSandbox,
		}
		if sandbox.ObservedState == coredb.ManagedSandboxReady && sandbox.LastObservedAt != nil {
			completeTrajectoryRecord(&record.record, *sandbox.LastObservedAt)
		}
		if activity.ReleasedAt != nil {
			record.record.Details = appendTrajectoryDetail(record.record.Details, "releasedAt", activity.ReleasedAt.UTC().Format(time.RFC3339Nano))
		}
		if status == "failed" || status == "unknown" {
			code := sandbox.LastErrorCode
			if code == "" {
				code = "sandbox_not_ready"
			}
			record.record.Failure = trajectoryFailure(code, "TAE sandbox did not become ready", "sandbox-gateway", "sandbox_ready")
		}
		result = append(result, record)
	}

	for _, credential := range source.CredentialUses {
		event := credential.Event
		run, ok := runs[event.RunID]
		if !ok {
			continue
		}
		status := "succeeded"
		if event.Decision != "allow" {
			status = "failed"
		}
		parent := "operation:" + event.OperationID
		if event.OperationID == "" {
			parent = "attempt:" + event.RunAttemptID + ":" + strconv.FormatInt(event.RunAttemptGeneration, 10)
		}
		summary := "Credential resolved via " + event.Stage
		if status == "failed" {
			summary = "Credential resolution denied: " + event.ReasonCode
		}
		record := projectedTrajectoryRecord{
			record: corecontract.UserSessionTrajectoryRecord{
				ID: "credential:" + event.EventID, ParentID: parent, Kind: "credential", Status: status,
				Title:   trajectoryProviderTitle(event.ProviderKind),
				Summary: summary, RunID: event.RunID, RunAttemptID: event.RunAttemptID,
				RunAttemptGeneration: event.RunAttemptGeneration, ExecutionID: event.ExecutionID,
				OperationID: event.OperationID, SandboxID: event.SandboxID,
				TargetGeneration: event.TargetGeneration, StartedAt: event.At,
				CompletedAt: timePointer(event.At), DurationMillis: trajectoryInt64Pointer(0),
				Details: []corecontract.UserSessionTrajectoryDetail{
					{Name: "mode", Value: event.Stage},
					{Name: "binding", Value: firstNonempty(credential.BindingDisplayName, event.BindingID)},
					{Name: "credentialVersion", Value: strconv.FormatInt(event.CredentialVersion, 10)},
					{Name: "webhookUsed", Value: strconv.FormatBool(event.Stage != "process_env")},
					{Name: "egressAuthorizerUsed", Value: strconv.FormatBool(event.Stage == "egress")},
				},
			},
			runCreatedAt: run.CreatedAt, anchorSeq: operationAnchors[event.OperationID], rank: trajectoryRankCredential,
		}
		if status == "failed" {
			record.record.Failure = trajectoryFailure(event.ReasonCode, summary, "agentserver-core", "credential_resolve")
		}
		result = append(result, record)
	}

	for _, checkpoint := range source.Checkpoints {
		run, ok := runs[checkpoint.RunID]
		if !ok {
			continue
		}
		anchor := int64(0)
		if terminal, ok := terminals[checkpoint.RunID]; ok {
			anchor = terminal.sequence
		}
		result = append(result, projectedTrajectoryRecord{
			record: corecontract.UserSessionTrajectoryRecord{
				ID:       "checkpoint:" + checkpoint.ID,
				ParentID: "attempt:" + checkpoint.RunAttemptID + ":" + strconv.FormatInt(checkpoint.RunAttemptGeneration, 10),
				Kind:     "checkpoint", Status: "succeeded", Title: "Checkpoint",
				Summary: "Checkpoint committed", RunID: checkpoint.RunID,
				RunAttemptID: checkpoint.RunAttemptID, RunAttemptGeneration: checkpoint.RunAttemptGeneration,
				StartedAt: checkpoint.CreatedAt, CompletedAt: timePointer(checkpoint.CreatedAt),
				DurationMillis: trajectoryInt64Pointer(0), Details: []corecontract.UserSessionTrajectoryDetail{},
			},
			runCreatedAt: run.CreatedAt, anchorSeq: anchor, rank: trajectoryRankCheckpoint,
		})
	}

	for index := range result {
		normalizeTrajectoryRecord(&result[index].record)
	}
	sort.SliceStable(result, func(left, right int) bool { return compareProjectedTrajectory(result[left], result[right]) < 0 })
	return result, nil
}

func trajectoryMessage(builders map[string]*trajectoryMessageBuilder, run coredb.Run, event runevent.Event, messageID string) *trajectoryMessageBuilder {
	key := event.Kind
	if strings.HasPrefix(key, "assistant.message.") {
		key = "assistant"
	} else {
		key = "reasoning"
	}
	id := key + ":" + run.ID + ":" + messageID
	if builder := builders[id]; builder != nil {
		return builder
	}
	title := "Assistant response"
	summary := "Assistant response streaming"
	if key == "reasoning" {
		title, summary = "Reasoning summary", "Reasoning summary streaming"
	}
	builder := &trajectoryMessageBuilder{record: projectedTrajectoryRecord{
		record: corecontract.UserSessionTrajectoryRecord{
			ID: id, ParentID: trajectoryAttemptParent(run.ID, event.RunAttemptID, event.RunAttemptGeneration),
			Kind: key, Status: "running", Title: title, Summary: summary,
			RunID: run.ID, RunAttemptID: pointerString(event.RunAttemptID),
			RunAttemptGeneration: pointerInt64(event.RunAttemptGeneration), StartedAt: event.CreatedAt,
			Details: []corecontract.UserSessionTrajectoryDetail{},
		},
		runCreatedAt: run.CreatedAt, anchorSeq: event.Seq, rank: trajectoryRankAssistant,
	}}
	builders[id] = builder
	return builder
}

func trajectoryTool(builders map[string]*trajectoryToolBuilder, run coredb.Run, event runevent.Event, toolCallID string) *trajectoryToolBuilder {
	id := "tool:" + run.ID + ":" + toolCallID
	if builder := builders[id]; builder != nil {
		return builder
	}
	builder := &trajectoryToolBuilder{record: projectedTrajectoryRecord{
		record: corecontract.UserSessionTrajectoryRecord{
			ID: id, ParentID: trajectoryAttemptParent(run.ID, event.RunAttemptID, event.RunAttemptGeneration),
			Kind: "tool", Status: "running", Title: "Tool call", Summary: "Tool call running",
			RunID: run.ID, RunAttemptID: pointerString(event.RunAttemptID),
			RunAttemptGeneration: pointerInt64(event.RunAttemptGeneration), ToolCallID: toolCallID,
			StartedAt: event.CreatedAt, Details: []corecontract.UserSessionTrajectoryDetail{},
		},
		runCreatedAt: run.CreatedAt, anchorSeq: event.Seq, rank: trajectoryRankTool,
	}}
	builders[id] = builder
	return builder
}

func genericTrajectoryEvent(run coredb.Run, event runevent.Event) projectedTrajectoryRecord {
	status := "info"
	if strings.HasSuffix(event.Kind, ".failed") {
		status = "failed"
	} else if strings.HasSuffix(event.Kind, ".ready") || strings.HasSuffix(event.Kind, ".accepted") || strings.HasSuffix(event.Kind, ".succeeded") {
		status = "succeeded"
	}
	record := corecontract.UserSessionTrajectoryRecord{
		ID: "event:" + event.EventID, ParentID: trajectoryAttemptParent(run.ID, event.RunAttemptID, event.RunAttemptGeneration),
		Kind: "event", Status: status, Title: strings.ReplaceAll(event.Kind, ".", " · "),
		Summary: event.Kind, RunID: run.ID, RunAttemptID: pointerString(event.RunAttemptID),
		RunAttemptGeneration: pointerInt64(event.RunAttemptGeneration), StartedAt: event.CreatedAt,
		CompletedAt: timePointer(event.CreatedAt), DurationMillis: trajectoryInt64Pointer(0),
		Details: []corecontract.UserSessionTrajectoryDetail{{Name: "source", Value: event.Source}},
	}
	if status == "failed" || status == "unknown" {
		record.Failure = trajectoryFailure(strings.ReplaceAll(event.Kind, ".", "_"), "Lifecycle event "+event.Kind, event.Source, event.Kind)
	}
	return projectedTrajectoryRecord{record: record, runCreatedAt: run.CreatedAt, anchorSeq: event.Seq, rank: trajectoryRankEvent}
}

func objectBackedTrajectoryEvent(run coredb.Run, event runevent.Event) projectedTrajectoryRecord {
	details := []corecontract.UserSessionTrajectoryDetail{{Name: "payload", Value: "object"}}
	if event.Object != nil {
		details = appendTrajectoryDetail(details, "mediaType", event.Object.MediaType)
		details = appendTrajectoryDetail(details, "bytes", strconv.FormatInt(event.Object.Size, 10))
	}
	return projectedTrajectoryRecord{
		record: corecontract.UserSessionTrajectoryRecord{
			ID: "event:" + event.EventID, ParentID: trajectoryAttemptParent(run.ID, event.RunAttemptID, event.RunAttemptGeneration),
			Kind: "event", Status: "info", Title: strings.ReplaceAll(event.Kind, ".", " · "),
			Summary: "Event payload is stored out of line; its preview is unavailable",
			RunID:   run.ID, RunAttemptID: pointerString(event.RunAttemptID),
			RunAttemptGeneration: pointerInt64(event.RunAttemptGeneration), StartedAt: event.CreatedAt,
			CompletedAt: timePointer(event.CreatedAt), DurationMillis: trajectoryInt64Pointer(0),
			OutputTruncated: true, Details: details,
		},
		runCreatedAt: run.CreatedAt, anchorSeq: event.Seq, rank: trajectoryRankEvent,
	}
}

func shouldProjectGenericTrajectoryEvent(kind string) bool {
	if strings.HasPrefix(kind, "execution.") || strings.HasPrefix(kind, "operation.") || kind == "run.queued" || kind == "attempt.leased" {
		return false
	}
	return strings.HasPrefix(kind, "run.") || strings.HasPrefix(kind, "attempt.") || strings.HasPrefix(kind, "turn.") || strings.HasPrefix(kind, "runtime.") || strings.HasPrefix(kind, "sandbox.") || strings.HasPrefix(kind, "checkpoint.") || strings.HasPrefix(kind, "model.")
}

func setFirstTrajectoryAnchor(values map[string]int64, id string, sequence int64) {
	if id == "" {
		return
	}
	if current, ok := values[id]; !ok || sequence < current {
		values[id] = sequence
	}
}

func trajectoryPayloadFields(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil
	}
	return result
}

func payloadString(fields map[string]json.RawMessage, name string) string {
	var result string
	if len(fields[name]) == 0 || json.Unmarshal(fields[name], &result) != nil {
		return ""
	}
	return result
}

func appendTrajectoryPreview(target *[]byte, value []byte, truncated *bool) {
	remaining := trajectoryTextPreviewBytes - len(*target)
	if remaining <= 0 {
		*truncated = *truncated || len(value) != 0
		return
	}
	if len(value) > remaining {
		value = value[:remaining]
		for len(value) > 0 && !utf8.Valid(value) {
			value = value[:len(value)-1]
		}
		*truncated = true
	}
	*target = append(*target, value...)
}

func appendTrajectoryDetail(values []corecontract.UserSessionTrajectoryDetail, name, value string) []corecontract.UserSessionTrajectoryDetail {
	if value == "" || len(values) >= 32 {
		return values
	}
	return append(values, corecontract.UserSessionTrajectoryDetail{Name: name, Value: trajectorySafeString(value, trajectoryDetailBytes)})
}

func trajectorySafeString(value string, maximum int) string {
	return safediagnostic.Sanitize([]byte(value), maximum).Value
}

func trajectorySafeLabel(value string, maximum int, fallback string) string {
	value = strings.Join(strings.Fields(trajectorySafeString(value, maximum)), " ")
	if value == "" {
		return fallback
	}
	return value
}

func normalizeTrajectoryRecord(record *corecontract.UserSessionTrajectoryRecord) {
	record.Title = trajectorySafeLabel(record.Title, 1024, "Trajectory record")
	record.Summary = strings.TrimSpace(trajectorySafeString(record.Summary, 4096))
	if record.Input != "" {
		value := safediagnostic.Sanitize([]byte(record.Input), trajectoryTextPreviewBytes)
		record.Input, record.InputTruncated = value.Value, record.InputTruncated || value.Truncated
	}
	if record.Output != "" {
		value := safediagnostic.Sanitize([]byte(record.Output), trajectoryTextPreviewBytes)
		record.Output, record.OutputTruncated = value.Value, record.OutputTruncated || value.Truncated
	}
	details := make([]corecontract.UserSessionTrajectoryDetail, 0, len(record.Details))
	for _, detail := range record.Details {
		if len(details) == 32 {
			break
		}
		name := trajectorySafeLabel(detail.Name, 128, "detail")
		value := trajectorySafeString(detail.Value, trajectoryDetailBytes)
		details = append(details, corecontract.UserSessionTrajectoryDetail{Name: name, Value: value})
	}
	record.Details = details
	if record.Failure == nil {
		return
	}
	failure := record.Failure
	failure.Code = trajectorySafeLabel(failure.Code, 128, "unknown")
	failure.Category = trajectorySafeLabel(failure.Category, 128, "unknown")
	failure.Message = strings.TrimSpace(trajectorySafeString(failure.Message, 4096))
	if failure.Message == "" {
		failure.Message = "The operation failed without a safe diagnostic message"
	}
	failure.Component = trajectorySafeLabel(failure.Component, 128, "agentserver")
	failure.Phase = trajectorySafeLabel(failure.Phase, 128, "unknown")
	if failure.Fingerprint != "" {
		failure.Fingerprint = trajectorySafeLabel(failure.Fingerprint, 256, "")
	}
}

func normalizeTrajectoryStatus(kind, status string) string {
	if kind == "approval" {
		switch status {
		case coredb.ApprovalStatusPending:
			return "running"
		case coredb.ApprovalStatusApproved, coredb.ApprovalStatusConsumed:
			return "succeeded"
		case coredb.ApprovalStatusDenied, coredb.ApprovalStatusExpired:
			return "failed"
		case coredb.ApprovalStatusCancelled:
			return "cancelled"
		default:
			return "unknown"
		}
	}
	switch status {
	case "queued", "created", "leased", "reserved":
		return "queued"
	case "starting", "running", "finalizing", "cancelling", "pending_approval", "approved",
		"dispatching", "prepared", "acknowledged", "creating", "deleting", "pending", "requested":
		return "running"
	case "completed", "succeeded", "skipped", "ready", "consumed":
		return "succeeded"
	case "failed", "interrupted", "fenced", "denied", "expired":
		return "failed"
	case "cancelled", "deleted":
		return "cancelled"
	case "unknown":
		return "unknown"
	default:
		return "info"
	}
}

func trajectoryStatusTerminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "cancelled" || status == "unknown"
}

func completeTrajectoryRecord(record *corecontract.UserSessionTrajectoryRecord, completed time.Time) {
	completed = completed.UTC()
	// Lifecycle timestamps can originate on different components. A sandbox
	// that was already ready, for example, may carry a provider observation a
	// few milliseconds before Core committed the run activity. Preserve the
	// terminal fact while keeping the public interval internally consistent.
	if completed.Before(record.StartedAt) {
		completed = record.StartedAt.UTC()
	}
	record.CompletedAt = &completed
	duration := completed.Sub(record.StartedAt).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	record.DurationMillis = &duration
}

func trajectoryFailure(code, message, component, phase string) *corecontract.UserSessionTrajectoryFailure {
	message = trajectorySafeString(message, 4096)
	if message == "" {
		message = "The operation failed without a safe diagnostic message"
	}
	category := trajectoryFailureCategory(code, message)
	failure := &corecontract.UserSessionTrajectoryFailure{
		Code: firstNonempty(code, category), Category: category, Message: message,
		Component: component, Phase: phase,
		Retryable: category == "model_overloaded" || category == "model_rate_limited" ||
			category == "model_transport_failure" || category == "model_timeout" ||
			category == "tae_transport" || category == "sandbox_not_ready" || category == "output_incomplete",
	}
	failure.Fingerprint = trajectoryFailureFingerprint(message)
	return failure
}

func trajectoryFailureFingerprint(message string) string {
	for _, field := range strings.Fields(message) {
		field = strings.Trim(field, `"';,()[]{} `)
		marker := strings.Index(field, "sha256=")
		if marker < 0 {
			continue
		}
		digest := field[marker+len("sha256="):]
		if len(digest) != 64 {
			continue
		}
		valid := true
		for _, current := range digest {
			if (current < '0' || current > '9') && (current < 'a' || current > 'f') {
				valid = false
				break
			}
		}
		if valid {
			return "sha256:" + digest
		}
	}
	return ""
}

func trajectoryFailureCategory(code, message string) string {
	for _, field := range strings.Fields(message) {
		if strings.HasPrefix(field, "category=") {
			value := strings.Trim(strings.TrimPrefix(field, "category="), ";, ")
			if value != "" {
				return value
			}
		}
	}
	lower := strings.ToLower(code + " " + message)
	switch {
	case strings.Contains(lower, "serveroverloaded") || strings.Contains(lower, "model_overloaded") || strings.Contains(lower, "model is at capacity"):
		return "model_overloaded"
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "429"):
		return "model_rate_limited"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		return "model_timeout"
	case strings.Contains(lower, "model") && (strings.Contains(lower, "transport") || strings.Contains(lower, "connection")):
		return "model_transport_failure"
	case strings.Contains(lower, "credential"):
		return "credential_unavailable"
	case strings.Contains(lower, "sandbox") && strings.Contains(lower, "ready"):
		return "sandbox_not_ready"
	case strings.Contains(lower, "tae") || strings.Contains(lower, "network unreachable"):
		return "tae_transport"
	case strings.Contains(lower, "output") && strings.Contains(lower, "incomplete"):
		return "output_incomplete"
	case code != "":
		return code
	default:
		return "unknown"
	}
}

func trajectoryModelFailure(category string) bool {
	return strings.HasPrefix(category, "model_") || strings.HasPrefix(category, "tls_")
}

func trajectoryAttemptParent(runID string, attemptID *string, generation *int64) string {
	if attemptID == nil || generation == nil {
		return "run:" + runID
	}
	return "attempt:" + *attemptID + ":" + strconv.FormatInt(*generation, 10)
}

func compareProjectedTrajectory(left, right projectedTrajectoryRecord) int {
	if before, after := left.runCreatedAt, right.runCreatedAt; !before.Equal(after) {
		if before.Before(after) {
			return -1
		}
		return 1
	}
	if left.record.RunID != right.record.RunID {
		return strings.Compare(left.record.RunID, right.record.RunID)
	}
	if left.anchorSeq != right.anchorSeq {
		if left.anchorSeq < right.anchorSeq {
			return -1
		}
		return 1
	}
	if left.rank != right.rank {
		if left.rank < right.rank {
			return -1
		}
		return 1
	}
	return strings.Compare(left.record.ID, right.record.ID)
}

func compareTrajectoryPosition(record projectedTrajectoryRecord, position trajectorycursor.Position) int {
	other := projectedTrajectoryRecord{
		record:       corecontract.UserSessionTrajectoryRecord{ID: position.RecordID, RunID: position.RunID},
		runCreatedAt: position.RunCreatedAt, anchorSeq: position.AnchorSeq, rank: position.Rank,
	}
	return compareProjectedTrajectory(record, other)
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func pointerInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func trajectoryInt64Pointer(value int64) *int64 { return &value }

func trajectoryProviderTitle(provider string) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return "Credential"
	}
	return strings.ToUpper(provider[:1]) + provider[1:] + " credential"
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
