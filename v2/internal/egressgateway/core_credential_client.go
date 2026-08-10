package egressgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/corecredentials"
)

const maximumCoreCredentialResponseBytes = 128 * 1024

// CredentialInjectionResolver is the narrow egress-authorizer boundary. The
// implementation below talks directly to the v2 Core egress contract; there
// is no intermediate credential service or credential-proxy HTTP hop.
type CredentialInjectionResolver interface {
	ResolveInjection(context.Context, corecredentials.UseRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error)
	AuthorizeProcessEnvironmentEgress(context.Context, corecontract.AuthorizeProcessEnvironmentEgressRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error)
}

// CoreCredentialClient calls the v2 Core egress contract over the existing
// egress-authorizer mTLS channel. Core owns the binding store, sealing keyring
// and provider materialization.
type CoreCredentialClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewCoreCredentialClient(baseURL string, httpClient *http.Client) (*CoreCredentialClient, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.RawPath != "" || parsed.Opaque != "" || parsed.ForceQuery || (parsed.Path != "" && parsed.Path != "/") ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("v2 Core credential URL must be a canonical HTTP(S) origin")
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext v2 Core credential URL is allowed only on loopback")
	}
	if httpClient == nil {
		return nil, errors.New("v2 Core credential HTTP client is required")
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	parsed.Path = ""
	return &CoreCredentialClient{baseURL: parsed, httpClient: &copyClient}, nil
}

func (client *CoreCredentialClient) ResolveInjection(ctx context.Context, use corecredentials.UseRequest) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
	if client == nil || client.baseURL == nil || client.httpClient == nil || ctx == nil {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("v2 Core credential client is not configured")
	}
	command := corecontract.ResolveEgressCredentialRequest{
		Placeholder: use.Placeholder,
		Operation: corecontract.EgressCredentialOperation{
			WorkspaceID: use.WorkspaceID, SessionID: use.SessionID, ActorID: use.ActorID,
			EnvironmentID: use.EnvironmentID, RunID: use.RunID, RunAttemptID: use.RunAttemptID,
			RunAttemptGeneration: use.RunAttemptGeneration, ExecutionID: use.ExecutionID,
			OperationID: use.OperationID, SandboxID: use.SandboxID, TargetGeneration: use.TargetGeneration,
		},
		ProviderKind: use.ProviderKind, BindingID: use.BindingID, AuthorityVersion: use.AuthorityVersion,
		PolicySHA256: use.PolicySHA256, TAEPSM: use.TAEPSM,
		Host: use.Host, Path: use.Path, Method: use.Method,
		Headers: cloneOriginalHeaders(use.Headers),
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(command); err != nil || raw.Len() > maximumCoreCredentialResponseBytes {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("encode bounded v2 Core credential request")
	}
	endpoint := *client.baseURL
	endpoint.Path = corecontract.ResolveEgressCredentialPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw.Bytes()))
	if err != nil {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("construct v2 Core credential request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("execute v2 Core credential request")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumCoreCredentialResponseBytes+1))
	if err != nil || len(body) > maximumCoreCredentialResponseBytes {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("read bounded v2 Core credential response")
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" || response.Header.Get("Cache-Control") != "no-store" {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("v2 Core credential response has unsafe metadata")
	}
	if response.StatusCode != http.StatusOK {
		var failure corecontract.ErrorResponse
		if decodeCoreCredentialJSON(body, &failure) != nil || failure.Code == "" {
			return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("v2 Core credential response is invalid")
		}
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, fmt.Errorf("v2 Core credential denied: %s", failure.Code)
	}
	var result corecontract.ResolveEgressCredentialResponse
	if err := decodeCoreCredentialJSON(body, &result); err != nil {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("decode v2 Core credential response")
	}
	mutation := corecredentials.HeaderMutation{Headers: result.Headers}
	if err := corecredentials.ValidateClosedHeaderMutation(mutation); err != nil || result.ProviderKind != use.ProviderKind ||
		result.BindingID != use.BindingID || result.AuthorityVersion != use.AuthorityVersion || result.CredentialVersion < 1 || result.ResolvedAt.IsZero() {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("v2 Core returned an out-of-scope credential mutation")
	}
	metadata := corecredentials.BindingMetadata{ID: result.BindingID, Kind: result.ProviderKind,
		AuthorityVersion: result.AuthorityVersion, CredentialVersion: result.CredentialVersion}
	return mutation, corecredentials.ResolveResult{
		ProviderKind: result.ProviderKind, Binding: metadata, AuthorityVersion: result.AuthorityVersion,
		CredentialVersion: result.CredentialVersion, AccessExpiresAt: result.AccessExpiresAt, ResolvedAt: result.ResolvedAt,
	}, nil
}

func (client *CoreCredentialClient) AuthorizeProcessEnvironmentEgress(
	ctx context.Context,
	command corecontract.AuthorizeProcessEnvironmentEgressRequest,
) (corecredentials.HeaderMutation, corecredentials.ResolveResult, error) {
	if client == nil || client.baseURL == nil || client.httpClient == nil || ctx == nil {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("v2 Core credential client is not configured")
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(command); err != nil || raw.Len() > maximumCoreCredentialResponseBytes {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("encode bounded v2 Core process environment request")
	}
	endpoint := *client.baseURL
	endpoint.Path = corecontract.AuthorizeProcessEnvironmentEgressPath
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw.Bytes()))
	if err != nil {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("construct v2 Core process environment request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("execute v2 Core process environment request")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumCoreCredentialResponseBytes+1))
	if err != nil || len(body) > maximumCoreCredentialResponseBytes {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("read bounded v2 Core process environment response")
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" || response.Header.Get("Cache-Control") != "no-store" {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("v2 Core process environment response has unsafe metadata")
	}
	if response.StatusCode != http.StatusOK {
		var failure corecontract.ErrorResponse
		if decodeCoreCredentialJSON(body, &failure) != nil || failure.Code == "" {
			return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("v2 Core process environment response is invalid")
		}
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, fmt.Errorf("v2 Core process environment egress denied: %s", failure.Code)
	}
	var result corecontract.ResolveEgressCredentialResponse
	if err := decodeCoreCredentialJSON(body, &result); err != nil {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("decode v2 Core process environment response")
	}
	mutation := corecredentials.HeaderMutation{Headers: result.Headers}
	if err := corecredentials.ValidateClosedHeaderMutation(mutation); err != nil ||
		result.ProviderKind != command.ProviderKind || result.BindingID != command.BindingID ||
		result.AuthorityVersion != command.AuthorityVersion || result.CredentialVersion != command.CredentialVersion ||
		result.ResolvedAt.IsZero() {
		return corecredentials.HeaderMutation{}, corecredentials.ResolveResult{}, errors.New("v2 Core returned an out-of-scope process environment mutation")
	}
	metadata := corecredentials.BindingMetadata{ID: result.BindingID, Kind: result.ProviderKind,
		AuthorityVersion: result.AuthorityVersion, CredentialVersion: result.CredentialVersion}
	return mutation, corecredentials.ResolveResult{
		ProviderKind: result.ProviderKind, Binding: metadata, AuthorityVersion: result.AuthorityVersion,
		CredentialVersion: result.CredentialVersion, AccessExpiresAt: result.AccessExpiresAt, ResolvedAt: result.ResolvedAt,
	}, nil
}

func decodeCoreCredentialJSON(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("v2 Core credential response contains trailing JSON")
		}
		return err
	}
	return nil
}

var _ CredentialInjectionResolver = (*CoreCredentialClient)(nil)

// Probe validates the Core mTLS route without sending a credential-bearing
// command. The endpoint intentionally answers GET with a canonical 404.
func (client *CoreCredentialClient) Probe(ctx context.Context) error {
	if client == nil || client.baseURL == nil || client.httpClient == nil || ctx == nil {
		return errors.New("v2 Core credential client is not configured")
	}
	endpoint := *client.baseURL
	endpoint.Path = corecontract.ResolveEgressCredentialPath
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumCoreCredentialResponseBytes+1))
	if err != nil || len(body) > maximumCoreCredentialResponseBytes {
		return errors.New("read bounded v2 Core credential probe response")
	}
	mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusNotFound || mediaErr != nil || mediaType != "application/json" || response.Header.Get("Cache-Control") != "no-store" {
		return errors.New("v2 Core credential probe returned an unsafe response")
	}
	var failure corecontract.ErrorResponse
	if err := decodeCoreCredentialJSON(body, &failure); err != nil || failure.Code != "not_found" {
		return errors.New("v2 Core credential probe returned an invalid error contract")
	}
	return nil
}
