package harnessworker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ucarion/jcs"
)

const (
	// SupportedMCPProtocolVersion is deliberately pinned to the last stateful
	// Streamable HTTP protocol supported by the reference bridge. The newer
	// stateless profile cannot carry the nested elicitation/create request used
	// by executor approvals.
	SupportedMCPProtocolVersion = "2025-11-25"

	MCPMetaRunID                = "io.agentserver/runId"
	MCPMetaThreadID             = "io.agentserver/threadId"
	MCPMetaTurnID               = "io.agentserver/turnId"
	MCPMetaCallID               = "io.agentserver/callId"
	MCPMetaRunAttemptGeneration = "io.agentserver/runAttemptGeneration"
	MCPMetaToolCatalogDigest    = "io.agentserver/toolCatalogDigest"
	MCPMetaExecutionID          = "io.agentserver/executionId"
	MCPMetaApprovalID           = "io.agentserver/approvalId"
	MCPMetaApprovalNonce        = "io.agentserver/approvalNonce"
	MCPMetaApprovalVersion      = "io.agentserver/approvalVersion"
	MCPMetaContextHash          = "io.agentserver/contextHash"
	MCPMetaExpiresAt            = "io.agentserver/expiresAt"

	maxIdentityBytes  = 256
	maxBearerBytes    = 16 * 1024
	transportOverhead = 64 * 1024

	defaultMCPShutdownGrace = 2 * time.Second
	maxMCPShutdownGrace     = 30 * time.Second
	mcpAbortCompletionGrace = time.Second
)

var errExecutorMCPTransportClosed = errors.New("executor MCP transport is closed")

type ApprovalAction string

const (
	ApprovalAccept  ApprovalAction = "accept"
	ApprovalDecline ApprovalAction = "decline"
	ApprovalCancel  ApprovalAction = "cancel"
)

// ElicitationRequest is the worker's canonical approval projection. All
// identity and deadline fields originate in gateway-controlled MCP metadata,
// never in the model's dynamic tool arguments.
type ElicitationRequest struct {
	RunID                string
	CallID               string
	RunAttemptGeneration int64
	ToolCatalogDigest    string
	ExecutionID          string
	ApprovalID           string
	Nonce                string
	ApprovalVersion      int64
	ContextHash          string
	ExpiresAt            time.Time
	Message              string
	RequestedSchema      json.RawMessage
}

type ElicitationDecision struct {
	Action  ApprovalAction
	Content map[string]any
}

type ElicitationHandler func(context.Context, ElicitationRequest) (ElicitationDecision, error)

type ProgressEvent struct {
	RunID                string
	CallID               string
	RunAttemptGeneration int64
	Progress             float64
	Total                float64
	Message              string
}

type ProgressHandler func(context.Context, ProgressEvent) error

// DynamicCall is the trusted envelope around one item/tool/call callback. The
// model controls only Namespace, Tool, and Arguments; the worker obtains all
// other fields from the app-server envelope and signed run manifest.
type DynamicCall struct {
	RunID                string
	ThreadID             string
	TurnID               string
	CallID               string
	RunAttemptGeneration int64
	Namespace            string
	Tool                 string
	Arguments            json.RawMessage
}

type InputTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// DynamicToolResult is written as the result of the original app-server
// item/tool/call request. It intentionally has no MCP metadata or resources.
type DynamicToolResult struct {
	ContentItems []InputTextContent `json:"contentItems"`
	Success      bool               `json:"success"`
}

type MCPClientConfig struct {
	Endpoint              string
	TLSIdentity           string
	BearerToken           string
	HTTPClient            *http.Client
	AllowInsecureLoopback bool
	Namespace             string
	NamespaceDescription  string
	ExpectedCatalogDigest string
	ExpectedCatalog       []byte
	Limits                Limits
	ElicitationHandler    ElicitationHandler
	ProgressHandler       ProgressHandler
	Logger                *slog.Logger
	// CloseGrace bounds the SDK's graceful session close before the private
	// HTTP transport is aborted. Zero selects the bounded reference default.
	CloseGrace time.Duration
}

type pendingMCPCall struct {
	call   DynamicCall
	ctx    context.Context
	cancel context.CancelCauseFunc
	err    error
}

// MCPClient is the worker-owned, bounded executor-gateway MCP client. Its
// bearer and HTTP transport never enter app-server configuration or stdio.
type MCPClient struct {
	session      *mcp.ClientSession
	transport    *exactMCPTransport
	catalog      *Catalog
	limits       Limits
	closeGrace   time.Duration
	elicitation  ElicitationHandler
	progress     ProgressHandler
	now          func() time.Time
	closeOnce    sync.Once
	closeErr     error
	pendingMu    sync.Mutex
	closed       bool
	pendingCalls map[string]*pendingMCPCall
}

// ConnectMCP initializes one bounded Streamable HTTP session, reads every
// tools/list page through the official MCP SDK, and verifies the exact frozen
// catalog before returning.
func ConnectMCP(ctx context.Context, config MCPClientConfig) (*MCPClient, error) {
	if ctx == nil {
		return nil, errors.New("MCP connect context is required")
	}
	if err := config.Limits.validate(); err != nil {
		return nil, err
	}
	if err := validateNamespace(config.Namespace, config.Limits.MaxNameBytes); err != nil {
		return nil, err
	}
	if err := validateText("namespace description", config.NamespaceDescription, config.Limits.MaxDescriptionBytes); err != nil {
		return nil, err
	}
	endpoint, err := validateMCPEndpoint(config.Endpoint, config.AllowInsecureLoopback)
	if err != nil {
		return nil, err
	}
	httpClient := config.HTTPClient
	if endpoint.Scheme == "https" {
		httpClient, err = secureMCPHTTPClient(config.HTTPClient, config.TLSIdentity)
		if err != nil {
			return nil, err
		}
	} else if config.TLSIdentity != "" {
		return nil, errors.New("plain HTTP executor MCP test endpoint cannot declare a TLS identity")
	}
	if err := validateBearer(config.BearerToken); err != nil {
		return nil, err
	}
	if config.ElicitationHandler == nil {
		return nil, errors.New("MCP elicitation handler is required")
	}
	if !equalDigest(config.ExpectedCatalogDigest, config.ExpectedCatalogDigest) {
		return nil, errors.New("expected MCP catalog digest must be a 32-byte hexadecimal SHA-256")
	}
	if len(config.ExpectedCatalog) == 0 {
		return nil, errors.New("expected MCP canonical catalog bytes are required")
	}
	if len(config.ExpectedCatalog) > config.Limits.MaxCatalogBytes {
		return nil, fmt.Errorf("expected MCP canonical catalog is %d bytes, limit is %d", len(config.ExpectedCatalog), config.Limits.MaxCatalogBytes)
	}
	closeGrace := config.CloseGrace
	if closeGrace == 0 {
		closeGrace = defaultMCPShutdownGrace
	}
	if closeGrace < 0 {
		return nil, errors.New("executor MCP close grace must be positive")
	}
	if closeGrace > maxMCPShutdownGrace {
		return nil, fmt.Errorf("executor MCP close grace exceeds hard maximum %s", maxMCPShutdownGrace)
	}

	httpClient, transport := boundedMCPHTTPClient(httpClient, endpoint, config.BearerToken, transportMessageLimit(config.Limits))
	result := &MCPClient{
		limits:       config.Limits,
		closeGrace:   closeGrace,
		transport:    transport,
		elicitation:  config.ElicitationHandler,
		progress:     config.ProgressHandler,
		now:          time.Now,
		pendingCalls: make(map[string]*pendingMCPCall),
	}
	capture := newCatalogCapture()
	client := mcp.NewClient(
		&mcp.Implementation{Name: "agentserver-harness-worker", Version: "v2-reference"},
		&mcp.ClientOptions{
			Logger: config.Logger,
			Capabilities: &mcp.ClientCapabilities{
				Elicitation: &mcp.ElicitationCapabilities{Form: &mcp.FormElicitationCapabilities{}},
			},
			ElicitationHandler:          result.handleElicitation,
			ProgressNotificationHandler: result.handleProgress,
			MultiRoundTrip:              &mcp.MultiRoundTripOptions{Disabled: true},
		},
	)
	client.AddSendingMiddleware(capture.middleware)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint.String(),
		HTTPClient:           httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		transport.abort(err)
		return nil, fmt.Errorf("connect executor MCP: %w", err)
	}
	result.session = session
	failAfterConnect := func(primary error) (*MCPClient, error) {
		return nil, errors.Join(primary, result.closeSession(primary))
	}
	initializeResult := session.InitializeResult()
	if initializeResult == nil || initializeResult.ProtocolVersion != SupportedMCPProtocolVersion {
		negotiated := "<missing>"
		if initializeResult != nil {
			negotiated = initializeResult.ProtocolVersion
		}
		return failAfterConnect(fmt.Errorf("executor MCP negotiated protocol %q, require %q for stateful elicitation", negotiated, SupportedMCPProtocolVersion))
	}

	descriptors, err := collectToolCatalog(ctx, session, capture, config.Limits)
	if err != nil {
		return failAfterConnect(err)
	}
	catalog, err := BuildCatalog(config.Namespace, config.NamespaceDescription, descriptors, config.Limits)
	if err != nil {
		return failAfterConnect(fmt.Errorf("verify executor MCP catalog: %w", err))
	}
	if err := catalog.VerifyFrozen(config.ExpectedCatalogDigest, config.ExpectedCatalog); err != nil {
		return failAfterConnect(err)
	}
	result.catalog = catalog
	return result, nil
}

func (c *MCPClient) Catalog() *Catalog { return c.catalog }

func (c *MCPClient) Close() error {
	c.closeOnce.Do(func() {
		cause := errors.New("executor MCP client is closed")
		c.pendingMu.Lock()
		c.closed = true
		pending := make([]*pendingMCPCall, 0, len(c.pendingCalls))
		for _, call := range c.pendingCalls {
			pending = append(pending, call)
		}
		c.pendingMu.Unlock()
		for _, call := range pending {
			call.cancel(cause)
		}
		c.closeErr = c.closeSession(cause)
	})
	return c.closeErr
}

func (c *MCPClient) closeSession(cause error) error {
	if c.session == nil {
		c.transport.abort(cause)
		return nil
	}
	closed := make(chan error, 1)
	go func() { closed <- c.session.Close() }()
	timer := time.NewTimer(c.closeGrace)
	defer timer.Stop()
	select {
	case err := <-closed:
		c.transport.abort(cause)
		return err
	case <-timer.C:
	}

	graceErr := fmt.Errorf("executor MCP graceful close exceeded %s", c.closeGrace)
	c.transport.abort(graceErr)
	hardTimer := time.NewTimer(mcpAbortCompletionGrace)
	defer hardTimer.Stop()
	select {
	case err := <-closed:
		return errors.Join(graceErr, err)
	case <-hardTimer.C:
		return errors.Join(graceErr, errors.New("executor MCP session did not stop after transport abort"))
	}
}

// CallDynamicTool validates the callback against the frozen catalog, adds only
// worker-owned metadata, and makes one non-retried MCP tools/call request.
func (c *MCPClient) CallDynamicTool(ctx context.Context, call DynamicCall) (DynamicToolResult, error) {
	if ctx == nil {
		return DynamicToolResult{}, errors.New("dynamic tool call context is required")
	}
	if err := validateDynamicCallEnvelope(call); err != nil {
		return DynamicToolResult{}, err
	}
	arguments, err := c.catalog.ValidateCall(call.Namespace, call.Tool, call.Arguments)
	if err != nil {
		return DynamicToolResult{}, err
	}

	callCtx, cancel := context.WithCancelCause(ctx)
	pending := &pendingMCPCall{call: call, ctx: callCtx, cancel: cancel}
	c.pendingMu.Lock()
	if c.closed {
		c.pendingMu.Unlock()
		cancel(errors.New("executor MCP client is closed"))
		return DynamicToolResult{}, errors.New("executor MCP client is closed")
	}
	if _, duplicate := c.pendingCalls[call.CallID]; duplicate {
		c.pendingMu.Unlock()
		cancel(errors.New("duplicate dynamic tool call id"))
		return DynamicToolResult{}, fmt.Errorf("dynamic tool call id %q is already outstanding", call.CallID)
	}
	c.pendingCalls[call.CallID] = pending
	c.pendingMu.Unlock()
	defer func() {
		cancel(nil)
		c.pendingMu.Lock()
		if c.pendingCalls[call.CallID] == pending {
			delete(c.pendingCalls, call.CallID)
		}
		c.pendingMu.Unlock()
	}()

	params := &mcp.CallToolParams{
		Meta: mcp.Meta{
			MCPMetaRunID:                call.RunID,
			MCPMetaThreadID:             call.ThreadID,
			MCPMetaTurnID:               call.TurnID,
			MCPMetaCallID:               call.CallID,
			MCPMetaRunAttemptGeneration: call.RunAttemptGeneration,
			MCPMetaToolCatalogDigest:    c.catalog.Digest(),
		},
		Name:      call.Tool,
		Arguments: arguments,
	}
	params.SetProgressToken(call.CallID)
	toolResult, callErr := c.session.CallTool(callCtx, params)

	c.pendingMu.Lock()
	pendingErr := pending.err
	c.pendingMu.Unlock()
	if pendingErr != nil {
		return DynamicToolResult{}, pendingErr
	}
	if callErr != nil {
		return DynamicToolResult{}, fmt.Errorf("executor MCP tools/call %q: %w", call.Tool, callErr)
	}
	converted, err := convertToolResult(toolResult, c.limits)
	if err != nil {
		return DynamicToolResult{}, fmt.Errorf("executor MCP tools/call %q result: %w", call.Tool, err)
	}
	return converted, nil
}

func (c *MCPClient) handleElicitation(ctx context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	if request == nil || request.Params == nil {
		return nil, errors.New("executor MCP elicitation has no params")
	}
	params := request.Params
	if params.Mode != "" && params.Mode != "form" {
		return nil, fmt.Errorf("executor MCP elicitation mode %q is not allowed", params.Mode)
	}
	if params.URL != "" || params.ElicitationID != "" {
		return nil, errors.New("executor MCP URL elicitation is not allowed")
	}
	if err := validateText("MCP elicitation message", params.Message, c.limits.MaxDescriptionBytes); err != nil {
		return nil, err
	}
	if params.Message == "" {
		return nil, errors.New("executor MCP elicitation message must not be empty")
	}
	schemaRaw, err := json.Marshal(params.RequestedSchema)
	if err != nil {
		return nil, fmt.Errorf("marshal MCP elicitation schema: %w", err)
	}
	schemaValue, schemaCanonical, err := decodeCanonicalJSON(schemaRaw, c.limits.MaxSchemaBytes, c.limits)
	if err != nil {
		return nil, fmt.Errorf("MCP elicitation schema: %w", err)
	}
	if _, ok := schemaValue.(map[string]any); !ok {
		return nil, errors.New("MCP elicitation schema root must be an object")
	}

	metadata, err := parseApprovalMetadata(params.Meta)
	if err != nil {
		return nil, err
	}
	c.pendingMu.Lock()
	pending := c.pendingCalls[metadata.CallID]
	c.pendingMu.Unlock()
	if pending == nil {
		return nil, fmt.Errorf("MCP elicitation references non-outstanding call %q", metadata.CallID)
	}
	if err := metadata.matches(pending.call, c.catalog.Digest()); err != nil {
		return nil, err
	}
	if !c.now().Before(metadata.ExpiresAt) {
		return &mcp.ElicitResult{Action: string(ApprovalDecline)}, nil
	}

	decisionCtx, cancel := context.WithDeadline(ctx, metadata.ExpiresAt)
	stopPendingCancellation := context.AfterFunc(pending.ctx, cancel)
	if pending.ctx.Err() != nil {
		cancel()
	}
	defer func() {
		stopPendingCancellation()
		cancel()
	}()
	if errors.Is(decisionCtx.Err(), context.DeadlineExceeded) {
		return &mcp.ElicitResult{Action: string(ApprovalDecline)}, nil
	}
	if errors.Is(decisionCtx.Err(), context.Canceled) {
		return &mcp.ElicitResult{Action: string(ApprovalCancel)}, nil
	}
	decision, err := c.elicitation(decisionCtx, ElicitationRequest{
		RunID:                metadata.RunID,
		CallID:               metadata.CallID,
		RunAttemptGeneration: metadata.RunAttemptGeneration,
		ToolCatalogDigest:    metadata.ToolCatalogDigest,
		ExecutionID:          metadata.ExecutionID,
		ApprovalID:           metadata.ApprovalID,
		Nonce:                metadata.Nonce,
		ApprovalVersion:      metadata.ApprovalVersion,
		ContextHash:          metadata.ContextHash,
		ExpiresAt:            metadata.ExpiresAt,
		Message:              params.Message,
		RequestedSchema:      append(json.RawMessage(nil), schemaCanonical...),
	})
	if errors.Is(decisionCtx.Err(), context.DeadlineExceeded) {
		return &mcp.ElicitResult{Action: string(ApprovalDecline)}, nil
	}
	if errors.Is(decisionCtx.Err(), context.Canceled) {
		return &mcp.ElicitResult{Action: string(ApprovalCancel)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("route MCP elicitation to core: %w", err)
	}
	if !c.now().Before(metadata.ExpiresAt) {
		return &mcp.ElicitResult{Action: string(ApprovalDecline)}, nil
	}
	if err := validateElicitationDecision(decision); err != nil {
		return nil, err
	}
	return &mcp.ElicitResult{Action: string(decision.Action), Content: cloneStringAnyMap(decision.Content)}, nil
}

func (c *MCPClient) handleProgress(ctx context.Context, request *mcp.ProgressNotificationClientRequest) {
	if request == nil || request.Params == nil {
		return
	}
	params := request.Params
	callID, ok := params.ProgressToken.(string)
	if !ok || callID == "" {
		return
	}
	c.pendingMu.Lock()
	pending := c.pendingCalls[callID]
	if pending == nil {
		c.pendingMu.Unlock()
		return
	}
	fail := func(err error) {
		if pending.err == nil {
			pending.err = err
			pending.cancel(err)
		}
	}
	if !finiteNonNegative(params.Progress) || !finiteNonNegative(params.Total) || params.Total > 0 && params.Progress > params.Total {
		fail(fmt.Errorf("MCP progress for call %q has invalid progress/total", callID))
		c.pendingMu.Unlock()
		return
	}
	if err := validateText("MCP progress message", params.Message, c.limits.MaxDescriptionBytes); err != nil {
		fail(err)
		c.pendingMu.Unlock()
		return
	}
	call := pending.call
	c.pendingMu.Unlock()
	if c.progress == nil {
		return
	}
	if err := c.progress(ctx, ProgressEvent{
		RunID:                call.RunID,
		CallID:               call.CallID,
		RunAttemptGeneration: call.RunAttemptGeneration,
		Progress:             params.Progress,
		Total:                params.Total,
		Message:              params.Message,
	}); err != nil {
		c.pendingMu.Lock()
		if c.pendingCalls[callID] == pending {
			fail(fmt.Errorf("route MCP progress for call %q: %w", callID, err))
		}
		c.pendingMu.Unlock()
	}
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func validateDynamicCallEnvelope(call DynamicCall) error {
	for label, value := range map[string]string{
		"run id": call.RunID, "thread id": call.ThreadID, "turn id": call.TurnID, "call id": call.CallID,
	} {
		if err := validateNameText(label, value, maxIdentityBytes); err != nil {
			return err
		}
	}
	if call.RunAttemptGeneration < 1 {
		return errors.New("run attempt generation must be positive")
	}
	return nil
}

func convertToolResult(result *mcp.CallToolResult, limits Limits) (DynamicToolResult, error) {
	if result == nil {
		return DynamicToolResult{}, errors.New("result is nil")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return DynamicToolResult{}, fmt.Errorf("marshal result: %w", err)
	}
	if len(raw) > limits.MaxResultBytes {
		return DynamicToolResult{}, fmt.Errorf("result is %d bytes, limit is %d", len(raw), limits.MaxResultBytes)
	}
	if result.NeedsInput() || len(result.InputRequests) != 0 || result.RequestState != "" {
		return DynamicToolResult{}, errors.New("multi-round-trip MCP result is not allowed")
	}
	if len(result.Content) > limits.MaxResultItems {
		return DynamicToolResult{}, fmt.Errorf("result has %d content items, limit is %d", len(result.Content), limits.MaxResultItems)
	}
	items := make([]InputTextContent, 0, len(result.Content)+1)
	totalText := 0
	appendText := func(text string) error {
		if !utf8.ValidString(text) {
			return errors.New("result text is not valid UTF-8")
		}
		if len(text) > limits.MaxResultTextBytes-totalText {
			return fmt.Errorf("result text exceeds %d bytes", limits.MaxResultTextBytes)
		}
		totalText += len(text)
		items = append(items, InputTextContent{Type: "inputText", Text: text})
		return nil
	}
	for index, content := range result.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok || text == nil {
			return DynamicToolResult{}, fmt.Errorf("result content item %d has forbidden type %T", index, content)
		}
		if err := appendText(text.Text); err != nil {
			return DynamicToolResult{}, fmt.Errorf("result content item %d: %w", index, err)
		}
	}
	if result.StructuredContent != nil {
		if len(items) >= limits.MaxResultItems {
			return DynamicToolResult{}, fmt.Errorf("result exceeds %d projected content items", limits.MaxResultItems)
		}
		structured, err := jcs.Append(nil, result.StructuredContent)
		if err != nil {
			return DynamicToolResult{}, fmt.Errorf("canonicalize structured content: %w", err)
		}
		if err := appendText(string(structured)); err != nil {
			return DynamicToolResult{}, fmt.Errorf("structured content: %w", err)
		}
	}
	if len(items) == 0 {
		return DynamicToolResult{}, errors.New("result has no text or structured content")
	}
	return DynamicToolResult{ContentItems: items, Success: !result.IsError}, nil
}

type approvalMetadata struct {
	RunID                string
	CallID               string
	RunAttemptGeneration int64
	ToolCatalogDigest    string
	ExecutionID          string
	ApprovalID           string
	Nonce                string
	ApprovalVersion      int64
	ContextHash          string
	ExpiresAt            time.Time
}

func parseApprovalMetadata(meta mcp.Meta) (approvalMetadata, error) {
	allowed := map[string]struct{}{
		MCPMetaRunID: {}, MCPMetaCallID: {}, MCPMetaRunAttemptGeneration: {}, MCPMetaToolCatalogDigest: {},
		MCPMetaExecutionID: {}, MCPMetaApprovalID: {}, MCPMetaApprovalNonce: {}, MCPMetaApprovalVersion: {}, MCPMetaContextHash: {}, MCPMetaExpiresAt: {},
		"progressToken": {},
	}
	for key := range meta {
		if _, ok := allowed[key]; !ok {
			return approvalMetadata{}, fmt.Errorf("executor MCP elicitation has unsupported metadata key %q", key)
		}
	}
	getString := func(key string) (string, error) {
		value, ok := meta[key].(string)
		if !ok || value == "" {
			return "", fmt.Errorf("executor MCP elicitation metadata %q must be a non-empty string", key)
		}
		if err := validateText("MCP metadata "+key, value, maxIdentityBytes); err != nil {
			return "", err
		}
		return value, nil
	}
	var result approvalMetadata
	var err error
	if result.RunID, err = getString(MCPMetaRunID); err != nil {
		return approvalMetadata{}, err
	}
	if result.CallID, err = getString(MCPMetaCallID); err != nil {
		return approvalMetadata{}, err
	}
	if progressToken, exists := meta["progressToken"]; exists {
		if token, ok := progressToken.(string); !ok || token != result.CallID {
			return approvalMetadata{}, errors.New("executor MCP elicitation progressToken must equal callId")
		}
	}
	if result.ToolCatalogDigest, err = getString(MCPMetaToolCatalogDigest); err != nil {
		return approvalMetadata{}, err
	}
	if result.ExecutionID, err = getString(MCPMetaExecutionID); err != nil {
		return approvalMetadata{}, err
	}
	if result.ApprovalID, err = getString(MCPMetaApprovalID); err != nil {
		return approvalMetadata{}, err
	}
	if result.Nonce, err = getString(MCPMetaApprovalNonce); err != nil {
		return approvalMetadata{}, err
	}
	result.ApprovalVersion, err = metadataInt64(meta[MCPMetaApprovalVersion])
	if err != nil || result.ApprovalVersion < 1 {
		return approvalMetadata{}, errors.New("executor MCP elicitation approvalVersion must be a positive integer")
	}
	if result.ContextHash, err = getString(MCPMetaContextHash); err != nil {
		return approvalMetadata{}, err
	}
	expiresAt, err := getString(MCPMetaExpiresAt)
	if err != nil {
		return approvalMetadata{}, err
	}
	result.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return approvalMetadata{}, fmt.Errorf("executor MCP elicitation expiresAt is invalid: %w", err)
	}
	result.RunAttemptGeneration, err = metadataInt64(meta[MCPMetaRunAttemptGeneration])
	if err != nil || result.RunAttemptGeneration < 1 {
		return approvalMetadata{}, errors.New("executor MCP elicitation runAttemptGeneration must be a positive integer")
	}
	if !equalDigest(result.ToolCatalogDigest, result.ToolCatalogDigest) || !equalDigest(result.ContextHash, result.ContextHash) {
		return approvalMetadata{}, errors.New("executor MCP elicitation catalog/context digest is invalid")
	}
	return result, nil
}

func metadataInt64(value any) (int64, error) {
	switch value := value.(type) {
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) || value > math.MaxInt64 || value < math.MinInt64 {
			return 0, errors.New("not an integer")
		}
		return int64(value), nil
	case int:
		return int64(value), nil
	case int64:
		return value, nil
	case json.Number:
		return value.Int64()
	default:
		return 0, fmt.Errorf("unexpected integer type %T", value)
	}
}

func (m approvalMetadata) matches(call DynamicCall, catalogDigest string) error {
	if m.RunID != call.RunID || m.CallID != call.CallID || m.RunAttemptGeneration != call.RunAttemptGeneration {
		return errors.New("executor MCP elicitation run/call/generation does not match outstanding call")
	}
	if !equalDigest(m.ToolCatalogDigest, catalogDigest) {
		return errors.New("executor MCP elicitation catalog digest does not match outstanding call")
	}
	return nil
}

func validateElicitationDecision(decision ElicitationDecision) error {
	switch decision.Action {
	case ApprovalAccept:
		if decision.Content == nil {
			return errors.New("accepted MCP elicitation requires content")
		}
	case ApprovalDecline, ApprovalCancel:
		if decision.Content != nil {
			return fmt.Errorf("%s MCP elicitation must not include content", decision.Action)
		}
	default:
		return fmt.Errorf("unsupported MCP elicitation decision %q", decision.Action)
	}
	return nil
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

type capturedCatalogPage struct {
	cursor     string
	nextCursor string
	tools      []ToolDescriptor
}

type catalogCapture struct {
	mu    sync.Mutex
	pages []capturedCatalogPage
}

func newCatalogCapture() *catalogCapture { return &catalogCapture{} }

func (c *catalogCapture) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, request)
		if err != nil || method != "tools/list" {
			return result, err
		}
		listed, ok := result.(*mcp.ListToolsResult)
		if !ok || listed == nil {
			return nil, errors.New("executor MCP tools/list returned an unexpected result type")
		}
		params, ok := request.GetParams().(*mcp.ListToolsParams)
		if !ok || params == nil {
			return nil, errors.New("executor MCP tools/list used unexpected params")
		}
		page := capturedCatalogPage{cursor: params.Cursor, nextCursor: listed.NextCursor, tools: make([]ToolDescriptor, 0, len(listed.Tools))}
		for index, tool := range listed.Tools {
			if tool == nil {
				return nil, fmt.Errorf("executor MCP tools/list item %d is null", index)
			}
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil {
				return nil, fmt.Errorf("executor MCP tools/list item %d schema: %w", index, err)
			}
			page.tools = append(page.tools, ToolDescriptor{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: json.RawMessage(schema),
			})
		}
		c.mu.Lock()
		c.pages = append(c.pages, page)
		c.mu.Unlock()
		return result, nil
	}
}

func (c *catalogCapture) take(cursor string) (capturedCatalogPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pages) == 0 {
		return capturedCatalogPage{}, errors.New("official MCP SDK did not expose the raw tools/list page")
	}
	page := c.pages[0]
	c.pages = c.pages[1:]
	if page.cursor != cursor {
		return capturedCatalogPage{}, fmt.Errorf("captured MCP tools/list cursor %q, want %q", page.cursor, cursor)
	}
	return page, nil
}

func collectToolCatalog(ctx context.Context, session *mcp.ClientSession, capture *catalogCapture, limits Limits) ([]ToolDescriptor, error) {
	var descriptors []ToolDescriptor
	cursor := ""
	seenCursors := map[string]struct{}{"": {}}
	for pageCount := 0; pageCount <= limits.MaxTools; pageCount++ {
		listed, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("list executor MCP tools at cursor %q: %w", cursor, err)
		}
		page, err := capture.take(cursor)
		if err != nil {
			return nil, err
		}
		if listed.NextCursor != page.nextCursor {
			return nil, errors.New("official MCP SDK changed the captured tools/list cursor")
		}
		if len(page.tools) > limits.MaxTools-len(descriptors) {
			return nil, fmt.Errorf("executor MCP catalog exceeds %d tools", limits.MaxTools)
		}
		descriptors = append(descriptors, page.tools...)
		if page.nextCursor == "" {
			return descriptors, nil
		}
		if _, duplicate := seenCursors[page.nextCursor]; duplicate {
			return nil, fmt.Errorf("executor MCP tools/list repeated cursor %q", page.nextCursor)
		}
		seenCursors[page.nextCursor] = struct{}{}
		cursor = page.nextCursor
	}
	return nil, fmt.Errorf("executor MCP tools/list exceeds %d pages", limits.MaxTools+1)
}

func validateMCPEndpoint(raw string, allowInsecureLoopback bool) (*url.URL, error) {
	endpoint, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse executor MCP endpoint: %w", err)
	}
	if !endpoint.IsAbs() || endpoint.Opaque != "" || endpoint.Host == "" {
		return nil, errors.New("executor MCP endpoint must be an absolute hierarchical URL")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("executor MCP endpoint cannot contain userinfo, query, or fragment")
	}
	if endpoint.Path == "" || endpoint.Path == "/" || path.Clean(endpoint.Path) != endpoint.Path || strings.Contains(endpoint.EscapedPath(), "%") {
		return nil, errors.New("executor MCP endpoint must have a canonical, unescaped non-root path")
	}
	if endpoint.Hostname() != strings.ToLower(endpoint.Hostname()) {
		return nil, errors.New("executor MCP endpoint host must be lowercase canonical form")
	}
	if ip := net.ParseIP(endpoint.Hostname()); ip != nil && ip.To4() == nil {
		return nil, errors.New("executor MCP endpoint must be IPv4 in the Phase 1 profile")
	}
	switch endpoint.Scheme {
	case "https":
	case "http":
		ip := net.ParseIP(endpoint.Hostname())
		if !allowInsecureLoopback || ip == nil || !ip.IsLoopback() || ip.To4() == nil {
			return nil, errors.New("plain HTTP executor MCP endpoint is allowed only for an explicit IPv4 loopback test")
		}
	default:
		return nil, errors.New("executor MCP endpoint scheme must be https")
	}
	return endpoint, nil
}

func secureMCPHTTPClient(source *http.Client, expectedIdentity string) (*http.Client, error) {
	parsedIdentity, err := url.Parse(expectedIdentity)
	if err != nil || parsedIdentity.Scheme != "spiffe" || parsedIdentity.Host == "" || parsedIdentity.Path == "" ||
		parsedIdentity.User != nil || parsedIdentity.RawQuery != "" || parsedIdentity.Fragment != "" ||
		parsedIdentity.String() != expectedIdentity {
		return nil, errors.New("executor MCP server TLS identity must be a canonical SPIFFE URI")
	}
	if source == nil {
		return nil, errors.New("executor MCP HTTPS client is required")
	}
	transport, ok := source.Transport.(*http.Transport)
	if !ok || transport == nil {
		return nil, errors.New("executor MCP HTTP client must use an explicit *http.Transport")
	}
	transport = transport.Clone()
	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	if tlsConfig.InsecureSkipVerify {
		return nil, errors.New("executor MCP TLS verification cannot be disabled")
	}
	if tlsConfig.MinVersion < tls.VersionTLS13 {
		tlsConfig.MinVersion = tls.VersionTLS13
	}
	priorVerify := tlsConfig.VerifyConnection
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if priorVerify != nil {
			if err := priorVerify(state); err != nil {
				return err
			}
		}
		if len(state.PeerCertificates) == 0 || len(state.PeerCertificates[0].URIs) != 1 ||
			state.PeerCertificates[0].URIs[0].String() != expectedIdentity {
			return errors.New("executor MCP server certificate has the wrong SPIFFE URI SAN")
		}
		return nil
	}
	transport.TLSClientConfig = tlsConfig
	client := *source
	client.Transport = transport
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("executor MCP redirects are forbidden")
	}
	return &client, nil
}

func validateBearer(token string) error {
	if token == "" {
		return errors.New("executor MCP bearer token is required")
	}
	if len(token) > maxBearerBytes {
		return fmt.Errorf("executor MCP bearer token exceeds %d bytes", maxBearerBytes)
	}
	for _, character := range []byte(token) {
		if character <= ' ' || character >= 0x7f {
			return errors.New("executor MCP bearer token contains an invalid byte")
		}
	}
	return nil
}

func transportMessageLimit(limits Limits) int64 {
	maximum := limits.MaxCatalogBytes
	for _, candidate := range []int{limits.MaxArgumentBytes, limits.MaxResultBytes} {
		if candidate > maximum {
			maximum = candidate
		}
	}
	return int64(maximum) + int64(transportOverhead)
}

func boundedMCPHTTPClient(base *http.Client, endpoint *url.URL, bearer string, maxBytes int64) (*http.Client, *exactMCPTransport) {
	var client http.Client
	if base != nil {
		client = *base
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	var closeIdle func()
	if cloneable, ok := transport.(*http.Transport); ok {
		cloned := cloneable.Clone()
		transport = cloned
		closeIdle = cloned.CloseIdleConnections
	}
	exact := &exactMCPTransport{
		base:      transport,
		endpoint:  endpoint.String(),
		bearer:    bearer,
		maxBytes:  maxBytes,
		active:    make(map[*exactMCPRequest]context.CancelCauseFunc),
		closeIdle: closeIdle,
	}
	client.Transport = exact
	client.Timeout = 0
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client, exact
}

// The byte makes request identities non-zero-sized; Go permits pointers to
// distinct zero-sized values to compare equal, which would collapse tracking.
type exactMCPRequest struct{ _ byte }

type exactMCPTransport struct {
	base     http.RoundTripper
	endpoint string
	bearer   string
	maxBytes int64

	mu        sync.Mutex
	closed    bool
	active    map[*exactMCPRequest]context.CancelCauseFunc
	closeIdle func()
}

func (t *exactMCPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.String() != t.endpoint {
		return nil, fmt.Errorf("executor MCP transport refused target %q", request.URL.String())
	}
	request, complete, err := t.track(request)
	if err != nil {
		return nil, err
	}
	request.Header = request.Header.Clone()
	request.Header.Set("Authorization", "Bearer "+t.bearer)
	if request.ContentLength > t.maxBytes {
		complete()
		return nil, fmt.Errorf("executor MCP request is %d bytes, limit is %d", request.ContentLength, t.maxBytes)
	}
	if request.Body != nil {
		request.Body = newBoundedReadCloser(request.Body, t.maxBytes)
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		complete()
		return nil, err
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		_ = response.Body.Close()
		complete()
		return nil, fmt.Errorf("executor MCP endpoint returned forbidden redirect status %d", response.StatusCode)
	}
	if response.ContentLength > t.maxBytes {
		_ = response.Body.Close()
		complete()
		return nil, fmt.Errorf("executor MCP response is %d bytes, limit is %d", response.ContentLength, t.maxBytes)
	}
	response.Body = &trackedMCPResponseBody{
		body:     newBoundedReadCloser(response.Body, t.maxBytes),
		complete: complete,
	}
	return response, nil
}

func (t *exactMCPTransport) track(request *http.Request) (*http.Request, func(), error) {
	ctx, cancel := context.WithCancelCause(request.Context())
	tracked := &exactMCPRequest{}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		cancel(errExecutorMCPTransportClosed)
		return nil, nil, errExecutorMCPTransportClosed
	}
	t.active[tracked] = cancel
	t.mu.Unlock()
	var once sync.Once
	complete := func() {
		once.Do(func() {
			t.mu.Lock()
			delete(t.active, tracked)
			t.mu.Unlock()
			cancel(nil)
		})
	}
	return request.Clone(ctx), complete, nil
}

func (t *exactMCPTransport) abort(cause error) {
	if cause == nil {
		cause = errExecutorMCPTransportClosed
	}
	t.mu.Lock()
	t.closed = true
	cancellations := make([]context.CancelCauseFunc, 0, len(t.active))
	for _, cancel := range t.active {
		cancellations = append(cancellations, cancel)
	}
	t.active = make(map[*exactMCPRequest]context.CancelCauseFunc)
	t.mu.Unlock()
	for _, cancel := range cancellations {
		cancel(cause)
	}
	if t.closeIdle != nil {
		t.closeIdle()
	}
}

type trackedMCPResponseBody struct {
	body     io.ReadCloser
	complete func()
	once     sync.Once
}

func (b *trackedMCPResponseBody) Read(buffer []byte) (int, error) {
	read, err := b.body.Read(buffer)
	if err != nil {
		b.finish()
	}
	return read, err
}

func (b *trackedMCPResponseBody) Close() error {
	err := b.body.Close()
	b.finish()
	return err
}

func (b *trackedMCPResponseBody) finish() {
	b.once.Do(b.complete)
}

type boundedReadCloser struct {
	body      io.ReadCloser
	remaining int64
	limit     int64
	exceeded  bool
}

func newBoundedReadCloser(body io.ReadCloser, limit int64) *boundedReadCloser {
	return &boundedReadCloser{body: body, remaining: limit, limit: limit}
}

func (r *boundedReadCloser) Read(buffer []byte) (int, error) {
	if r.exceeded {
		return 0, &http.MaxBytesError{Limit: r.limit}
	}
	if int64(len(buffer)) > r.remaining+1 {
		buffer = buffer[:r.remaining+1]
	}
	read, err := r.body.Read(buffer)
	if int64(read) > r.remaining {
		allowed := int(r.remaining)
		r.remaining = 0
		r.exceeded = true
		return allowed, &http.MaxBytesError{Limit: r.limit}
	}
	r.remaining -= int64(read)
	return read, err
}

func (r *boundedReadCloser) Close() error { return r.body.Close() }
