package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/k8sruntime"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

const (
	maxRuntimeBusyRetries = 3
	runtimeBusyRetryDelay = 40 * time.Millisecond
)

type HTTPRuntimeClient struct {
	client *http.Client
	logger *slog.Logger
}

func (c *HTTPRuntimeClient) PrepareRepository(ctx context.Context, e Endpoint, r k8sruntime.PrepareRepositoryRequest) (k8sruntime.RepositoryState, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	copyClient := *c.client
	if transport, ok := c.client.Transport.(*http.Transport); ok {
		copyTransport := transport.Clone()
		copyTransport.ResponseHeaderTimeout = 5 * time.Minute
		defer copyTransport.CloseIdleConnections()
		copyClient.Transport = copyTransport
	}
	clone := HTTPRuntimeClient{client: &copyClient}
	resp, err := clone.request(ctx, e, http.MethodPost, k8sruntime.PrepareRepositoryPath, r)
	if err != nil {
		return k8sruntime.RepositoryState{}, err
	}
	defer resp.Body.Close()
	var state k8sruntime.RepositoryState
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024+1))
	decoder.DisallowUnknownFields()
	if resp.StatusCode != http.StatusOK || decoder.Decode(&state) != nil {
		return k8sruntime.RepositoryState{}, errors.New("runtime repository preparation failed")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || state.CheckoutID != r.CheckoutID || state.Context.Validate(r.Source.WorkingDirectory) != nil {
		return k8sruntime.RepositoryState{}, errors.New("invalid runtime repository response")
	}
	return state, nil
}

// NewHTTPRuntimeClient requires a caller-provided authenticated transport.
// Production assembly configures server verification and gateway mTLS.
func NewHTTPRuntimeClient(client *http.Client, loggers ...*slog.Logger) (*HTTPRuntimeClient, error) {
	if client == nil || client.Transport == nil {
		return nil, errors.New("runtime authenticated HTTP transport is required")
	}
	copy := *client
	copy.Timeout = 0
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return &HTTPRuntimeClient{client: &copy, logger: logger}, nil
}

func (c *HTTPRuntimeClient) request(ctx context.Context, e Endpoint, method, path string, body any) (*http.Response, error) {
	u, err := url.Parse(e.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("invalid runtime origin")
	}
	var data []byte
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, errors.New("runtime payload encoding failed")
		}
	}
	u.Path = path
	r, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	r.GetBody = nil
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(k8sruntime.PodHeader, e.PodUID)
	r.Header.Set(k8sruntime.BootHeader, e.BootID)
	resp, err := c.client.Do(r)
	if err != nil {
		if c.logger != nil {
			c.logger.Error("runtime HTTP request failed", "method", method, "path", path, "pod_uid", e.PodUID, "boot_id", e.BootID, "error", err)
		}
		return nil, errors.New("runtime transport unavailable")
	}
	return resp, nil
}

func (c *HTTPRuntimeClient) Identity(ctx context.Context, e Endpoint) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.request(ctx, e, http.MethodGet, k8sruntime.IdentityPath, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var identity k8sruntime.Identity
	if resp.StatusCode != http.StatusOK || decodeRuntimeJSON(resp.Body, &identity) != nil || identity.PodUID != e.PodUID || len(identity.BootID) != 64 {
		return "", errors.New("runtime identity mismatch")
	}
	return identity.BootID, nil
}

func (c *HTTPRuntimeClient) Bind(ctx context.Context, e Endpoint, binding k8sruntime.Binding) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := c.request(ctx, e, http.MethodPost, k8sruntime.BindPath, binding)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var actual k8sruntime.Binding
	if resp.StatusCode != http.StatusOK || decodeRuntimeJSON(resp.Body, &actual) != nil || actual != binding {
		return errors.New("runtime binding rejected")
	}
	return nil
}

func decodeRuntimeJSON(body io.Reader, dst any) error {
	d := json.NewDecoder(io.LimitReader(body, 8193))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("invalid runtime document")
	}
	return nil
}

func operationIdentity(o executionbackend.OperationContext, env string) sandboxcontract.OperationIdentity {
	return sandboxcontract.OperationIdentity{Session: sandboxcontract.SessionIdentity{WorkspaceID: o.WorkspaceID, SessionID: o.SessionID, EnvironmentID: env}, RunID: o.RunID, RunAttemptID: o.RunAttemptID, RunAttemptGeneration: o.RunAttemptGeneration, ExecutionID: o.ExecutionID, OperationID: o.OperationID, MutationKey: o.MutationKey}
}
func runtimeRef(t executionbackend.Target) sandboxcontract.SandboxRef {
	return sandboxcontract.SandboxRef{SandboxID: t.ID, TargetGeneration: t.Generation, BackendKind: t.Kind}
}

func (c *HTTPRuntimeClient) exchange(ctx context.Context, e Endpoint, path string, t executionbackend.Target, o executionbackend.OperationContext, payload any) (executionbackend.Exchange, error) {
	var resp *http.Response
	var err error
	for attempt := 0; ; attempt++ {
		resp, err = c.request(ctx, e, http.MethodPost, path, payload)
		if err != nil || resp.StatusCode == http.StatusOK {
			break
		}
		document := readRuntimeError(resp)
		if resp.StatusCode != http.StatusConflict || !document.IsBusy() || attempt >= maxRuntimeBusyRetries {
			return nil, runtimeDispatchErrorFromDocument(resp.StatusCode, document)
		}
		// repository_busy is emitted before accept() consumes the operation.
		// Retry only this proven pre-admission rejection, with identical IDs.
		// Transport failures, duplicate operations and boot changes are never replayed.
		timer := time.NewTimer(runtimeBusyRetryDelay * time.Duration(attempt+1))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, runtimeDispatchErrorFromDocument(resp.StatusCode, document)
		case <-timer.C:
		}
	}
	if err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "runtime_transport", err)
	}
	if resp.StatusCode != http.StatusOK {
		document := readRuntimeError(resp)
		if c.logger != nil {
			code := ""
			if document.valid {
				code = document.document.Code
			}
			c.logger.Error("runtime dispatch rejected", "path", path, "status", resp.StatusCode, "code", code, "pod_uid", e.PodUID, "boot_id", e.BootID, "operation_id", o.OperationID, "execution_id", o.ExecutionID)
		}
		return nil, runtimeDispatchErrorFromDocument(resp.StatusCode, document)
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/x-ndjson" {
		if c.logger != nil {
			c.logger.Error("runtime dispatch returned invalid stream", "path", path, "status", resp.StatusCode, "content_type", resp.Header.Get("Content-Type"), "pod_uid", e.PodUID, "boot_id", e.BootID, "operation_id", o.OperationID, "execution_id", o.ExecutionID)
		}
		resp.Body.Close()
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "invalid_runtime_stream", errors.New("invalid runtime stream content type"))
	}
	return executorgateway.NewSandboxOperationExchange(t, o, resp.Body, c.logger)
}

type runtimeErrorDocument struct {
	document sandboxcontract.ErrorResponse
	valid    bool
}

func (e runtimeErrorDocument) IsBusy() bool {
	return e.valid && e.document.Code == "repository_busy" && e.document.Outcome == string(executionbackend.OutcomeRejected)
}

func readRuntimeError(resp *http.Response) runtimeErrorDocument {
	if resp == nil || resp.Body == nil {
		return runtimeErrorDocument{}
	}
	defer resp.Body.Close()
	var document sandboxcontract.ErrorResponse
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024+1))
	if err != nil || len(raw) > 16*1024 {
		return runtimeErrorDocument{}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || document.Code == "" {
		return runtimeErrorDocument{}
	}
	var extra any
	return runtimeErrorDocument{document: document, valid: decoder.Decode(&extra) == io.EOF}
}

func runtimeDispatchErrorFromDocument(status int, runtime runtimeErrorDocument) error {
	document := runtime.document
	outcome := executionbackend.OutcomeUnknown
	code, providerCode := "runtime_rejected", ""
	// Only known runtime codes may enter logs. Never retain provider messages,
	// arbitrary request IDs or untrusted text in dispatch diagnostics.
	if runtime.valid && document.Outcome == string(executionbackend.OutcomeRejected) && runtimeErrorStatus(document.Code) == status {
		code, providerCode = "runtime_"+document.Code, document.Code
		if document.Code != "duplicate_or_fenced" && document.Code != "runtime_replaced" && document.Code != "binding_conflict" {
			outcome = executionbackend.OutcomeRejected
		}
	}
	dispatch := executionbackend.NewDispatchError(outcome, code, errors.New("runtime rejected execution request"))
	dispatch.ProviderCode = providerCode
	dispatch.HTTPStatus = status
	written := true
	dispatch.RequestWritten = &written
	return dispatch
}

func runtimeErrorStatus(code string) int {
	switch code {
	case "repository_busy", "duplicate_or_fenced", "runtime_replaced", "binding_conflict":
		return http.StatusConflict
	case "invalid_json", "invalid_command", "invalid_signal", "invalid_read", "path_outside_workspace", "not_regular_file", "read_failed":
		return http.StatusBadRequest
	case "forbidden":
		return http.StatusForbidden
	case "file_unavailable", "process_not_found", "not_found":
		return http.StatusNotFound
	case "process_start_failed", "interrupt_unsupported":
		return http.StatusUnprocessableEntity
	case "not_ready", "workspace_unavailable":
		return http.StatusServiceUnavailable
	default:
		return 0
	}
}

func (c *HTTPRuntimeClient) StartProcess(ctx context.Context, e Endpoint, r executionbackend.StartProcessRequest) (executionbackend.Exchange, error) {
	if err := r.Validate(); err != nil {
		return nil, notSent(err)
	}
	if r.TTY {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "tty_unsupported", errors.New("managed runtime does not support TTY"))
	}
	path, err := sandboxcontract.RunCommandPath(r.Target.ID)
	if err != nil {
		return nil, notSent(err)
	}
	return c.exchange(ctx, e, path, r.Target, r.Operation, sandboxcontract.RunCommandRequest{Profile: sandboxcontract.ProfileV1, RequestID: r.RequestID, Identity: operationIdentity(r.Operation, r.Target.EnvironmentID), Ref: runtimeRef(r.Target), ProcessID: r.ProcessID, Executable: r.Executable, Arguments: r.Arguments, WorkingDirectory: r.WorkingDirectory, WorkspaceAccess: r.WorkspaceAccess, Environment: r.Environment, TimeoutMillis: r.Timeout.Milliseconds(), OutputLimitBytes: r.OutputLimitBytes})
}
func (c *HTTPRuntimeClient) SignalProcess(ctx context.Context, e Endpoint, r executionbackend.SignalProcessRequest) (executionbackend.Exchange, error) {
	if err := r.Validate(); err != nil {
		return nil, notSent(err)
	}
	path, err := sandboxcontract.SignalProcessPath(r.Target.ID, r.ProcessID)
	if err != nil {
		return nil, notSent(err)
	}
	return c.exchange(ctx, e, path, r.Target, r.Operation, sandboxcontract.SignalCommandRequest{Profile: sandboxcontract.ProfileV1, RequestID: r.RequestID, Identity: operationIdentity(r.Operation, r.Target.EnvironmentID), Ref: runtimeRef(r.Target), ProcessID: r.ProcessID, ProviderHandle: r.ProviderHandle, Signal: r.Signal, Reason: r.Reason})
}
func (c *HTTPRuntimeClient) ReadFile(ctx context.Context, e Endpoint, r executionbackend.ReadFileRequest) (executionbackend.Exchange, error) {
	if err := r.Validate(); err != nil {
		return nil, notSent(err)
	}
	path, err := sandboxcontract.ReadFilePath(r.Target.ID)
	if err != nil {
		return nil, notSent(err)
	}
	return c.exchange(ctx, e, path, r.Target, r.Operation, sandboxcontract.ReadFileRequest{Profile: sandboxcontract.ProfileV1, RequestID: r.RequestID, Identity: operationIdentity(r.Operation, r.Target.EnvironmentID), Ref: runtimeRef(r.Target), Path: r.Path, Offset: r.Offset, Limit: r.Limit})
}

var _ RuntimeClient = (*HTTPRuntimeClient)(nil)
