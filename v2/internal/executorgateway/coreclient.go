package executorgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const maxCoreCommandResponseBytes = 512 * 1024

type CoreConnectionClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// CoreCommandError preserves the stable core rejection code and retry hints.
// Callers must branch on Code, not on the human-readable Message.
type CoreCommandError struct {
	HTTPStatus        int
	Code              string
	Message           string
	CurrentVersion    int64
	CurrentGeneration int64
	cause             error
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

func (err *CoreCommandError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

type RegisteredEnvironment struct {
	EnvironmentID        string
	ExecutorID           string
	RootDescriptor       json.RawMessage
	Platform             string
	OuterProfileVersion  string
	InsecureDev          bool
	EnvironmentVersion   int64
	ConnectionGeneration int64
}

func NewCoreConnectionClient(baseURL string, httpClient *http.Client) (*CoreConnectionClient, error) {
	if httpClient == nil {
		return nil, errors.New("core HTTP client is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse core base URL: %w", err)
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.ForceQuery {
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
	return &CoreConnectionClient{baseURL: parsed, httpClient: &clientCopy}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func (client *CoreConnectionClient) AcquireConnection(ctx context.Context, request AcquireConnectionRequest) (ConnectionHolder, error) {
	contractRequest := corecontract.AcquireExecutorConnectionRequest{
		ExecutorID:               request.ExecutorID,
		ConnectionID:             request.ConnectionID,
		SessionID:                request.SessionID,
		GatewayInstanceID:        request.GatewayInstanceID,
		AgentxVersion:            request.AgentxVersion,
		RuntimeManifestSHA256:    hex.EncodeToString(request.RuntimeManifestSHA256[:]),
		ExecProtocolSourceSHA256: hex.EncodeToString(request.ExecProtocolSourceSHA256[:]),
		Environments:             contractEnvironmentDeclarations(request.Environments),
		ConnectionLeaseTTLMillis: request.LeaseTTL.Milliseconds(),
	}
	var response corecontract.ExecutorConnectionResponse
	if err := client.post(ctx, corecontract.AcquireExecutorConnectionPath, contractRequest, &response, http.StatusOK); err != nil {
		return ConnectionHolder{}, err
	}
	return gatewayHolder(response.Holder), nil
}

func (client *CoreConnectionClient) RenewConnection(ctx context.Context, holder ConnectionHolder, leaseTTL time.Duration) (ConnectionHolder, error) {
	request := corecontract.RenewExecutorConnectionRequest{
		Holder:                   contractHolder(holder),
		ConnectionLeaseTTLMillis: leaseTTL.Milliseconds(),
	}
	var response corecontract.ExecutorConnectionResponse
	if err := client.post(ctx, corecontract.RenewExecutorConnectionPath(holder.ExecutorID), request, &response, http.StatusOK); err != nil {
		return ConnectionHolder{}, err
	}
	return gatewayHolder(response.Holder), nil
}

func (client *CoreConnectionClient) ActivateConnection(ctx context.Context, request ActivateConnectionRequest) (ConnectionHolder, error) {
	contractRequest := corecontract.ActivateExecutorConnectionRequest{
		Holder:       contractHolder(request.Holder),
		Environments: contractEnvironmentDeclarations(request.Environments),
	}
	var response corecontract.ExecutorConnectionResponse
	if err := client.post(ctx, corecontract.ActivateExecutorConnectionPath(request.Holder.ExecutorID), contractRequest, &response, http.StatusOK); err != nil {
		return ConnectionHolder{}, err
	}
	return gatewayHolder(response.Holder), nil
}

func (client *CoreConnectionClient) FenceConnection(ctx context.Context, holder ConnectionHolder) error {
	request := corecontract.FenceExecutorConnectionRequest{Holder: contractHolder(holder)}
	return client.post(ctx, corecontract.FenceExecutorConnectionPath(holder.ExecutorID), request, nil, http.StatusNoContent)
}

func (client *CoreConnectionClient) CompleteExecutorEnrollment(
	ctx context.Context,
	bearer string,
	expectedExecutorID string,
	command corecontract.CompleteExecutorEnrollmentRequest,
) (corecontract.CompleteExecutorEnrollmentResponse, error) {
	if err := validateSensitiveCoreBearer(bearer, "executor enrollment"); err != nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, err
	}
	if err := validateRegistryIdentity("expected executor ID", expectedExecutorID); err != nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, err
	}
	var response corecontract.CompleteExecutorEnrollmentResponse
	if err := client.postWithPolicy(
		ctx, corecontract.CompleteExecutorEnrollmentPath, command, &response,
		http.StatusOK, bearer, true,
		map[string]string{corecontract.ExpectedExecutorIDHeader: expectedExecutorID},
	); err != nil {
		return corecontract.CompleteExecutorEnrollmentResponse{}, err
	}
	return response, nil
}

func (client *CoreConnectionClient) AuthorizeExecutorConnection(
	ctx context.Context,
	bearer string,
) (ExecutorMachineAuthority, error) {
	if err := validateSensitiveCoreBearer(bearer, "executor OAuth"); err != nil {
		return ExecutorMachineAuthority{}, err
	}
	var response corecontract.AuthorizeExecutorConnectionResponse
	if err := client.postWithPolicy(
		ctx, corecontract.AuthorizeExecutorConnectionPath, nil, &response,
		http.StatusOK, bearer, true, nil,
	); err != nil {
		return ExecutorMachineAuthority{}, err
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(response.MachinePublicKeyEd25519)
	if err != nil || len(publicKey) != ed25519.PublicKeySize ||
		base64.RawURLEncoding.EncodeToString(publicKey) != response.MachinePublicKeyEd25519 {
		return ExecutorMachineAuthority{}, errors.New("Core executor authorization returned an invalid machine public key")
	}
	machineDigest, err := hex.DecodeString(response.MachineKeySHA256)
	if err != nil || len(machineDigest) != sha256.Size || hex.EncodeToString(machineDigest) != response.MachineKeySHA256 {
		return ExecutorMachineAuthority{}, errors.New("Core executor authorization returned an invalid machine key fingerprint")
	}
	result := ExecutorMachineAuthority{
		ExecutorID: response.ExecutorID, WorkspaceID: response.WorkspaceID, OAuthClientID: response.OAuthClientID,
		MachinePublicKeyEd25519: append(ed25519.PublicKey(nil), publicKey...),
		ExecutorVersion:         response.ExecutorVersion, TokenExpiresAt: response.TokenExpiresAt, AuthorizedAt: response.AuthorizedAt,
	}
	copy(result.MachineKeySHA256[:], machineDigest)
	if err := validateMachineAuthority(result, response.ExecutorID); err != nil {
		return ExecutorMachineAuthority{}, err
	}
	return result, nil
}

func validateSensitiveCoreBearer(bearer, kind string) error {
	if bearer == "" || len(bearer) > maximumExecutorBearerBytes || strings.TrimSpace(bearer) != bearer ||
		strings.ContainsAny(bearer, " \t\x00\r\n") {
		return fmt.Errorf("%s bearer is invalid", kind)
	}
	return nil
}

func (client *CoreConnectionClient) ListEnvironments(ctx context.Context, workspaceID, executorID string) ([]RegisteredEnvironment, error) {
	request := corecontract.ListExecutorEnvironmentsRequest{WorkspaceID: workspaceID, ExecutorID: executorID}
	var response corecontract.ListExecutorEnvironmentsResponse
	if err := client.post(ctx, corecontract.ListExecutorEnvironmentsPath, request, &response, http.StatusOK); err != nil {
		return nil, err
	}
	if len(response.Environments) > 256 {
		return nil, errors.New("core returned more than 256 executor environments")
	}
	result := make([]RegisteredEnvironment, len(response.Environments))
	for index, environment := range response.Environments {
		result[index] = RegisteredEnvironment{
			EnvironmentID:        environment.EnvironmentID,
			ExecutorID:           environment.ExecutorID,
			RootDescriptor:       append(json.RawMessage(nil), environment.RootDescriptor...),
			Platform:             environment.Platform,
			OuterProfileVersion:  environment.OuterProfileVersion,
			InsecureDev:          environment.InsecureDev,
			EnvironmentVersion:   environment.EnvironmentVersion,
			ConnectionGeneration: environment.ConnectionGeneration,
		}
	}
	return result, nil
}

func (client *CoreConnectionClient) AuthorizeExecutorRunCapability(
	ctx context.Context,
	request ExecutorRunCapabilityAuthorizationRequest,
) (ExecutorRunCapabilityAuthorization, error) {
	if request.Token == "" || strings.TrimSpace(request.Token) != request.Token ||
		strings.ContainsAny(request.Token, "\r\n") || len(request.Token) > 16*1024 {
		return ExecutorRunCapabilityAuthorization{}, errors.New("executor run capability bearer is invalid")
	}
	contractRequest := corecontract.AuthorizeExecutorRunCapabilityRequest{
		ExecutorID: request.ExecutorID, ToolCatalogDigest: request.ToolCatalogDigest,
	}
	var response corecontract.AuthorizeRunCapabilityResponse
	if err := client.postWithPolicy(
		ctx, corecontract.AuthorizeExecutorRunCapabilityPath, contractRequest, &response,
		http.StatusOK, request.Token, true, nil,
	); err != nil {
		// The bearer is deliberately excluded from the JSON body, but a
		// transport or Core error envelope is still untrusted diagnostic text.
		// Do not let either reflect the capability into a caller-visible error.
		return ExecutorRunCapabilityAuthorization{}, errors.New("Core executor capability live authorization failed")
	}
	result := ExecutorRunCapabilityAuthorization{
		CapabilityID: response.CapabilityID, Audience: response.Audience,
		RunID: response.RunID, RunAttemptID: response.RunAttemptID,
		RunAttemptGeneration: response.RunAttemptGeneration,
		RunVersion:           resultSafeVersion(response.RunVersion), RunAttemptVersion: resultSafeVersion(response.RunAttemptVersion),
		AuthorizedAt: response.AuthorizedAt,
	}
	versionsMatch := result.RunVersion == request.ExpectedRunVersion &&
		result.RunAttemptVersion == request.ExpectedRunAttemptVersion
	preTurnVersions := result.RunVersion > 0 && result.RunAttemptVersion > 0 &&
		result.RunVersion+1 == request.ExpectedRunVersion &&
		result.RunAttemptVersion+1 == request.ExpectedRunAttemptVersion
	if result.CapabilityID != request.CapabilityID || result.Audience != "executor-mcp" ||
		result.RunID != request.RunID || result.RunAttemptID != request.RunAttemptID ||
		result.RunAttemptGeneration != request.RunAttemptGeneration || result.AuthorizedAt.IsZero() ||
		(!versionsMatch && !preTurnVersions) {
		return ExecutorRunCapabilityAuthorization{}, errors.New("Core executor capability authorization response is inconsistent")
	}
	return result, nil
}

func resultSafeVersion(value int64) int64 {
	if value < 1 || value > 1<<53-1 {
		return 0
	}
	return value
}

func (client *CoreConnectionClient) post(ctx context.Context, path string, command, destination any, wantStatus int) error {
	return client.postWithPolicy(ctx, path, command, destination, wantStatus, "", false, nil)
}

func (client *CoreConnectionClient) postWithPolicy(
	ctx context.Context,
	path string,
	command, destination any,
	wantStatus int,
	bearer string,
	requireNoStore bool,
	extraHeaders map[string]string,
) error {
	var raw []byte
	var err error
	if command != nil {
		raw, err = json.Marshal(command)
		if err != nil {
			return fmt.Errorf("encode core command: %w", err)
		}
	}
	endpoint := *client.baseURL
	endpoint.Path = path
	var requestBody io.Reader
	if command != nil {
		requestBody = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), requestBody)
	if err != nil {
		return fmt.Errorf("construct core command: %w", err)
	}
	if command != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	for name, value := range extraHeaders {
		if name == "" || value == "" || strings.EqualFold(name, "Authorization") || strings.ContainsAny(name+value, "\x00\r\n") {
			return errors.New("construct core command with invalid additional header")
		}
		request.Header.Set(name, value)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("execute core command: %w", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxCoreCommandResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read core command response: %w", err)
	}
	if len(body) > maxCoreCommandResponseBytes {
		return errors.New("core command response exceeds size limit")
	}
	if requireNoStore && response.Header.Get("Cache-Control") != "no-store" {
		return errors.New("sensitive Core response is missing Cache-Control no-store")
	}
	if destination != nil || response.StatusCode != wantStatus {
		mediaType, _, mediaTypeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if mediaTypeErr != nil || mediaType != "application/json" {
			return fmt.Errorf("core command returned HTTP %d with a non-JSON response", response.StatusCode)
		}
	}
	if response.StatusCode != wantStatus {
		return decodeCoreCommandError(response.StatusCode, body)
	}
	if destination == nil {
		if len(bytes.TrimSpace(body)) != 0 {
			return errors.New("core no-content response unexpectedly contains a body")
		}
		return nil
	}
	if err := decodeStrictCoreCommandJSON(body, destination); err != nil {
		return fmt.Errorf("decode core command response: %w", err)
	}
	return nil
}

func decodeCoreCommandError(status int, body []byte) error {
	var response corecontract.ErrorResponse
	if err := decodeStrictCoreCommandJSON(body, &response); err != nil || response.Code == "" {
		return fmt.Errorf("core command returned HTTP %d with an invalid error envelope", status)
	}
	commandError := &CoreCommandError{
		HTTPStatus:        status,
		Code:              response.Code,
		Message:           strings.TrimSpace(response.Message),
		CurrentVersion:    response.CurrentVersion,
		CurrentGeneration: response.CurrentGeneration,
	}
	if response.Code == "connection_fenced" {
		commandError.cause = ErrConnectionFenced
	}
	return commandError
}

func decodeStrictCoreCommandJSON(body []byte, destination any) error {
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 65_536
	limits.MaxJSONDepth = 256
	value, canonical, err := braincatalog.DecodeCanonicalJSON(body, maxCoreCommandResponseBytes, limits)
	if err != nil {
		return err
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New("core command response is not a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return finishCoreJSON(decoder)
}

func finishCoreJSON(decoder *json.Decoder) error {
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

func contractEnvironmentDeclarations(source []EnvironmentDeclaration) []corecontract.EnvironmentDeclaration {
	converted := make([]corecontract.EnvironmentDeclaration, len(source))
	for index, environment := range source {
		converted[index] = corecontract.EnvironmentDeclaration{
			ID:                  environment.ID,
			Platform:            environment.Platform,
			CodexRelease:        environment.CodexRelease,
			CodexCommit:         environment.CodexCommit,
			CodexSHA256:         hex.EncodeToString(environment.CodexSHA256[:]),
			OuterProfileVersion: environment.OuterProfileVersion,
			ProcessMethods:      append([]string(nil), environment.ProcessMethods...),
			InsecureDev:         environment.InsecureDev,
		}
	}
	return converted
}

func contractHolder(holder ConnectionHolder) corecontract.ConnectionHolder {
	return corecontract.ConnectionHolder{
		ExecutorID:        holder.ExecutorID,
		ConnectionID:      holder.ConnectionID,
		SessionID:         holder.SessionID,
		GatewayInstanceID: holder.GatewayInstanceID,
		Generation:        holder.Generation,
		Status:            holder.Status,
		ExpiresAt:         holder.ExpiresAt,
	}
}

func gatewayHolder(holder corecontract.ConnectionHolder) ConnectionHolder {
	return ConnectionHolder{
		ExecutorID:        holder.ExecutorID,
		ConnectionID:      holder.ConnectionID,
		SessionID:         holder.SessionID,
		GatewayInstanceID: holder.GatewayInstanceID,
		Generation:        holder.Generation,
		Status:            holder.Status,
		ExpiresAt:         holder.ExpiresAt,
	}
}
