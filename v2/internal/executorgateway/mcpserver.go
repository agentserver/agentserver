package executorgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ExecutorMCPPath            = "/mcp"
	ExecutorMCPProtocolVersion = "2025-11-25"

	defaultExecutorMCPMaxSessions      = 256
	defaultExecutorMCPSessionTimeout   = 30 * time.Minute
	defaultExecutorMCPMaxRequestBytes  = 2 * 1024 * 1024
	maximumExecutorMCPMaxSessions      = 4096
	maximumExecutorMCPSessionTimeout   = 24 * time.Hour
	maximumExecutorMCPMaxRequestBytes  = 8 * 1024 * 1024
	maximumExecutorMCPCapabilityIDSize = 256
	maximumExecutorMCPSessionIDTries   = 8
	maximumExecutorMCPApprovalTTL      = 24 * time.Hour

	mcpSessionIDHeader = "Mcp-Session-Id"

	executorMCPMetaRunID                = "io.agentserver/runId"
	executorMCPMetaThreadID             = "io.agentserver/threadId"
	executorMCPMetaTurnID               = "io.agentserver/turnId"
	executorMCPMetaCallID               = "io.agentserver/callId"
	executorMCPMetaRunAttemptGeneration = "io.agentserver/runAttemptGeneration"
	executorMCPMetaToolCatalogDigest    = "io.agentserver/toolCatalogDigest"
	executorMCPMetaExecutionID          = "io.agentserver/executionId"
	executorMCPMetaApprovalID           = "io.agentserver/approvalId"
	executorMCPMetaApprovalNonce        = "io.agentserver/approvalNonce"
	executorMCPMetaApprovalVersion      = "io.agentserver/approvalVersion"
	executorMCPMetaContextHash          = "io.agentserver/contextHash"
	executorMCPMetaExpiresAt            = "io.agentserver/expiresAt"
	executorMCPMetaProgressToken        = "progressToken"
)

var errExecutorMCPShuttingDown = errors.New("executor MCP server is shutting down")

// ExecutorMCPPrincipal is the immutable authorization projection for one MCP
// session. CapabilityID must identify one concrete credential (normally its
// jti), so a different run capability cannot reuse a known MCP session ID.
// AuthenticateExecutorMCP is still invoked for every HTTP request and must
// perform live expiry, generation, and authorization checks.
type ExecutorMCPPrincipal struct {
	CapabilityID string
	WorkspaceID  string
	// SessionID is capability-derived authority metadata. It is not the MCP
	// transport session ID; managed backends bind every sandbox operation to
	// this agentserver session.
	SessionID         string
	ActorID           string
	ToolCatalogDigest string
	MaxApprovalTTL    time.Duration
	// RunDeadline and CapabilityExpiresAt are absolute, signed bounds. The
	// latter may include a small cleanup grace, but no new approval may outlive
	// either bound.
	RunDeadline         time.Time
	CapabilityExpiresAt time.Time
	Run                 ExecutorMCPRunContext
	// ExecutorID optionally narrows list_environments to one executor. The
	// empty value grants the workspace-wide registry projection.
	ExecutorID string
	// Production requires every selected and listed environment to be backed
	// by a non-insecure enrollment. Development principals leave this false.
	Production     bool
	ManagedSandbox *ExecutorManagedSandboxAuthority
}

type ExecutorManagedSandboxAuthority struct {
	SettingVersion int64
	Region         string
	ProfileID      string
	BindingSHA256  string
	EnvironmentID  string
}

// ExecutorMCPRunContext is the immutable, capability-derived core command
// context for one worker attempt. Tool arguments and MCP metadata may only
// correlate a call with this context; they never supply or override it.
type ExecutorMCPRunContext struct {
	RunID                     string
	RunAttemptID              string
	RunAttemptGeneration      int64
	HolderID                  string
	ExpectedRunVersion        int64
	ExpectedRunAttemptVersion int64
}

type ExecutorMCPAuthenticator interface {
	AuthenticateExecutorMCP(*http.Request) (ExecutorMCPPrincipal, error)
}

type ExecutorMCPConfig struct {
	MaxSessions            int
	SessionTimeout         time.Duration
	MaxRequestBodyBytes    int64
	IDGenerator            IDGenerator
	Logger                 *slog.Logger
	ShellExecutor          *ShellExecutor
	ReadFileExecutor       *ReadFileExecutor
	ManagedSandboxAcquirer ManagedSandboxSessionAcquirer
}

func DefaultExecutorMCPConfig() ExecutorMCPConfig {
	return ExecutorMCPConfig{
		MaxSessions:         defaultExecutorMCPMaxSessions,
		SessionTimeout:      defaultExecutorMCPSessionTimeout,
		MaxRequestBodyBytes: defaultExecutorMCPMaxRequestBytes,
		IDGenerator:         newRandomUUID,
	}
}

type executorMCPServerContextKey struct{}

type executorMCPSession struct {
	id        string
	principal ExecutorMCPPrincipal
	server    *mcp.Server
	pending   bool

	managedMu    sync.Mutex
	managedLease ManagedSandboxSessionLease
}

// ExecutorMCPHandler exposes the implemented executor tools over one bounded,
// stateful Streamable HTTP endpoint. The official SDK owns protocol framing;
// this wrapper owns authentication, exact protocol selection, session
// authorization binding, and shutdown.
type ExecutorMCPHandler struct {
	authenticator ExecutorMCPAuthenticator
	resolver      *EnvironmentResolver
	shell         *ShellExecutor
	readFile      *ReadFileExecutor
	config        ExecutorMCPConfig
	streamable    *mcp.StreamableHTTPHandler

	mu           sync.Mutex
	shuttingDown bool
	sessions     map[string]*executorMCPSession
}

func NewExecutorMCPHandler(authenticator ExecutorMCPAuthenticator, resolver *EnvironmentResolver, config ExecutorMCPConfig) (*ExecutorMCPHandler, error) {
	if authenticator == nil {
		return nil, errors.New("executor MCP authenticator is required")
	}
	if resolver == nil {
		return nil, errors.New("environment resolver is required")
	}
	if config.MaxSessions < 1 || config.MaxSessions > maximumExecutorMCPMaxSessions {
		return nil, fmt.Errorf("executor MCP max sessions must be between 1 and %d", maximumExecutorMCPMaxSessions)
	}
	if config.SessionTimeout <= 0 || config.SessionTimeout > maximumExecutorMCPSessionTimeout {
		return nil, fmt.Errorf("executor MCP session timeout must be positive and at most %s", maximumExecutorMCPSessionTimeout)
	}
	if config.MaxRequestBodyBytes < 1 || config.MaxRequestBodyBytes > maximumExecutorMCPMaxRequestBytes {
		return nil, fmt.Errorf("executor MCP request body limit must be between 1 and %d bytes", maximumExecutorMCPMaxRequestBytes)
	}
	if config.IDGenerator == nil {
		return nil, errors.New("executor MCP session ID generator is required")
	}

	handler := &ExecutorMCPHandler{
		authenticator: authenticator,
		resolver:      resolver,
		shell:         config.ShellExecutor,
		readFile:      config.ReadFileExecutor,
		config:        config,
		sessions:      make(map[string]*executorMCPSession),
	}
	handler.streamable = mcp.NewStreamableHTTPHandler(
		func(request *http.Request) *mcp.Server {
			server, _ := request.Context().Value(executorMCPServerContextKey{}).(*mcp.Server)
			return server
		},
		&mcp.StreamableHTTPOptions{
			Stateless:           false,
			SessionTimeout:      config.SessionTimeout,
			MaxRequestBodyBytes: config.MaxRequestBodyBytes,
			Logger:              config.Logger,
		},
	)
	return handler, nil
}

func (handler *ExecutorMCPHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request == nil || request.URL == nil || request.URL.Path != ExecutorMCPPath || request.URL.RawPath != "" ||
		request.URL.RawQuery != "" || request.URL.ForceQuery {
		http.NotFound(response, request)
		return
	}
	response = &executorMCPResponseWriter{ResponseWriter: response}
	response.Header().Set("Cache-Control", "no-store, no-transform")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Method != http.MethodPost && request.Method != http.MethodGet && request.Method != http.MethodDelete {
		response.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// A browser origin is never a valid harness-worker. Reject it before
	// bearer authentication so this endpoint cannot become a CSRF oracle.
	if request.Header.Get("Origin") != "" {
		http.Error(response, "browser origins are forbidden", http.StatusForbidden)
		return
	}

	principal, err := handler.authenticator.AuthenticateExecutorMCP(request)
	if err != nil {
		response.Header().Set("WWW-Authenticate", `Bearer realm="executor-gateway"`)
		http.Error(response, "executor MCP authentication failed", http.StatusUnauthorized)
		return
	}
	if err := validateExecutorMCPPrincipal(principal); err != nil {
		http.Error(response, "executor MCP authentication produced an invalid principal", http.StatusInternalServerError)
		return
	}

	sessionID, err := requestSessionID(request)
	if err != nil {
		http.Error(response, "invalid MCP session header", http.StatusBadRequest)
		return
	}
	var session *executorMCPSession
	if sessionID == "" {
		if request.Method == http.MethodPost {
			session, err = handler.prepareSession(principal)
			if err != nil {
				status := http.StatusServiceUnavailable
				if !errors.Is(err, errExecutorMCPShuttingDown) {
					status = http.StatusInternalServerError
					if errors.Is(err, errExecutorMCPSessionLimit) {
						status = http.StatusServiceUnavailable
					}
				}
				http.Error(response, "executor MCP session is unavailable", status)
				return
			}
			defer handler.finishPreparedSession(session)
		}
	} else {
		session, err = handler.authorizeSession(sessionID, principal)
		if err != nil {
			if errors.Is(err, errExecutorMCPShuttingDown) {
				http.Error(response, "executor MCP server is shutting down", http.StatusServiceUnavailable)
				return
			}
			http.Error(response, "MCP session is not authorized by this capability", http.StatusForbidden)
			return
		}
		if request.Method == http.MethodDelete {
			defer handler.finishExistingSession(sessionID, session)
		}
	}

	if session != nil {
		request = request.WithContext(context.WithValue(request.Context(), executorMCPServerContextKey{}, session.server))
	}
	handler.streamable.ServeHTTP(response, request)
}

type executorMCPResponseWriter struct {
	http.ResponseWriter
}

func (writer *executorMCPResponseWriter) WriteHeader(status int) {
	writer.Header().Set("Cache-Control", "no-store, no-transform")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *executorMCPResponseWriter) Write(body []byte) (int, error) {
	writer.Header().Set("Cache-Control", "no-store, no-transform")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	return writer.ResponseWriter.Write(body)
}

func (writer *executorMCPResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

var errExecutorMCPSessionLimit = errors.New("executor MCP session limit reached")

func (handler *ExecutorMCPHandler) prepareSession(principal ExecutorMCPPrincipal) (*executorMCPSession, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.shuttingDown {
		return nil, errExecutorMCPShuttingDown
	}
	handler.sweepClosedSessionsLocked()
	if len(handler.sessions) >= handler.config.MaxSessions {
		return nil, errExecutorMCPSessionLimit
	}
	var sessionID string
	for range maximumExecutorMCPSessionIDTries {
		candidate, err := handler.config.IDGenerator()
		if err != nil {
			return nil, fmt.Errorf("generate executor MCP session ID: %w", err)
		}
		if err := validateExecutorMCPSessionID(candidate); err != nil {
			return nil, err
		}
		if _, duplicate := handler.sessions[candidate]; !duplicate {
			sessionID = candidate
			break
		}
	}
	if sessionID == "" {
		return nil, errors.New("executor MCP session ID generator repeatedly collided")
	}
	session := &executorMCPSession{id: sessionID, principal: principal, pending: true}
	session.server = handler.newScopedServer(session)
	handler.sessions[sessionID] = session
	return session, nil
}

func (handler *ExecutorMCPHandler) newScopedServer(session *executorMCPSession) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "agentserver-executor-gateway", Version: mcpcontract.Version},
		&mcp.ServerOptions{
			Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
			PageSize:     16,
			GetSessionID: func() string { return session.id },
			Logger:       handler.config.Logger,
		},
	)
	server.AddReceivingMiddleware(requireExecutorMCPProtocol)
	tool, found := mcpcontract.Lookup(mcpcontract.ToolListEnvironments)
	if !found {
		panic("list_environments is missing from executor MCP contract")
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:         tool.Name,
		Description:  tool.Description,
		InputSchema:  tool.InputSchema,
		OutputSchema: tool.OutputSchema,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input listEnvironmentsInput) (*mcp.CallToolResult, ListEnvironmentsResult, error) {
		if request == nil || request.Session == nil || request.Session.ID() != session.id {
			return nil, ListEnvironmentsResult{}, errors.New("list_environments arrived without its authenticated MCP session")
		}
		if request.Params == nil || len(request.Params.InputResponses) != 0 || request.Params.RequestState != "" {
			return nil, ListEnvironmentsResult{}, errors.New("list_environments does not support multi-round-trip input")
		}
		executorID := input.ExecutorID
		if session.principal.ExecutorID != "" {
			if executorID != "" && executorID != session.principal.ExecutorID {
				return nil, ListEnvironmentsResult{}, errors.New("executor_id is outside the authenticated run capability")
			}
			executorID = session.principal.ExecutorID
		}
		toolContext, finishManaged, err := handler.managedToolContext(ctx, session)
		if err != nil {
			if handler.config.Logger != nil {
				handler.config.Logger.ErrorContext(ctx, "acquire managed sandbox for list_environments",
					"workspace_id", session.principal.WorkspaceID,
					"session_id", session.principal.SessionID,
					"run_id", session.principal.Run.RunID,
					"error", err,
				)
			}
			return nil, ListEnvironmentsResult{}, errors.New("list_environments is temporarily unavailable")
		}
		defer finishManaged()
		result, err := handler.resolver.ListForPrincipal(toolContext, session.principal, executorID, session.principal.Production)
		if err != nil {
			if handler.config.Logger != nil {
				handler.config.Logger.ErrorContext(ctx, "list executor MCP environments",
					"workspace_id", session.principal.WorkspaceID,
					"executor_id", executorID,
					"error", err,
				)
			}
			return nil, ListEnvironmentsResult{}, errors.New("list_environments is temporarily unavailable")
		}
		// A non-nil empty Content slice prevents the SDK from duplicating the
		// structured JSON as a second text result. harness-worker projects the
		// validated structured value exactly once.
		return &mcp.CallToolResult{Content: []mcp.Content{}}, result, nil
	})
	if handler.shell != nil {
		shellTool, found := mcpcontract.Lookup(mcpcontract.ToolShell)
		if !found {
			panic("shell is missing from executor MCP contract")
		}
		mcp.AddTool(server, &mcp.Tool{
			Name:         shellTool.Name,
			Description:  shellTool.Description,
			InputSchema:  shellTool.InputSchema,
			OutputSchema: shellTool.OutputSchema,
		}, func(ctx context.Context, request *mcp.CallToolRequest, _ ShellV1Arguments) (*mcp.CallToolResult, ShellV1Result, error) {
			if request == nil || request.Session == nil || request.Session.ID() != session.id || request.Params == nil {
				return nil, ShellV1Result{}, errors.New("shell arrived without its authenticated MCP session")
			}
			if len(request.Params.InputResponses) != 0 || request.Params.RequestState != "" {
				return nil, ShellV1Result{}, errors.New("shell does not support multi-round-trip input")
			}
			call, err := parseExecutorMCPCallContext(request.Params.Meta, session.principal)
			if err != nil {
				return nil, ShellV1Result{}, err
			}
			toolContext, finishManaged, err := handler.managedToolContext(ctx, session)
			if err != nil {
				if handler.config.Logger != nil {
					handler.config.Logger.ErrorContext(ctx, "acquire managed sandbox for shell",
						"run_id", session.principal.Run.RunID,
						"call_id", call.CallID,
						"error", err,
					)
				}
				return nil, ShellV1Result{}, errors.New("shell execution is temporarily unavailable")
			}
			defer finishManaged()
			result, err := handler.shell.Execute(toolContext, ShellExecuteRequest{
				Principal: session.principal, ToolCallID: call.CallID,
				Arguments: append(json.RawMessage(nil), request.Params.Arguments...),
				Elicitor:  request.Session,
			})
			if err != nil {
				if handler.config.Logger != nil {
					handler.config.Logger.ErrorContext(ctx, "execute shell MCP call",
						"run_id", session.principal.Run.RunID,
						"call_id", call.CallID,
						"error", err,
					)
				}
				return nil, ShellV1Result{}, errors.New("shell execution is temporarily unavailable")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{}}, result, nil
		})
	}
	if handler.readFile != nil {
		readFileTool, found := mcpcontract.Lookup(mcpcontract.ToolReadFile)
		if !found {
			panic("read_file is missing from executor MCP contract")
		}
		mcp.AddTool(server, &mcp.Tool{
			Name:         readFileTool.Name,
			Description:  readFileTool.Description,
			InputSchema:  readFileTool.InputSchema,
			OutputSchema: readFileTool.OutputSchema,
		}, func(ctx context.Context, request *mcp.CallToolRequest, _ ReadFileV1Arguments) (*mcp.CallToolResult, ReadFileV1Result, error) {
			if request == nil || request.Session == nil || request.Session.ID() != session.id || request.Params == nil {
				return nil, ReadFileV1Result{}, errors.New("read_file arrived without its authenticated MCP session")
			}
			if len(request.Params.InputResponses) != 0 || request.Params.RequestState != "" {
				return nil, ReadFileV1Result{}, errors.New("read_file does not support multi-round-trip input")
			}
			call, err := parseExecutorMCPCallContext(request.Params.Meta, session.principal)
			if err != nil {
				return nil, ReadFileV1Result{}, err
			}
			toolContext, finishManaged, err := handler.managedToolContext(ctx, session)
			if err != nil {
				if handler.config.Logger != nil {
					handler.config.Logger.ErrorContext(ctx, "acquire managed sandbox for read_file",
						"run_id", session.principal.Run.RunID,
						"call_id", call.CallID,
						"error", err,
					)
				}
				return nil, ReadFileV1Result{}, errors.New("read_file execution is temporarily unavailable")
			}
			defer finishManaged()
			result, err := handler.readFile.Execute(toolContext, ReadFileExecuteRequest{
				Principal: session.principal, ToolCallID: call.CallID,
				Arguments: append(json.RawMessage(nil), request.Params.Arguments...),
				Elicitor:  request.Session,
			})
			if err != nil {
				if handler.config.Logger != nil {
					handler.config.Logger.ErrorContext(ctx, "execute read_file MCP call",
						"run_id", session.principal.Run.RunID,
						"call_id", call.CallID,
						"error", err,
					)
				}
				return nil, ReadFileV1Result{}, errors.New("read_file execution is temporarily unavailable")
			}
			return &mcp.CallToolResult{Content: []mcp.Content{}}, result, nil
		})
	}
	return server
}

func (handler *ExecutorMCPHandler) managedToolContext(
	ctx context.Context,
	session *executorMCPSession,
) (context.Context, func(), error) {
	if handler.config.ManagedSandboxAcquirer == nil {
		return ctx, func() {}, nil
	}
	lease, err := session.acquireManagedSandbox(ctx, handler.config.ManagedSandboxAcquirer)
	if err != nil {
		return nil, nil, err
	}
	select {
	case <-lease.Done():
		if err := lease.Err(); err != nil {
			return nil, nil, err
		}
		return nil, nil, errors.New("managed sandbox activity ended before executor dispatch")
	default:
	}
	toolContext, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-lease.Done():
			cause := lease.Err()
			if cause == nil {
				cause = errors.New("managed sandbox activity ended during executor dispatch")
			}
			cancel(cause)
		case <-toolContext.Done():
		}
	}()
	return toolContext, func() { cancel(context.Canceled) }, nil
}

func (session *executorMCPSession) acquireManagedSandbox(
	ctx context.Context,
	acquirer ManagedSandboxSessionAcquirer,
) (ManagedSandboxSessionLease, error) {
	if session == nil || acquirer == nil || ctx == nil {
		return nil, errors.New("managed sandbox MCP session, acquirer, and context are required")
	}
	for {
		session.managedMu.Lock()
		lease := session.managedLease
		if lease != nil {
			select {
			case <-lease.Done():
				session.managedLease = nil
				session.managedMu.Unlock()
				releaseManagedSandboxLeaseAsync(lease, nil)
				continue
			default:
				session.managedMu.Unlock()
				return lease, nil
			}
		}
		if err := ctx.Err(); err != nil {
			session.managedMu.Unlock()
			return nil, err
		}
		// Serialize the first actual tool call. This keeps concurrent MCP calls
		// from creating multiple provider sessions for the same attempt while
		// leaving model-only turns entirely untouched.
		lease, err := acquirer.Acquire(ctx, session.principal)
		if err == nil {
			session.managedLease = lease
		}
		session.managedMu.Unlock()
		return lease, err
	}
}

func (session *executorMCPSession) detachManagedSandboxLease() ManagedSandboxSessionLease {
	if session == nil {
		return nil
	}
	session.managedMu.Lock()
	defer session.managedMu.Unlock()
	lease := session.managedLease
	session.managedLease = nil
	return lease
}

func releaseManagedSandboxLease(lease ManagedSandboxSessionLease, logger *slog.Logger) {
	if lease == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = releaseManagedSandboxLeaseWithContext(ctx, lease, logger)
}

func releaseManagedSandboxLeaseWithContext(
	ctx context.Context,
	lease ManagedSandboxSessionLease,
	logger *slog.Logger,
) error {
	if lease == nil {
		return nil
	}
	if err := lease.Release(ctx); err != nil {
		if logger != nil {
			logger.Error("release lazy managed sandbox activity", "error", err)
		}
		return err
	}
	return nil
}

func releaseManagedSandboxLeases(
	ctx context.Context,
	leases []ManagedSandboxSessionLease,
	logger *slog.Logger,
) error {
	if len(leases) == 0 {
		return nil
	}
	errorsByLease := make(chan error, len(leases))
	var releases sync.WaitGroup
	releases.Add(len(leases))
	for _, lease := range leases {
		go func(lease ManagedSandboxSessionLease) {
			defer releases.Done()
			releaseCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := releaseManagedSandboxLeaseWithContext(releaseCtx, lease, logger); err != nil {
				errorsByLease <- err
			}
		}(lease)
	}
	releases.Wait()
	close(errorsByLease)
	var releaseErrors []error
	for err := range errorsByLease {
		releaseErrors = append(releaseErrors, err)
	}
	return errors.Join(releaseErrors...)
}

func releaseManagedSandboxLeaseAsync(lease ManagedSandboxSessionLease, logger *slog.Logger) {
	if lease != nil {
		go releaseManagedSandboxLease(lease, logger)
	}
}

type listEnvironmentsInput struct {
	ExecutorID string `json:"executor_id,omitempty"`
}

type executorMCPCallContext struct {
	RunID                string
	ThreadID             string
	TurnID               string
	CallID               string
	RunAttemptGeneration int64
	ToolCatalogDigest    string
}

func parseExecutorMCPCallContext(meta mcp.Meta, principal ExecutorMCPPrincipal) (executorMCPCallContext, error) {
	allowed := map[string]struct{}{
		executorMCPMetaRunID: {}, executorMCPMetaThreadID: {}, executorMCPMetaTurnID: {}, executorMCPMetaCallID: {},
		executorMCPMetaRunAttemptGeneration: {}, executorMCPMetaToolCatalogDigest: {}, executorMCPMetaProgressToken: {},
	}
	if len(meta) != len(allowed) {
		return executorMCPCallContext{}, errors.New("executor tool requires the exact trusted MCP call metadata")
	}
	for key := range meta {
		if _, ok := allowed[key]; !ok {
			return executorMCPCallContext{}, fmt.Errorf("executor tool MCP metadata contains unsupported key %q", key)
		}
	}
	getString := func(key string) (string, error) {
		value, ok := meta[key].(string)
		if !ok || value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return "", fmt.Errorf("executor tool MCP metadata %s is missing or invalid", key)
		}
		return value, nil
	}
	var result executorMCPCallContext
	var err error
	if result.RunID, err = getString(executorMCPMetaRunID); err != nil {
		return executorMCPCallContext{}, err
	}
	if result.ThreadID, err = getString(executorMCPMetaThreadID); err != nil {
		return executorMCPCallContext{}, err
	}
	if result.TurnID, err = getString(executorMCPMetaTurnID); err != nil {
		return executorMCPCallContext{}, err
	}
	if result.CallID, err = getString(executorMCPMetaCallID); err != nil {
		return executorMCPCallContext{}, err
	}
	if result.ToolCatalogDigest, err = getString(executorMCPMetaToolCatalogDigest); err != nil {
		return executorMCPCallContext{}, err
	}
	progressToken, err := getString(executorMCPMetaProgressToken)
	if err != nil {
		return executorMCPCallContext{}, err
	}
	result.RunAttemptGeneration, err = executorMCPMetadataInt64(meta[executorMCPMetaRunAttemptGeneration])
	if err != nil || result.RunAttemptGeneration < 1 {
		return executorMCPCallContext{}, errors.New("executor tool MCP run attempt generation is invalid")
	}
	if result.RunID != principal.Run.RunID || result.RunAttemptGeneration != principal.Run.RunAttemptGeneration ||
		result.ToolCatalogDigest != principal.ToolCatalogDigest || progressToken != result.CallID {
		return executorMCPCallContext{}, errors.New("executor tool MCP call metadata is outside the authenticated run capability")
	}
	return result, nil
}

func executorMCPMetadataInt64(value any) (int64, error) {
	switch value := value.(type) {
	case int64:
		return value, nil
	case int:
		return int64(value), nil
	case float64:
		converted := int64(value)
		if float64(converted) != value {
			return 0, errors.New("metadata number is not an integer")
		}
		return converted, nil
	case json.Number:
		return value.Int64()
	default:
		return 0, errors.New("metadata value is not an integer")
	}
}

func requireExecutorMCPProtocol(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		if method == "initialize" {
			params, ok := request.GetParams().(*mcp.InitializeParams)
			if !ok || params == nil || params.ProtocolVersion != ExecutorMCPProtocolVersion {
				requested := ""
				if ok && params != nil {
					requested = params.ProtocolVersion
				}
				data, _ := json.Marshal(mcp.UnsupportedProtocolVersionData{
					Supported: []string{ExecutorMCPProtocolVersion},
					Requested: requested,
				})
				return nil, &jsonrpc.Error{
					Code:    mcp.CodeUnsupportedProtocolVersion,
					Message: fmt.Sprintf("executor MCP requires protocol %s", ExecutorMCPProtocolVersion),
					Data:    data,
				}
			}
		}
		result, err := next(ctx, method, request)
		if err == nil && method == "tools/list" {
			if listed, ok := result.(*mcp.ListToolsResult); ok && listed != nil {
				listed.CacheScope = "private"
				listed.TTLMs = 0
			}
		}
		return result, err
	}
}

func (handler *ExecutorMCPHandler) finishPreparedSession(session *executorMCPSession) {
	handler.mu.Lock()
	current := handler.sessions[session.id]
	if current != session {
		handler.mu.Unlock()
		return
	}
	session.pending = false
	var lease ManagedSandboxSessionLease
	if !mcpServerHasSessions(session.server) {
		delete(handler.sessions, session.id)
		lease = session.detachManagedSandboxLease()
	}
	handler.mu.Unlock()
	releaseManagedSandboxLeaseAsync(lease, handler.config.Logger)
}

func (handler *ExecutorMCPHandler) authorizeSession(sessionID string, principal ExecutorMCPPrincipal) (*executorMCPSession, error) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.shuttingDown {
		return nil, errExecutorMCPShuttingDown
	}
	handler.sweepClosedSessionsLocked()
	session := handler.sessions[sessionID]
	if session == nil || !equalExecutorMCPPrincipals(session.principal, principal) {
		return nil, errors.New("MCP session principal mismatch")
	}
	return session, nil
}

// equalExecutorMCPPrincipals compares the immutable authority by value. The
// production authenticator reconstructs ManagedSandbox on every request, so
// comparing ExecutorMCPPrincipal directly would compare allocation addresses
// and reject an otherwise identical principal after initialize.
func equalExecutorMCPPrincipals(left, right ExecutorMCPPrincipal) bool {
	if (left.ManagedSandbox == nil) != (right.ManagedSandbox == nil) {
		return false
	}
	if left.ManagedSandbox != nil && *left.ManagedSandbox != *right.ManagedSandbox {
		return false
	}
	left.ManagedSandbox = nil
	right.ManagedSandbox = nil
	return left == right
}

func (handler *ExecutorMCPHandler) finishExistingSession(sessionID string, session *executorMCPSession) {
	handler.mu.Lock()
	// Only forget a DELETE after the SDK actually removed its session. A
	// malformed DELETE must not detach our authorization record from a still
	// live SDK session and thereby bypass MaxSessions accounting.
	var lease ManagedSandboxSessionLease
	if handler.sessions[sessionID] == session && !mcpServerHasSessions(session.server) {
		delete(handler.sessions, sessionID)
		lease = session.detachManagedSandboxLease()
	}
	handler.mu.Unlock()
	releaseManagedSandboxLeaseAsync(lease, handler.config.Logger)
}

func (handler *ExecutorMCPHandler) sweepClosedSessionsLocked() {
	for sessionID, session := range handler.sessions {
		if !session.pending && !mcpServerHasSessions(session.server) {
			delete(handler.sessions, sessionID)
			lease := session.detachManagedSandboxLease()
			releaseManagedSandboxLeaseAsync(lease, handler.config.Logger)
		}
	}
}

func mcpServerHasSessions(server *mcp.Server) bool {
	for range server.Sessions() {
		return true
	}
	return false
}

// Shutdown rejects new requests and gracefully closes every official-SDK
// session. The caller-supplied context bounds waiting for in-flight tool calls.
func (handler *ExecutorMCPHandler) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("executor MCP shutdown context is required")
	}
	handler.mu.Lock()
	handler.shuttingDown = true
	servers := make([]*mcp.Server, 0, len(handler.sessions))
	for _, session := range handler.sessions {
		servers = append(servers, session.server)
	}
	handler.mu.Unlock()

	closed := make(chan error, 1)
	go func() {
		var closeErrors []error
		for _, server := range servers {
			for session := range server.Sessions() {
				closeErrors = append(closeErrors, session.Close())
			}
		}
		closed <- errors.Join(closeErrors...)
	}()
	select {
	case err := <-closed:
		handler.mu.Lock()
		leases := make([]ManagedSandboxSessionLease, 0, len(handler.sessions))
		for _, session := range handler.sessions {
			if lease := session.detachManagedSandboxLease(); lease != nil {
				leases = append(leases, lease)
			}
		}
		clear(handler.sessions)
		handler.mu.Unlock()
		releaseErr := releaseManagedSandboxLeases(ctx, leases, handler.config.Logger)
		return errors.Join(err, releaseErr)
	case <-ctx.Done():
		return fmt.Errorf("shutdown executor MCP sessions: %w", ctx.Err())
	}
}

func requestSessionID(request *http.Request) (string, error) {
	values := request.Header.Values(mcpSessionIDHeader)
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 {
		return "", errors.New("multiple MCP session headers")
	}
	if err := validateExecutorMCPSessionID(values[0]); err != nil {
		return "", err
	}
	return values[0], nil
}

func validateExecutorMCPSessionID(value string) error {
	if len(value) < 1 || len(value) > 128 || !utf8.ValidString(value) {
		return errors.New("MCP session ID is empty, invalid, or too long")
	}
	for _, character := range []byte(value) {
		if character <= ' ' || character >= 0x7f {
			return errors.New("MCP session ID contains an invalid byte")
		}
	}
	return nil
}

func validateExecutorMCPPrincipal(principal ExecutorMCPPrincipal) error {
	if err := validateRegistryIdentity("workspace ID", principal.WorkspaceID); err != nil {
		return err
	}
	if err := validateRegistryIdentity("session ID", principal.SessionID); err != nil {
		return err
	}
	if err := validateRegistryIdentity("actor ID", principal.ActorID); err != nil {
		return err
	}
	if err := validateRegistryIdentity("run ID", principal.Run.RunID); err != nil {
		return err
	}
	if err := validateRegistryIdentity("run attempt ID", principal.Run.RunAttemptID); err != nil {
		return err
	}
	if principal.Run.RunAttemptGeneration < 1 || principal.Run.ExpectedRunVersion < 1 || principal.Run.ExpectedRunAttemptVersion < 1 {
		return errors.New("MCP run generation and expected versions must be positive")
	}
	if principal.MaxApprovalTTL <= 0 || principal.MaxApprovalTTL > maximumExecutorMCPApprovalTTL {
		return errors.New("MCP maximum approval TTL must be positive and at most 24 hours")
	}
	if principal.RunDeadline.IsZero() || principal.CapabilityExpiresAt.IsZero() ||
		principal.RunDeadline.After(principal.CapabilityExpiresAt) {
		return errors.New("MCP run deadline and capability expiry are missing or inconsistent")
	}
	if principal.Run.HolderID == "" || len(principal.Run.HolderID) > 256 || !utf8.ValidString(principal.Run.HolderID) || strings.ContainsRune(principal.Run.HolderID, 0) {
		return errors.New("MCP run holder ID must contain between 1 and 256 valid UTF-8 bytes without NUL")
	}
	if principal.ExecutorID != "" {
		if err := validateRegistryIdentity("executor ID", principal.ExecutorID); err != nil {
			return err
		}
	}
	if len(principal.ToolCatalogDigest) != 64 || strings.ToLower(principal.ToolCatalogDigest) != principal.ToolCatalogDigest {
		return errors.New("MCP tool catalog digest must be 64 lowercase hexadecimal characters")
	}
	for _, character := range []byte(principal.ToolCatalogDigest) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return errors.New("MCP tool catalog digest must be 64 lowercase hexadecimal characters")
		}
	}
	if principal.CapabilityID == "" || len(principal.CapabilityID) > maximumExecutorMCPCapabilityIDSize || !utf8.ValidString(principal.CapabilityID) {
		return errors.New("MCP capability ID is empty, invalid, or too long")
	}
	if strings.TrimSpace(principal.CapabilityID) != principal.CapabilityID || slices.ContainsFunc([]byte(principal.CapabilityID), func(value byte) bool {
		return value < 0x21 || value > 0x7e
	}) {
		return errors.New("MCP capability ID contains an invalid byte")
	}
	return nil
}
