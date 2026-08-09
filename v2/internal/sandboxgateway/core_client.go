package sandboxgateway

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
	"strconv"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	maxCoreRequestBytes  = 512 * 1024
	maxCoreResponseBytes = 2 * 1024 * 1024
)

type Core interface {
	ReserveManagedSandbox(context.Context, corecontract.ReserveManagedSandboxRequest) (corecontract.ReserveManagedSandboxResponse, error)
	GetManagedSandbox(context.Context, string, int64) (corecontract.GetManagedSandboxResponse, error)
	BeginManagedSandboxCreate(context.Context, corecontract.BeginManagedSandboxCreateRequest) (corecontract.ManagedSandboxMutationResponse, error)
	ObserveManagedSandbox(context.Context, corecontract.ObserveManagedSandboxRequest) (corecontract.ManagedSandboxMutationResponse, error)
	RenewManagedSandboxActivity(context.Context, corecontract.RenewManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error)
	ReleaseManagedSandboxActivity(context.Context, corecontract.ReleaseManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error)
	BeginManagedSandboxDelete(context.Context, corecontract.BeginManagedSandboxDeleteRequest) (corecontract.ManagedSandboxMutationResponse, error)
	ListManagedSandboxesForReconcile(context.Context, corecontract.ListManagedSandboxesForReconcileRequest) (corecontract.ListManagedSandboxesForReconcileResponse, error)
	AuthorizeManagedSandboxOperation(context.Context, corecontract.AuthorizeManagedSandboxOperationRequest) (corecontract.AuthorizeManagedSandboxOperationResponse, error)
}

type CoreClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

type CoreError struct {
	HTTPStatus        int
	Code              string
	Message           string
	CurrentVersion    int64
	CurrentGeneration int64
}

func (coreError *CoreError) Error() string {
	if coreError == nil {
		return "<nil>"
	}
	message := strings.TrimSpace(coreError.Message)
	if message == "" {
		message = coreError.Code
	}
	return fmt.Sprintf("core managed sandbox command %s: %s", coreError.Code, message)
}

func NewCoreClient(baseURL string, httpClient *http.Client) (*CoreClient, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		(parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("core base URL must be an absolute canonical HTTP(S) origin")
	}
	if parsed.Scheme == "http" && !coreLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext Core URL is allowed only on loopback")
	}
	if httpClient == nil {
		return nil, errors.New("core HTTP client is required")
	}
	parsed.Path = ""
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &CoreClient{baseURL: parsed, httpClient: &clientCopy}, nil
}

func coreLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func (client *CoreClient) ReserveManagedSandbox(ctx context.Context, request corecontract.ReserveManagedSandboxRequest) (corecontract.ReserveManagedSandboxResponse, error) {
	var response corecontract.ReserveManagedSandboxResponse
	err := client.post(ctx, corecontract.ReserveManagedSandboxPath, request, &response)
	return response, err
}

func (client *CoreClient) GetManagedSandbox(ctx context.Context, sandboxID string, generation int64) (corecontract.GetManagedSandboxResponse, error) {
	var response corecontract.GetManagedSandboxResponse
	path := corecontract.ManagedSandboxPath(sandboxID) + "?generation=" + strconv.FormatInt(generation, 10)
	err := client.do(ctx, http.MethodGet, path, nil, &response)
	return response, err
}

func (client *CoreClient) BeginManagedSandboxCreate(ctx context.Context, request corecontract.BeginManagedSandboxCreateRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	var response corecontract.ManagedSandboxMutationResponse
	err := client.post(ctx, corecontract.BeginManagedSandboxCreatePath(request.SandboxID), request, &response)
	return response, err
}

func (client *CoreClient) ObserveManagedSandbox(ctx context.Context, request corecontract.ObserveManagedSandboxRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	var response corecontract.ManagedSandboxMutationResponse
	err := client.post(ctx, corecontract.ObserveManagedSandboxPath(request.SandboxID), request, &response)
	return response, err
}

func (client *CoreClient) RenewManagedSandboxActivity(ctx context.Context, request corecontract.RenewManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	var response corecontract.ManagedSandboxMutationResponse
	err := client.post(ctx, corecontract.RenewManagedSandboxActivityPath(request.SandboxID), request, &response)
	return response, err
}

func (client *CoreClient) ReleaseManagedSandboxActivity(ctx context.Context, request corecontract.ReleaseManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	var response corecontract.ManagedSandboxMutationResponse
	err := client.post(ctx, corecontract.ReleaseManagedSandboxActivityPath(request.SandboxID), request, &response)
	return response, err
}

func (client *CoreClient) BeginManagedSandboxDelete(ctx context.Context, request corecontract.BeginManagedSandboxDeleteRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	var response corecontract.ManagedSandboxMutationResponse
	err := client.post(ctx, corecontract.BeginManagedSandboxDeletePath(request.SandboxID), request, &response)
	return response, err
}

func (client *CoreClient) ListManagedSandboxesForReconcile(ctx context.Context, request corecontract.ListManagedSandboxesForReconcileRequest) (corecontract.ListManagedSandboxesForReconcileResponse, error) {
	var response corecontract.ListManagedSandboxesForReconcileResponse
	err := client.post(ctx, corecontract.ListManagedSandboxesForReconcilePath, request, &response)
	return response, err
}

func (client *CoreClient) AuthorizeManagedSandboxOperation(ctx context.Context, request corecontract.AuthorizeManagedSandboxOperationRequest) (corecontract.AuthorizeManagedSandboxOperationResponse, error) {
	var response corecontract.AuthorizeManagedSandboxOperationResponse
	err := client.post(ctx, corecontract.AuthorizeManagedSandboxOperationPath, request, &response)
	return response, err
}

func (client *CoreClient) post(ctx context.Context, path string, request, response any) error {
	return client.do(ctx, http.MethodPost, path, request, response)
}

func (client *CoreClient) do(ctx context.Context, method, path string, command, destination any) error {
	if client == nil || client.baseURL == nil || client.httpClient == nil {
		return errors.New("core client is not configured")
	}
	if ctx == nil {
		return errors.New("core command context is required")
	}
	var body io.Reader
	if command != nil {
		var raw bytes.Buffer
		encoder := json.NewEncoder(&raw)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(command); err != nil {
			return fmt.Errorf("encode core managed sandbox command: %w", err)
		}
		if raw.Len() > maxCoreRequestBytes {
			return errors.New("core managed sandbox command exceeds request size limit")
		}
		body = bytes.NewReader(raw.Bytes())
	}
	endpoint := *client.baseURL
	parsedPath, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(parsedPath.Path, "/") {
		return errors.New("core command path is invalid")
	}
	endpoint.Path = parsedPath.Path
	endpoint.RawQuery = parsedPath.RawQuery
	httpRequest, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("construct core managed sandbox command: %w", err)
	}
	if command != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("execute core managed sandbox command: %w", err)
	}
	defer httpResponse.Body.Close()
	rawResponse, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxCoreResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read core managed sandbox response: %w", err)
	}
	if len(rawResponse) > maxCoreResponseBytes {
		return errors.New("core managed sandbox response exceeds size limit")
	}
	if httpResponse.StatusCode != http.StatusOK {
		return decodeCoreError(httpResponse.StatusCode, rawResponse)
	}
	if err := decodeStrictJSON(rawResponse, destination); err != nil {
		return fmt.Errorf("decode core managed sandbox response: %w", err)
	}
	return nil
}

func decodeCoreError(status int, body []byte) error {
	var response corecontract.ErrorResponse
	if err := decodeStrictJSON(body, &response); err != nil || response.Code == "" {
		return &CoreError{HTTPStatus: status, Code: "invalid_error_response", Message: "core returned an invalid error response"}
	}
	return &CoreError{
		HTTPStatus: status, Code: response.Code, Message: response.Message,
		CurrentVersion: response.CurrentVersion, CurrentGeneration: response.CurrentGeneration,
	}
}

func decodeStrictJSON(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("response contains more than one JSON value")
		}
		return err
	}
	return nil
}

var _ Core = (*CoreClient)(nil)
