package egressgateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type AuditEventIDGenerator func() (string, error)

// CoreEgressAuditClient persists the provider-neutral webhook decision in
// Core. It deliberately carries no credential material.
type CoreEgressAuditClient struct {
	baseURL    *url.URL
	httpClient *http.Client
	newID      AuditEventIDGenerator
}

func NewCoreEgressAuditClient(baseURL string, httpClient *http.Client) (*CoreEgressAuditClient, error) {
	return newCoreEgressAuditClient(baseURL, httpClient, randomAuditEventID)
}

func newCoreEgressAuditClient(baseURL string, httpClient *http.Client, newID AuditEventIDGenerator) (*CoreEgressAuditClient, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.RawPath != "" || parsed.Opaque != "" || parsed.ForceQuery || (parsed.Path != "" && parsed.Path != "/") ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("v2 Core audit URL must be a canonical HTTP(S) origin")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext v2 Core audit URL is allowed only on loopback")
	}
	if httpClient == nil || newID == nil {
		return nil, errors.New("v2 Core audit HTTP client and event ID generator are required")
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	parsed.Path = ""
	return &CoreEgressAuditClient{baseURL: parsed, httpClient: &copyClient, newID: newID}, nil
}

func (client *CoreEgressAuditClient) RecordEgressDecision(ctx context.Context, record AuditRecord) error {
	if client == nil || client.baseURL == nil || client.httpClient == nil || client.newID == nil || ctx == nil {
		return errors.New("v2 Core egress audit client is not configured")
	}
	eventID, err := client.newID()
	if err != nil {
		return err
	}
	command := corecontract.RecordEgressCredentialAuditRequest{
		EventID: eventID, At: record.At.UTC(), CapabilityID: record.CapabilityID,
		Operation: corecontract.EgressCredentialOperation{WorkspaceID: record.WorkspaceID, SessionID: record.SessionID,
			ActorID: record.ActorID, EnvironmentID: record.EnvironmentID, RunID: record.RunID, RunAttemptID: record.RunAttemptID,
			RunAttemptGeneration: record.RunAttemptGeneration, ExecutionID: record.ExecutionID, OperationID: record.OperationID,
			SandboxID: record.SandboxID, TargetGeneration: record.TargetGeneration},
		ProviderKind: record.ProviderKind, BindingID: record.BindingID, AuthorityVersion: record.AuthorityVersion,
		CredentialVersion: record.CredentialVersion,
		TAEPSM:            record.PSM,
		Host:              record.Host, Path: record.Path, Method: record.Method, Decision: record.Decision, ReasonCode: record.ReasonCode,
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(command); err != nil || raw.Len() > maximumCoreCredentialResponseBytes {
		return errors.New("encode bounded v2 egress audit command")
	}
	endpoint := *client.baseURL
	endpoint.Path = corecontract.RecordEgressCredentialAuditPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw.Bytes()))
	if err != nil {
		return errors.New("construct v2 egress audit command")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return errors.New("execute v2 egress audit command")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumCoreCredentialResponseBytes+1))
	if err != nil || len(body) > maximumCoreCredentialResponseBytes {
		return errors.New("read bounded v2 egress audit response")
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" || response.Header.Get("Cache-Control") != "no-store" {
		return errors.New("v2 egress audit response has unsafe metadata")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("v2 egress audit returned HTTP %d", response.StatusCode)
	}
	var result corecontract.RecordEgressCredentialAuditResponse
	if err := decodeCoreCredentialJSON(body, &result); err != nil || !result.Recorded {
		return errors.New("v2 egress audit was not durably recorded")
	}
	return nil
}

var _ AuditSink = (*CoreEgressAuditClient)(nil)

func randomAuditEventID() (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded), nil
}

// Ensure time remains part of the public audit contract even if a compiler
// elides the UTC conversion in a future refactor.
var _ = time.Time{}
