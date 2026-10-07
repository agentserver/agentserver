package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/executorgateway"
	"github.com/agentserver/agentserver/v2/internal/k8sruntime"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

type HTTPRuntimeClient struct{ client *http.Client }

// NewHTTPRuntimeClient requires a caller-provided authenticated transport.
// Production assembly configures server verification and gateway mTLS.
func NewHTTPRuntimeClient(client *http.Client) (*HTTPRuntimeClient, error) {
	if client == nil || client.Transport == nil {
		return nil, errors.New("runtime authenticated HTTP transport is required")
	}
	copy := *client
	copy.Timeout = 0
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPRuntimeClient{client: &copy}, nil
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
	resp, err := c.request(ctx, e, http.MethodPost, path, payload)
	if err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "runtime_transport", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		outcome := executionbackend.OutcomeUnknown
		if resp.StatusCode == 400 || resp.StatusCode == 403 || resp.StatusCode == 404 || resp.StatusCode == 422 {
			outcome = executionbackend.OutcomeRejected
		}
		return nil, executionbackend.NewDispatchError(outcome, "runtime_rejected", errors.New("runtime rejected execution request"))
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/x-ndjson" {
		resp.Body.Close()
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "invalid_runtime_stream", errors.New("invalid runtime stream content type"))
	}
	return executorgateway.NewSandboxOperationExchange(t, o, resp.Body)
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
