package llmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const maximumCoreResponseBytes = 512 * 1024

type CoreClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewCoreClient(baseURL string, httpClient *http.Client) (*CoreClient, error) {
	if httpClient == nil {
		return nil, errors.New("llmproxy Core HTTP client is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse llmproxy Core base URL: %w", err)
	}
	if (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("llmproxy Core base URL must be an absolute HTTP(S) origin")
	}
	if parsed.Scheme == "http" && !loopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext llmproxy Core base URL is allowed only on loopback")
	}
	parsed.Path = ""
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CoreClient{baseURL: parsed, httpClient: &clientCopy}, nil
}

func (client *CoreClient) AuthorizeLLMProxyRunCapability(
	ctx context.Context,
	request RunCapabilityAuthorizationRequest,
) (RunCapabilityAuthorization, error) {
	if request.Token == "" || strings.TrimSpace(request.Token) != request.Token ||
		strings.ContainsAny(request.Token, "\r\n") || len(request.Token) > maximumRunCapabilityBytes {
		return RunCapabilityAuthorization{}, errors.New("llmproxy run capability bearer is invalid")
	}
	body, err := json.Marshal(corecontract.AuthorizeLLMProxyRunCapabilityRequest{
		Model: request.Model, Provider: request.Provider,
		LLMGatewayID: request.LLMGatewayID, LLMGatewayVersion: request.LLMGatewayVersion,
		LLMGatewayGrantUserID: request.LLMGatewayGrantUserID,
	})
	if err != nil {
		return RunCapabilityAuthorization{}, errors.New("encode Core llmproxy authorization request")
	}
	endpoint := *client.baseURL
	endpoint.Path = corecontract.AuthorizeLLMProxyRunCapabilityPath
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return RunCapabilityAuthorization{}, errors.New("construct Core llmproxy authorization request")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+request.Token)
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return RunCapabilityAuthorization{}, errors.New("Core llmproxy capability live authorization failed")
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maximumCoreResponseBytes+1))
	if readErr != nil || len(raw) > maximumCoreResponseBytes || response.Header.Get("Cache-Control") != "no-store" ||
		response.StatusCode != http.StatusOK {
		return RunCapabilityAuthorization{}, errors.New("Core llmproxy capability live authorization failed")
	}
	var contractResponse corecontract.AuthorizeLLMProxyRunCapabilityResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contractResponse); err != nil || finishJSON(decoder) != nil {
		return RunCapabilityAuthorization{}, errors.New("Core llmproxy capability live authorization failed")
	}
	result := RunCapabilityAuthorization{
		CapabilityID: contractResponse.CapabilityID, Audience: contractResponse.Audience,
		RunID: contractResponse.RunID, RunAttemptID: contractResponse.RunAttemptID,
		RunAttemptGeneration: contractResponse.RunAttemptGeneration,
		RunVersion:           contractResponse.RunVersion, RunAttemptVersion: contractResponse.RunAttemptVersion,
		AuthorizedAt: contractResponse.AuthorizedAt,
		Model:        contractResponse.Model, Provider: contractResponse.Provider,
		LLMGatewayID: contractResponse.LLMGatewayID, LLMGatewayVersion: contractResponse.LLMGatewayVersion,
		LLMGatewayGrantUserID: contractResponse.LLMGatewayGrantUserID,
		ResponsesURL:          contractResponse.ResponsesURL,
		UpstreamAuthorization: contractResponse.UpstreamAuthorization,
		BearerExpiresAt:       contractResponse.BearerExpiresAt,
	}
	if result.CapabilityID != request.CapabilityID || result.Audience != runcapabilityAudience ||
		result.RunID != request.RunID || result.RunAttemptID != request.RunAttemptID ||
		result.RunAttemptGeneration != request.RunAttemptGeneration || !safeVersion(result.RunVersion) ||
		!safeVersion(result.RunAttemptVersion) || result.AuthorizedAt.IsZero() ||
		result.Model != request.Model || result.Provider != request.Provider ||
		result.LLMGatewayID != request.LLMGatewayID || result.LLMGatewayVersion != request.LLMGatewayVersion ||
		result.LLMGatewayGrantUserID != request.LLMGatewayGrantUserID || result.ResponsesURL == "" ||
		result.UpstreamAuthorization == "" || result.BearerExpiresAt.IsZero() {
		return RunCapabilityAuthorization{}, errors.New("Core llmproxy capability authorization response is inconsistent")
	}
	return result, nil
}

const runcapabilityAudience = "llmproxy"

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

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
