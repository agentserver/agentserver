package browsergateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const maxCoreRunResponseBytes = int64(18 * 1024 * 1024)

type CoreRunBackend struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewCoreRunBackend(baseURL string, httpClient *http.Client) (*CoreRunBackend, error) {
	if httpClient == nil {
		return nil, errors.New("core run HTTP client is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("core run base URL must be an absolute HTTP(S) origin without credentials, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("core run base URL must not contain a path")
	}
	if parsed.Scheme == "http" && !coreRunLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext core run base URL is allowed only on loopback")
	}
	parsed.Path = ""
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CoreRunBackend{baseURL: parsed, httpClient: &clientCopy}, nil
}

func (backend *CoreRunBackend) StartRun(ctx context.Context, request StartRunRequest) (StartRunResult, error) {
	body, err := json.Marshal(corecontract.CreateUserRunRequest{
		ClientRunID: request.ClientRunID, Prompt: request.Prompt,
		ExpectedPermissionModeVersion:   request.ExpectedPermissionModeVersion,
		ExpectedWorkingDirectoryVersion: request.ExpectedWorkingDirectoryVersion,
	})
	if err != nil {
		return StartRunResult{}, fmt.Errorf("encode core CreateRun request: %w", err)
	}
	endpoint := backend.endpoint(corecontract.CreateUserRunPath(request.WorkspaceID, request.SessionID))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return StartRunResult{}, fmt.Errorf("construct core CreateRun request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+request.BearerToken)
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	response, raw, err := backend.do(httpRequest)
	if err != nil {
		return StartRunResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return StartRunResult{}, decodePublicCoreError(response.StatusCode, raw)
	}
	var result corecontract.CreateUserRunResponse
	if err := decodeStrictCoreJSON(raw, &result); err != nil {
		return StartRunResult{}, fmt.Errorf("decode core CreateRun response: %w", err)
	}
	if (response.StatusCode == http.StatusCreated) != result.Created {
		return StartRunResult{}, errors.New("core CreateRun status and created flag disagree")
	}
	started := StartRunResult{
		WorkspaceID: result.WorkspaceID, SessionID: result.SessionID, RunID: result.RunID,
		CreatedAt: result.CreatedAt, Cursor: result.Cursor, LastEventSequence: result.LastEventSequence,
	}
	if err := validateStartResult(started, request.WorkspaceID, request.SessionID); err != nil {
		return StartRunResult{}, fmt.Errorf("validate core CreateRun response: %w", err)
	}
	if request.ResumeCursor != "" {
		resolved, expired, err := backend.resolveRunCursor(
			ctx,
			request.BearerToken,
			result.WorkspaceID,
			result.SessionID,
			result.RunID,
			request.ResumeCursor,
		)
		if err != nil {
			return StartRunResult{}, err
		}
		if expired != nil {
			started.Cursor = expired.RebaseCursor
			started.LastEventSequence = expired.LastEventSequence
			started.RebaseSnapshot = expired.Snapshot
		} else {
			started.Cursor = resolved.NextCursor
			started.LastEventSequence = resolved.LastEventSequence
		}
	}
	return started, nil
}

func (backend *CoreRunBackend) ReadRunEvents(ctx context.Context, request ReadRunEventsRequest) (ReadRunEventsResult, error) {
	endpoint := backend.endpoint(corecontract.ReadUserRunEventsPath(request.WorkspaceID, request.RunID))
	query := endpoint.Query()
	query.Set("after", request.After)
	query.Set("limit", strconv.Itoa(request.Limit))
	query.Set("waitMs", strconv.FormatInt(request.Wait.Milliseconds(), 10))
	endpoint.RawQuery = query.Encode()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return ReadRunEventsResult{}, fmt.Errorf("construct core event cursor request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+request.BearerToken)
	httpRequest.Header.Set("Accept", "application/json")

	response, raw, err := backend.do(httpRequest)
	if err != nil {
		return ReadRunEventsResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusGone {
		expired, err := decodeCoreCursorExpired(raw, ProjectionScope{
			WorkspaceID: request.WorkspaceID, SessionID: request.SessionID, RunID: request.RunID,
		})
		if err != nil {
			return ReadRunEventsResult{}, err
		}
		return ReadRunEventsResult{}, expired
	}
	if response.StatusCode != http.StatusOK {
		return ReadRunEventsResult{}, decodePublicCoreError(response.StatusCode, raw)
	}
	var result corecontract.ReadUserRunEventsResponse
	if err := decodeStrictCoreJSON(raw, &result); err != nil {
		return ReadRunEventsResult{}, fmt.Errorf("decode core event cursor response: %w", err)
	}
	if result.Events == nil || result.EventCursors == nil || len(result.EventCursors) != len(result.Events) {
		return ReadRunEventsResult{}, errors.New("core event cursor count does not match the event page")
	}
	for index := range result.Events {
		if err := result.Events[index].Validate(); err != nil {
			return ReadRunEventsResult{}, fmt.Errorf("validate core event %d: %w", index, err)
		}
		if result.Events[index].WorkspaceID != request.WorkspaceID || result.Events[index].SessionID != request.SessionID || result.Events[index].RunID != request.RunID {
			return ReadRunEventsResult{}, errors.New("core event escaped the requested browser projection scope")
		}
	}
	if len(result.Events) != 0 && result.LastEventSequence != result.Events[len(result.Events)-1].Seq {
		return ReadRunEventsResult{}, errors.New("core event page sequence does not match its final event")
	}
	return ReadRunEventsResult{Events: result.Events, EventCursors: result.EventCursors, NextCursor: result.NextCursor}, nil
}

func (backend *CoreRunBackend) CancelRun(ctx context.Context, request CancelRunRequest) (CancelRunResult, error) {
	endpoint := backend.endpoint(corecontract.CancelUserRunPath(request.WorkspaceID, request.RunID))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
	if err != nil {
		return CancelRunResult{}, fmt.Errorf("construct core cancel request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+request.BearerToken)
	httpRequest.Header.Set("Accept", "application/json")
	response, raw, err := backend.do(httpRequest)
	if err != nil {
		return CancelRunResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return CancelRunResult{}, decodePublicCoreError(response.StatusCode, raw)
	}
	var contract corecontract.CancelUserRunResponse
	if err := decodeStrictCoreJSON(raw, &contract); err != nil {
		return CancelRunResult{}, fmt.Errorf("decode core cancel response: %w", err)
	}
	result := CancelRunResult{
		WorkspaceID: contract.WorkspaceID, SessionID: contract.SessionID, RunID: contract.RunID,
		Status: contract.Status, RunVersion: contract.RunVersion,
		Terminal: contract.Terminal, Changed: contract.Changed,
	}
	if result.WorkspaceID != request.WorkspaceID || result.RunID != request.RunID ||
		validateCanonicalUUID("sessionId", result.SessionID) != nil || result.RunVersion < 1 || result.RunVersion >= 1<<53-1 ||
		!validCancelRunStatus(result.Status) || result.Terminal != terminalCancelRunStatus(result.Status) {
		return CancelRunResult{}, errors.New("core cancel response escaped or contradicted the requested run scope")
	}
	return result, nil
}

func (backend *CoreRunBackend) DecideApproval(ctx context.Context, request DecideApprovalRequest) (DecideApprovalResult, error) {
	body, err := json.Marshal(corecontract.DecideUserApprovalRequest{
		Decision: request.Decision, Nonce: request.Nonce, ContextDigest: request.ContextDigest,
		ExpectedApprovalVersion: request.ExpectedApprovalVersion,
	})
	if err != nil {
		return DecideApprovalResult{}, fmt.Errorf("encode core approval decision request: %w", err)
	}
	endpoint := backend.endpoint(corecontract.DecideUserApprovalPath(request.WorkspaceID, request.ApprovalID))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return DecideApprovalResult{}, fmt.Errorf("construct core approval decision request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+request.BearerToken)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	response, raw, err := backend.do(httpRequest)
	if err != nil {
		return DecideApprovalResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return DecideApprovalResult{}, decodePublicCoreError(response.StatusCode, raw)
	}
	var result corecontract.DecideUserApprovalResponse
	if err := decodeStrictCoreJSON(raw, &result); err != nil {
		return DecideApprovalResult{}, fmt.Errorf("decode core approval decision response: %w", err)
	}
	if err := validateCoreApprovalDecisionResult(result, request); err != nil {
		return DecideApprovalResult{}, fmt.Errorf("validate core approval decision response: %w", err)
	}
	return result, nil
}

func (backend *CoreRunBackend) resolveRunCursor(ctx context.Context, bearer, workspaceID, sessionID, runID, after string) (corecontract.ReadUserRunEventsResponse, *CursorExpiredError, error) {
	endpoint := backend.endpoint(corecontract.ReadUserRunEventsPath(workspaceID, runID))
	query := endpoint.Query()
	query.Set("after", after)
	query.Set("limit", "0")
	query.Set("waitMs", "0")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return corecontract.ReadUserRunEventsResponse{}, nil, fmt.Errorf("construct core cursor resolution request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Accept", "application/json")
	response, raw, err := backend.do(request)
	if err != nil {
		return corecontract.ReadUserRunEventsResponse{}, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusGone {
		expired, err := decodeCoreCursorExpired(raw, ProjectionScope{
			WorkspaceID: workspaceID, SessionID: sessionID, RunID: runID,
		})
		return corecontract.ReadUserRunEventsResponse{}, expired, err
	}
	if response.StatusCode != http.StatusOK {
		return corecontract.ReadUserRunEventsResponse{}, nil, decodePublicCoreError(response.StatusCode, raw)
	}
	var resolved corecontract.ReadUserRunEventsResponse
	if err := decodeStrictCoreJSON(raw, &resolved); err != nil {
		return corecontract.ReadUserRunEventsResponse{}, nil, fmt.Errorf("decode core cursor resolution response: %w", err)
	}
	if resolved.Events == nil || resolved.EventCursors == nil || len(resolved.Events) != 0 || len(resolved.EventCursors) != 0 ||
		resolved.NextCursor == "" || resolved.LastEventSequence < 1 {
		return corecontract.ReadUserRunEventsResponse{}, nil, errors.New("core returned an invalid cursor resolution response")
	}
	return resolved, nil, nil
}

func decodeCoreCursorExpired(raw []byte, expected ProjectionScope) (*CursorExpiredError, error) {
	var expired corecontract.UserRunCursorExpiredResponse
	if err := decodeStrictCoreJSON(raw, &expired); err != nil || expired.Code != "cursor_expired" || expired.Message == "" {
		return nil, errors.New("core returned an invalid cursor-expired response")
	}
	if expired.Snapshot.WorkspaceID != expected.WorkspaceID || expired.Snapshot.SessionID != expected.SessionID ||
		expired.Snapshot.RunID != expected.RunID || expired.Snapshot.Status == "" || expired.Snapshot.RunVersion < 1 ||
		expired.Snapshot.LastEventSequence != expired.LastEventSequence || expired.LastEventSequence < 1 ||
		expired.Snapshot.UpdatedAt.IsZero() {
		return nil, errors.New("core cursor-expired snapshot escaped or contradicted the requested projection scope")
	}
	if err := validateCursor("core rebase cursor", expired.RebaseCursor); err != nil {
		return nil, err
	}
	var snapshot any
	if err := decodeStrictCoreJSON(expired.Snapshot.State, &snapshot); err != nil {
		return nil, fmt.Errorf("decode authorized cursor snapshot: %w", err)
	}
	if _, ok := snapshot.(map[string]any); !ok {
		return nil, errors.New("authorized cursor snapshot state is not an object")
	}
	return &CursorExpiredError{
		Snapshot: snapshot, RebaseCursor: expired.RebaseCursor, LastEventSequence: expired.LastEventSequence,
	}, nil
}

func (backend *CoreRunBackend) do(request *http.Request) (*http.Response, []byte, error) {
	return backend.doBounded(request, maxCoreRunResponseBytes)
}

func (backend *CoreRunBackend) doBounded(request *http.Request, maximumBytes int64) (*http.Response, []byte, error) {
	if maximumBytes <= 0 || maximumBytes > maxCoreRunResponseBytes {
		return nil, nil, errors.New("core response size bound is invalid")
	}
	response, err := backend.httpClient.Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("execute core run request: %w", err)
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		response.Body.Close()
		return nil, nil, errors.New("core run response is not application/json")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil {
		response.Body.Close()
		return nil, nil, fmt.Errorf("read core run response: %w", err)
	}
	if int64(len(raw)) > maximumBytes {
		response.Body.Close()
		return nil, nil, errors.New("core run response exceeds size limit")
	}
	return response, raw, nil
}

func (backend *CoreRunBackend) endpoint(path string) url.URL {
	endpoint := *backend.baseURL
	endpoint.Path = path
	return endpoint
}

func decodePublicCoreError(status int, raw []byte) error {
	var response corecontract.PublicErrorResponse
	if err := decodeStrictCoreJSON(raw, &response); err != nil || !validCorePublicError(response) {
		return fmt.Errorf("core run API returned HTTP %d with an invalid error envelope", status)
	}
	return &BackendHTTPError{
		Status: status, Code: response.Code, Message: response.Message, CurrentRunID: response.CurrentRunID,
	}
}

func validCorePublicError(response corecontract.PublicErrorResponse) bool {
	if len(response.Code) < 1 || len(response.Code) > 128 || len(response.Message) < 1 || len(response.Message) > 1024 ||
		!utf8.ValidString(response.Message) || strings.ContainsAny(response.Message, "\x00\r\n") {
		return false
	}
	for _, character := range []byte(response.Code) {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return response.CurrentRunID == "" || validateCanonicalUUID("currentRunId", response.CurrentRunID) == nil
}

func decodeStrictCoreJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("additional JSON value")
		}
		return err
	}
	return nil
}

func coreRunLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

var _ RunBackend = (*CoreRunBackend)(nil)
var _ RunCommandBackend = (*CoreRunBackend)(nil)
var _ ApprovalCommandBackend = (*CoreRunBackend)(nil)

func validateCoreApprovalDecisionResult(result corecontract.DecideUserApprovalResponse, request DecideApprovalRequest) error {
	approval := result.Approval
	if result.WorkspaceID != request.WorkspaceID || approval.ApprovalID != request.ApprovalID ||
		result.ExecutionID != approval.ExecutionID || approval.Nonce != request.Nonce ||
		approval.ContextDigest != request.ContextDigest {
		return errors.New("approval decision response escaped or contradicted the requested authority scope")
	}
	for field, value := range map[string]string{
		"executionId": approval.ExecutionID, "runId": approval.RunID,
		"runAttemptId": approval.RunAttemptID, "approvalId": approval.ApprovalID, "nonce": approval.Nonce,
	} {
		if err := validateCanonicalUUID(field, value); err != nil {
			return err
		}
	}
	if approval.RunAttemptGeneration < 1 || approval.RunAttemptGeneration >= 1<<53-1 ||
		approval.Version < request.ExpectedApprovalVersion || approval.Version >= 1<<53-1 ||
		result.ExecutionVersion < 1 || result.ExecutionVersion >= 1<<53-1 ||
		approval.RequesterID == "" || len(approval.RequesterID) > 256 || approval.ExpiresAt.IsZero() ||
		approval.CreatedAt.IsZero() || approval.UpdatedAt.IsZero() {
		return errors.New("approval decision response contains invalid bounded state")
	}
	if !validApprovalStatus(approval.Status) || !validApprovalExecutionStatus(result.ExecutionStatus) ||
		!approvalDecisionMatchesStatus(approval.Status, approval.Decision, approval.ApproverID) ||
		!approvalExecutionStatusMatches(approval.Status, result.ExecutionStatus) {
		return errors.New("approval decision response contains contradictory status")
	}
	if request.Decision == "approve" {
		if !((approval.Decision == "approve" && (approval.Status == "approved" || approval.Status == "consumed")) ||
			(approval.Decision == "" && approval.Status == "expired")) {
			return errors.New("approval decision response does not contain the requested approve outcome")
		}
	} else if request.Decision == "deny" {
		if !((approval.Decision == "deny" && approval.Status == "denied") ||
			(approval.Decision == "" && approval.Status == "expired")) {
			return errors.New("approval decision response does not contain the requested deny outcome")
		}
	} else {
		return errors.New("approval decision request contains an unsupported decision")
	}
	return nil
}

func validApprovalStatus(status string) bool {
	switch status {
	case "pending", "approved", "denied", "expired", "cancelled", "consumed":
		return true
	default:
		return false
	}
}

func validApprovalExecutionStatus(status string) bool {
	switch status {
	case "pending_approval", "approved", "denied", "expired", "cancelled":
		return true
	default:
		return false
	}
}

func approvalDecisionMatchesStatus(status, decision, approverID string) bool {
	switch status {
	case "pending":
		return decision == "" && approverID == ""
	case "approved", "consumed":
		return decision == "approve" && validateCanonicalUUID("approverId", approverID) == nil
	case "denied":
		return decision == "deny" && validateCanonicalUUID("approverId", approverID) == nil
	case "expired", "cancelled":
		return (decision == "" && approverID == "") ||
			(decision == "approve" && validateCanonicalUUID("approverId", approverID) == nil)
	default:
		return false
	}
}

func approvalExecutionStatusMatches(approvalStatus, executionStatus string) bool {
	switch approvalStatus {
	case "pending", "approved":
		return executionStatus == "pending_approval"
	case "consumed":
		return executionStatus == "approved"
	case "denied":
		return executionStatus == "denied"
	case "expired":
		return executionStatus == "expired"
	case "cancelled":
		return executionStatus == "cancelled"
	default:
		return false
	}
}

func validCancelRunStatus(status string) bool {
	switch status {
	case "completed", "failed", "interrupted", "cancelling", "cancelled":
		return true
	default:
		return false
	}
}

func terminalCancelRunStatus(status string) bool {
	switch status {
	case "completed", "failed", "interrupted", "cancelled":
		return true
	default:
		return false
	}
}
