package executorgateway

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

const maxCoreCommandResponseBytes = 512 * 1024

type CoreConnectionClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewCoreConnectionClient(baseURL string, httpClient *http.Client) (*CoreConnectionClient, error) {
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

func (client *CoreConnectionClient) post(ctx context.Context, path string, command, destination any, wantStatus int) error {
	raw, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("encode core command: %w", err)
	}
	endpoint := *client.baseURL
	endpoint.Path = path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("construct core command: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
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
	if response.StatusCode != wantStatus {
		return decodeCoreCommandError(response.StatusCode, body)
	}
	if destination == nil {
		if len(bytes.TrimSpace(body)) != 0 {
			return errors.New("core no-content response unexpectedly contains a body")
		}
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode core command response: %w", err)
	}
	if err := finishCoreJSON(decoder); err != nil {
		return fmt.Errorf("finish core command response: %w", err)
	}
	return nil
}

func decodeCoreCommandError(status int, body []byte) error {
	var response corecontract.ErrorResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || finishCoreJSON(decoder) != nil || response.Code == "" {
		return fmt.Errorf("core command returned HTTP %d with an invalid error envelope", status)
	}
	message := strings.TrimSpace(response.Message)
	if message == "" {
		message = response.Code
	}
	if response.Code == "connection_fenced" {
		return fmt.Errorf("%w: %s (current generation %d)", ErrConnectionFenced, message, response.CurrentGeneration)
	}
	return fmt.Errorf("core command %s: %s", response.Code, message)
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
