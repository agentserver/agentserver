// Package sandboxclient is the provider-neutral lifecycle client shared by
// harness-pool and exceptional managed-target recovery paths. It has no TAE
// SDK dependency.
package sandboxclient

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

	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

const maxResponseBytes = 2 * 1024 * 1024

const (
	ActionEnsure          = "ensure"
	ActionRenewActivity   = "renew_activity"
	ActionReleaseActivity = "release_activity"
	ActionDelete          = "delete"
)

type TokenRequest struct {
	Action               string
	Session              sandboxcontract.SessionIdentity
	Ref                  sandboxcontract.SandboxRef
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	HolderID             string
}

type TokenSource interface {
	Token(context.Context, TokenRequest) (string, error)
}

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	tokens     TokenSource
}

type Error struct {
	HTTPStatus int
	Code       string
	Outcome    string
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("sandbox gateway %s", err.Code)
}

func New(baseURL string, httpClient *http.Client, tokens TokenSource) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Path != "" ||
		parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.ForceQuery {
		return nil, errors.New("sandbox-gateway base URL must be an absolute HTTP(S) origin")
	}
	if parsed.Scheme == "http" && !loopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext sandbox-gateway URL is allowed only on loopback")
	}
	if httpClient == nil || tokens == nil {
		return nil, errors.New("sandbox-gateway HTTP client and token source are required")
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{baseURL: parsed, httpClient: &clientCopy, tokens: tokens}, nil
}

func (client *Client) Ensure(ctx context.Context, request sandboxcontract.EnsureSandboxRequest, authority TokenRequest) (sandboxcontract.EnsureSandboxResponse, error) {
	var response sandboxcontract.EnsureSandboxResponse
	err := client.do(ctx, http.MethodPost, sandboxcontract.EnsureSandboxPath, request, authority, &response)
	if err == nil {
		err = response.Validate()
	}
	return response, err
}

func (client *Client) Get(ctx context.Context, request sandboxcontract.GetSandboxRequest, authority TokenRequest) (sandboxcontract.SandboxResponse, error) {
	path, err := sandboxcontract.GetSandboxPath(request.Ref.SandboxID)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, err
	}
	var response sandboxcontract.SandboxResponse
	err = client.do(ctx, http.MethodGet, path, request, authority, &response)
	if err == nil {
		err = response.Validate()
	}
	return response, err
}

func (client *Client) RenewActivity(ctx context.Context, request sandboxcontract.RenewSandboxActivityRequest, authority TokenRequest) (sandboxcontract.SandboxResponse, error) {
	path, err := sandboxcontract.RenewSandboxActivityPath(request.Ref.SandboxID)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, err
	}
	var response sandboxcontract.SandboxResponse
	err = client.do(ctx, http.MethodPost, path, request, authority, &response)
	if err == nil {
		err = response.Validate()
	}
	return response, err
}

func (client *Client) ReleaseActivity(ctx context.Context, request sandboxcontract.ReleaseSandboxActivityRequest, authority TokenRequest) (sandboxcontract.SandboxResponse, error) {
	path, err := sandboxcontract.ReleaseSandboxActivityPath(request.Ref.SandboxID)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, err
	}
	var response sandboxcontract.SandboxResponse
	err = client.do(ctx, http.MethodPost, path, request, authority, &response)
	if err == nil {
		err = response.Validate()
	}
	return response, err
}

func (client *Client) Delete(ctx context.Context, request sandboxcontract.DeleteSandboxRequest, authority TokenRequest) (sandboxcontract.SandboxResponse, error) {
	path, err := sandboxcontract.GetSandboxPath(request.Ref.SandboxID)
	if err != nil {
		return sandboxcontract.SandboxResponse{}, err
	}
	var response sandboxcontract.SandboxResponse
	err = client.do(ctx, http.MethodDelete, path, request, authority, &response)
	if err == nil {
		err = response.Validate()
	}
	return response, err
}

func (client *Client) do(ctx context.Context, method, path string, command any, authority TokenRequest, destination any) error {
	if client == nil || client.baseURL == nil || client.httpClient == nil || client.tokens == nil || ctx == nil {
		return errors.New("sandbox-gateway client and context are required")
	}
	token, err := client.tokens.Token(ctx, authority)
	if err != nil {
		return fmt.Errorf("issue sandbox-gateway capability: %w", err)
	}
	if token == "" || len(token) > 32*1024 || strings.TrimSpace(token) != token || strings.ContainsAny(token, " \t\r\n\x00") {
		return errors.New("sandbox-gateway capability is invalid")
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(command); err != nil {
		return fmt.Errorf("encode sandbox-gateway request: %w", err)
	}
	endpoint := *client.baseURL
	endpoint.Path = path
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(raw.Bytes()))
	if err != nil {
		return fmt.Errorf("construct sandbox-gateway request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("execute sandbox-gateway request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return errors.New("sandbox-gateway response is unreadable or too large")
	}
	if response.StatusCode != http.StatusOK {
		var contractError sandboxcontract.ErrorResponse
		if decodeStrict(body, &contractError) != nil || contractError.Code == "" {
			return &Error{HTTPStatus: response.StatusCode, Code: "invalid_error_response"}
		}
		return &Error{HTTPStatus: response.StatusCode, Code: contractError.Code, Outcome: contractError.Outcome}
	}
	return decodeStrict(body, destination)
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("response contains trailing JSON")
		}
		return err
	}
	return nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
