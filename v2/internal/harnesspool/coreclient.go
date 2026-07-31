// Package harnesspool contains the controller-side boundaries for claiming
// and supervising one run attempt. It never links coredb or writes PostgreSQL.
package harnesspool

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	maxCoreCommandRequestBytes  = 18 * 1024 * 1024
	maxCoreCommandResponseBytes = 512 * 1024
)

type CoreClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

type CoreCommandError struct {
	HTTPStatus        int
	Code              string
	Message           string
	CurrentVersion    int64
	CurrentGeneration int64
}

func (err *CoreCommandError) Error() string {
	if err == nil {
		return "<nil>"
	}
	message := strings.TrimSpace(err.Message)
	if message == "" {
		message = err.Code
	}
	return fmt.Sprintf("core command %s: %s", err.Code, message)
}

type TransitionRecord struct {
	EventID            string
	ProducerInstanceID string
	ProducerSeq        int64
	OutboxID           string
}

type Run struct {
	RunID                    string
	WorkspaceID              string
	SessionID                string
	ActorID                  string
	Status                   string
	CurrentAttemptGeneration int64
	NextEventSeq             int64
	Version                  int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type RunAttempt struct {
	RunAttemptID  string
	RunID         string
	Generation    int64
	Status        string
	TurnStartedAt *time.Time
	HolderID      string
	Version       int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Lease struct {
	HolderID   string
	Generation int64
	ExpiresAt  time.Time
	AcquiredAt time.Time
	RenewedAt  time.Time
}

type ClaimRunAttemptRequest struct {
	RunID              string
	RunAttemptID       string
	HolderID           string
	ExpectedRunVersion int64
	LeaseTTL           time.Duration
	Record             TransitionRecord
}

type ClaimRunAttemptResult struct {
	Run          Run
	RunAttempt   RunAttempt
	SessionLease Lease
	AttemptLease Lease
	Created      bool
	Reclaimed    bool
}

type RenewRunAttemptRequest struct {
	SessionID            string
	RunID                string
	RunAttemptID         string
	HolderID             string
	RunAttemptGeneration int64
	LeaseTTL             time.Duration
}

type RenewRunAttemptResult struct {
	SessionLease Lease
	AttemptLease Lease
}

type MarkTurnAcceptedRequest struct {
	RunID                     string
	RunAttemptID              string
	HolderID                  string
	RunAttemptGeneration      int64
	ExpectedRunVersion        int64
	ExpectedRunAttemptVersion int64
	Record                    TransitionRecord
}

type MarkTurnAcceptedResult struct {
	Run        Run
	RunAttempt RunAttempt
	Changed    bool
}

type EventObjectPointer struct {
	ObjectID  string
	SHA256    [32]byte
	Size      int64
	MediaType string
}

type AttemptEvent struct {
	EventID            string
	ProducerInstanceID string
	ProducerSeq        int64
	Source             string
	Kind               string
	SchemaVersion      int
	Payload            json.RawMessage
	Object             *EventObjectPointer
}

type AppendAttemptEventsRequest struct {
	RunID                string
	RunAttemptID         string
	HolderID             string
	RunAttemptGeneration int64
	OutboxID             string
	Events               []AttemptEvent
}

type AppendedAttemptEvent struct {
	EventID            string
	ProducerInstanceID string
	ProducerSeq        int64
	RunSeq             int64
	Duplicate          bool
}

type AppendAttemptEventsResult struct {
	Events   []AppendedAttemptEvent
	NewCount int
}

func NewCoreClient(baseURL string, httpClient *http.Client) (*CoreClient, error) {
	if httpClient == nil {
		return nil, errors.New("core HTTP client is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse core base URL: %w", err)
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("core base URL must be an absolute HTTP(S) origin without userinfo, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("core base URL must not contain a path")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext core base URL is allowed only on loopback")
	}
	parsed.Path = ""
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CoreClient{baseURL: parsed, httpClient: &clientCopy}, nil
}

func (client *CoreClient) ClaimRunAttempt(ctx context.Context, request ClaimRunAttemptRequest) (ClaimRunAttemptResult, error) {
	contractRequest := corecontract.ClaimRunAttemptRequest{
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		ExpectedRunVersion: request.ExpectedRunVersion, LeaseTTLMillis: request.LeaseTTL.Milliseconds(),
		Record: contractTransitionRecord(request.Record),
	}
	var response corecontract.ClaimRunAttemptResponse
	if err := client.post(ctx, corecontract.ClaimRunAttemptPath, contractRequest, &response); err != nil {
		return ClaimRunAttemptResult{}, err
	}
	result := ClaimRunAttemptResult{
		Run: contractRun(response.Run), RunAttempt: contractRunAttempt(response.RunAttempt),
		SessionLease: contractLease(response.SessionLease), AttemptLease: contractLease(response.AttemptLease),
		Created: response.Created, Reclaimed: response.Reclaimed,
	}
	if err := validateClaimResult(request, result); err != nil {
		return ClaimRunAttemptResult{}, fmt.Errorf("validate core claim response: %w", err)
	}
	return result, nil
}

func (client *CoreClient) RenewRunAttempt(ctx context.Context, request RenewRunAttemptRequest) (RenewRunAttemptResult, error) {
	contractRequest := corecontract.RenewRunAttemptRequest{
		SessionID: request.SessionID, RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		HolderID: request.HolderID, RunAttemptGeneration: request.RunAttemptGeneration, LeaseTTLMillis: request.LeaseTTL.Milliseconds(),
	}
	var response corecontract.RenewRunAttemptResponse
	if err := client.post(ctx, corecontract.RenewRunAttemptPath(request.RunAttemptID), contractRequest, &response); err != nil {
		return RenewRunAttemptResult{}, err
	}
	result := RenewRunAttemptResult{SessionLease: contractLease(response.SessionLease), AttemptLease: contractLease(response.AttemptLease)}
	if !sameLeaseHolder(result.SessionLease, request.HolderID, request.RunAttemptGeneration) || !sameLeaseHolder(result.AttemptLease, request.HolderID, request.RunAttemptGeneration) {
		return RenewRunAttemptResult{}, errors.New("core renew response does not match the requested holder generation")
	}
	return result, nil
}

func (client *CoreClient) MarkTurnAccepted(ctx context.Context, request MarkTurnAcceptedRequest) (MarkTurnAcceptedResult, error) {
	contractRequest := corecontract.MarkTurnAcceptedRequest{
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		RunAttemptGeneration: request.RunAttemptGeneration, ExpectedRunVersion: request.ExpectedRunVersion,
		ExpectedRunAttemptVersion: request.ExpectedRunAttemptVersion, Record: contractTransitionRecord(request.Record),
	}
	var response corecontract.MarkTurnAcceptedResponse
	if err := client.post(ctx, corecontract.MarkTurnAcceptedPath(request.RunAttemptID), contractRequest, &response); err != nil {
		return MarkTurnAcceptedResult{}, err
	}
	result := MarkTurnAcceptedResult{Run: contractRun(response.Run), RunAttempt: contractRunAttempt(response.RunAttempt), Changed: response.Changed}
	if result.Run.RunID != request.RunID || result.RunAttempt.RunID != request.RunID || result.RunAttempt.RunAttemptID != request.RunAttemptID ||
		result.Run.CurrentAttemptGeneration != request.RunAttemptGeneration || result.RunAttempt.Generation != request.RunAttemptGeneration || result.RunAttempt.HolderID != request.HolderID {
		return MarkTurnAcceptedResult{}, errors.New("core turn-accepted response does not match the requested attempt identity")
	}
	return result, nil
}

func (client *CoreClient) AppendAttemptEvents(ctx context.Context, request AppendAttemptEventsRequest) (AppendAttemptEventsResult, error) {
	contractEvents := make([]corecontract.AttemptEvent, len(request.Events))
	for index, event := range request.Events {
		contractEvents[index] = corecontract.AttemptEvent{
			EventID: event.EventID, ProducerInstanceID: event.ProducerInstanceID, ProducerSeq: event.ProducerSeq,
			Source: event.Source, Kind: event.Kind, SchemaVersion: event.SchemaVersion,
			Payload: append(json.RawMessage(nil), event.Payload...),
		}
		if event.Object != nil {
			contractEvents[index].Object = &corecontract.EventObjectPointer{
				ObjectID: event.Object.ObjectID, SHA256: hex.EncodeToString(event.Object.SHA256[:]),
				Size: event.Object.Size, MediaType: event.Object.MediaType,
			}
		}
	}
	contractRequest := corecontract.AppendAttemptEventsRequest{
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		RunAttemptGeneration: request.RunAttemptGeneration, OutboxID: request.OutboxID, Events: contractEvents,
	}
	var response corecontract.AppendAttemptEventsResponse
	if err := client.post(ctx, corecontract.AppendAttemptEventsPath(request.RunAttemptID), contractRequest, &response); err != nil {
		return AppendAttemptEventsResult{}, err
	}
	if len(response.Events) != len(request.Events) {
		return AppendAttemptEventsResult{}, errors.New("core append-events response length differs from the request")
	}
	result := AppendAttemptEventsResult{Events: make([]AppendedAttemptEvent, len(response.Events)), NewCount: response.NewCount}
	newCount := 0
	for index, event := range response.Events {
		requested := request.Events[index]
		if event.EventID != requested.EventID || event.ProducerInstanceID != requested.ProducerInstanceID || event.ProducerSeq != requested.ProducerSeq || event.RunSeq < 1 {
			return AppendAttemptEventsResult{}, errors.New("core append-events response does not preserve producer identities")
		}
		if !event.Duplicate {
			newCount++
		}
		result.Events[index] = AppendedAttemptEvent{
			EventID: event.EventID, ProducerInstanceID: event.ProducerInstanceID, ProducerSeq: event.ProducerSeq,
			RunSeq: event.RunSeq, Duplicate: event.Duplicate,
		}
	}
	if response.NewCount != newCount {
		return AppendAttemptEventsResult{}, errors.New("core append-events newCount is inconsistent with duplicate flags")
	}
	return result, nil
}

func (client *CoreClient) post(ctx context.Context, path string, command, destination any) error {
	if ctx == nil {
		return errors.New("core command context is required")
	}
	raw, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("encode core command: %w", err)
	}
	if len(raw) > maxCoreCommandRequestBytes {
		return errors.New("core command request exceeds size limit")
	}
	endpoint := *client.baseURL
	endpoint.Path = path
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("construct core command: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("execute core command: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxCoreCommandResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read core command response: %w", err)
	}
	if len(body) > maxCoreCommandResponseBytes {
		return errors.New("core command response exceeds size limit")
	}
	if response.StatusCode != http.StatusOK {
		return decodeCoreCommandError(response.StatusCode, body)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode core command response: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return fmt.Errorf("finish core command response: %w", err)
	}
	return nil
}

func decodeCoreCommandError(status int, body []byte) error {
	var response corecontract.ErrorResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || finishJSON(decoder) != nil || response.Code == "" {
		return fmt.Errorf("core command returned HTTP %d with an invalid error envelope", status)
	}
	return &CoreCommandError{
		HTTPStatus: status, Code: response.Code, Message: response.Message,
		CurrentVersion: response.CurrentVersion, CurrentGeneration: response.CurrentGeneration,
	}
}

func finishJSON(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("additional JSON value")
	}
	return err
}

func validateClaimResult(request ClaimRunAttemptRequest, result ClaimRunAttemptResult) error {
	if result.Run.RunID != request.RunID || result.RunAttempt.RunID != request.RunID || result.RunAttempt.RunAttemptID != request.RunAttemptID || result.RunAttempt.HolderID != request.HolderID {
		return errors.New("claim identity differs from request")
	}
	generation := result.Run.CurrentAttemptGeneration
	if generation < 1 || result.RunAttempt.Generation != generation || !sameLeaseHolder(result.SessionLease, request.HolderID, generation) || !sameLeaseHolder(result.AttemptLease, request.HolderID, generation) {
		return errors.New("claim generation or lease holder tuple is inconsistent")
	}
	if result.Reclaimed && !result.Created {
		return errors.New("reclaimed claim must also be newly created")
	}
	return nil
}

func sameLeaseHolder(lease Lease, holderID string, generation int64) bool {
	return lease.HolderID == holderID && lease.Generation == generation && generation > 0 &&
		!lease.ExpiresAt.IsZero() && !lease.AcquiredAt.IsZero() && !lease.RenewedAt.IsZero()
}

func contractTransitionRecord(record TransitionRecord) corecontract.TransitionRecord {
	return corecontract.TransitionRecord{
		EventID: record.EventID, ProducerInstanceID: record.ProducerInstanceID,
		ProducerSeq: record.ProducerSeq, OutboxID: record.OutboxID,
	}
}

func contractRun(source corecontract.RunState) Run {
	return Run{
		RunID: source.RunID, WorkspaceID: source.WorkspaceID, SessionID: source.SessionID, ActorID: source.ActorID,
		Status: source.Status, CurrentAttemptGeneration: source.CurrentAttemptGeneration, NextEventSeq: source.NextEventSeq,
		Version: source.Version, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt,
	}
}

func contractRunAttempt(source corecontract.RunAttemptState) RunAttempt {
	return RunAttempt{
		RunAttemptID: source.RunAttemptID, RunID: source.RunID, Generation: source.Generation, Status: source.Status,
		TurnStartedAt: source.TurnStartedAt, HolderID: source.HolderID, Version: source.Version,
		CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt,
	}
}

func contractLease(source corecontract.LeaseState) Lease {
	return Lease{
		HolderID: source.HolderID, Generation: source.Generation, ExpiresAt: source.ExpiresAt,
		AcquiredAt: source.AcquiredAt, RenewedAt: source.RenewedAt,
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
