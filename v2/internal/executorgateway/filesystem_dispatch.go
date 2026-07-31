package executorgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"nhooyr.io/websocket"
)

var ErrFilesystemReadUnavailable = errors.New("executor environment is not connected with the bounded filesystem-read profile")

type FilesystemDispatchRequest struct {
	ExecutorID                   string
	ExpectedConnectionGeneration int64
	Context                      agentxconn.RoutingContext
	RPC                          json.RawMessage
}

type FilesystemDispatcher interface {
	DispatchFilesystem(context.Context, FilesystemDispatchRequest) (*FilesystemExchange, error)
}

// DispatchFilesystem journals and writes one bounded filesystem request under
// an explicitly fenced connection generation. A non-nil exchange paired with
// ErrDispatchAmbiguous means the frame entered the session journal and must
// never be issued again, even though read is a non-mutating effect class.
func (s *Server) DispatchFilesystem(ctx context.Context, request FilesystemDispatchRequest) (*FilesystemExchange, error) {
	if ctx == nil {
		return nil, errors.New("filesystem dispatch context is required")
	}
	if err := request.Context.Validate(); err != nil {
		return nil, err
	}
	if request.Context.EnvID == "" || request.ExecutorID == "" {
		return nil, errors.New("filesystem dispatch executor and environment are required")
	}
	if request.ExpectedConnectionGeneration < 1 {
		return nil, errors.New("expected connection generation must be positive")
	}
	runtime, connection, holder, err := s.readyFilesystemRuntime(request.ExecutorID, request.Context.EnvID, request.ExpectedConnectionGeneration)
	if err != nil {
		return nil, err
	}
	exchange, err := s.filesystemCalls.register(holder, request)
	if err != nil {
		return nil, err
	}
	frame, err := runtime.session.Send(agentxconn.Payload{
		Type:    agentxconn.MessageTypeRPC,
		Context: &request.Context,
		RPC:     append(json.RawMessage(nil), request.RPC...),
	})
	if err != nil {
		s.filesystemCalls.cancel(exchange, err)
		return nil, err
	}
	if err := s.writeValue(ctx, runtime, connection, frame); err != nil {
		return exchange, fmt.Errorf("%w: %v", ErrDispatchAmbiguous, err)
	}
	return exchange, nil
}

func (s *Server) readyFilesystemRuntime(executorID, environmentID string, expectedGeneration int64) (*sessionRuntime, *websocket.Conn, ConnectionHolder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown {
		return nil, nil, ConnectionHolder{}, errServerShuttingDown
	}
	runtime := s.byExecutor[executorID]
	if runtime == nil {
		return nil, nil, ConnectionHolder{}, ErrFilesystemReadUnavailable
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.phase != connectionReady || runtime.connection == nil || runtime.holder.Status != "online" {
		return nil, nil, ConnectionHolder{}, ErrFilesystemReadUnavailable
	}
	if runtime.holder.Generation != expectedGeneration {
		return nil, nil, ConnectionHolder{}, ErrConnectionFenced
	}
	for _, environment := range runtime.environments {
		if environment.ID != environmentID {
			continue
		}
		if !execprofile.SupportsFilesystemRead(environment.OuterProfileVersion) {
			return nil, nil, ConnectionHolder{}, ErrFilesystemReadUnavailable
		}
		return runtime, runtime.connection, runtime.holder, nil
	}
	return nil, nil, ConnectionHolder{}, ErrFilesystemReadUnavailable
}

// FilesystemExchange retains exactly one response or one terminal failure.
// It is scoped to the complete holder and routing context registered before
// the outbound frame enters the session journal.
type FilesystemExchange struct {
	holder ConnectionHolder

	response chan json.RawMessage
	failure  chan error
	done     chan struct{}

	mu       sync.Mutex
	terminal error
}

func (exchange *FilesystemExchange) Holder() ConnectionHolder { return exchange.holder }

func (exchange *FilesystemExchange) AwaitResponse(ctx context.Context) (json.RawMessage, error) {
	if ctx == nil {
		return nil, errors.New("filesystem response context is required")
	}
	select {
	case response := <-exchange.response:
		return append(json.RawMessage(nil), response...), nil
	default:
	}
	select {
	case response := <-exchange.response:
		return append(json.RawMessage(nil), response...), nil
	case err := <-exchange.failure:
		return nil, err
	case <-exchange.done:
		select {
		case response := <-exchange.response:
			return append(json.RawMessage(nil), response...), nil
		default:
		}
		select {
		case err := <-exchange.failure:
			return nil, err
		default:
		}
		exchange.mu.Lock()
		err := exchange.terminal
		exchange.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (exchange *FilesystemExchange) Done() <-chan struct{} { return exchange.done }

type filesystemCall struct {
	exchange     *FilesystemExchange
	holder       ConnectionHolder
	routing      agentxconn.RoutingContext
	requestID    string
	responseKey  string
	routingKey   string
	abandoned    bool
	exchangeDone bool
	closed       bool
}

type filesystemCallTable struct {
	mu sync.Mutex

	maxCalls   int
	byResponse map[string]*filesystemCall
	byRouting  map[string]*filesystemCall
}

func newFilesystemCallTable(maxCalls int) (*filesystemCallTable, error) {
	if maxCalls < 1 || maxCalls > 4096 {
		return nil, errors.New("filesystem dispatch retention bound is invalid")
	}
	return &filesystemCallTable{
		maxCalls: maxCalls, byResponse: make(map[string]*filesystemCall), byRouting: make(map[string]*filesystemCall),
	}, nil
}

func (table *filesystemCallTable) register(holder ConnectionHolder, request FilesystemDispatchRequest) (*FilesystemExchange, error) {
	message, err := codexwire.Parse(request.RPC)
	if err != nil {
		return nil, fmt.Errorf("parse filesystem dispatch RPC: %w", err)
	}
	if message.Kind != codexwire.KindRequest || message.Method != execprofile.MethodFilesystemReadFileBlock {
		return nil, errors.New("filesystem dispatch accepts only agentx/fs/readFileBlock requests")
	}
	requestID, err := canonicalRPCID(message.ID)
	if err != nil {
		return nil, err
	}
	responseKey := holder.SessionID + "\x00" + requestID
	routingKey := filesystemRoutingKey(holder.SessionID, request.Context)

	table.mu.Lock()
	defer table.mu.Unlock()
	if len(table.byResponse) >= table.maxCalls {
		return nil, &agentxconn.ProtocolError{Code: agentxconn.ErrorJournalFull, Message: "pending filesystem call table is full", Terminal: false}
	}
	if table.byResponse[responseKey] != nil {
		return nil, errors.New("filesystem request id is already pending in this agentx session")
	}
	if table.byRouting[routingKey] != nil {
		return nil, errors.New("filesystem routing context is already pending in this agentx session")
	}
	exchange := &FilesystemExchange{
		holder: holder, response: make(chan json.RawMessage, 1), failure: make(chan error, 1), done: make(chan struct{}),
	}
	call := &filesystemCall{
		exchange: exchange, holder: holder, routing: request.Context, requestID: requestID,
		responseKey: responseKey, routingKey: routingKey,
	}
	table.byResponse[responseKey] = call
	table.byRouting[routingKey] = call
	return exchange, nil
}

func (table *filesystemCallTable) cancel(exchange *FilesystemExchange, cause error) {
	table.mu.Lock()
	defer table.mu.Unlock()
	for _, call := range table.byResponse {
		if call.exchange == exchange {
			table.failLocked(call, cause)
			return
		}
	}
}

func (table *filesystemCallTable) handle(holder ConnectionHolder, frame agentxconn.Frame) (bool, error) {
	message, err := codexwire.Parse(frame.RPC)
	if err != nil {
		return false, err
	}
	if message.Kind != codexwire.KindResponse && message.Kind != codexwire.KindError {
		return false, nil
	}
	requestID, err := canonicalRPCID(message.ID)
	if err != nil {
		return false, err
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	call := table.byResponse[holder.SessionID+"\x00"+requestID]
	if call == nil {
		return false, nil
	}
	if !sameHolder(call.holder, holder) {
		return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorStaleGeneration, Message: "filesystem response came from a different connection holder", Terminal: true}
	}
	if frame.Context == nil || *frame.Context != call.routing {
		return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorMutationConflict, Message: "filesystem response routing context differs from the dispatched operation", Terminal: true}
	}
	if call.abandoned {
		// The MCP operation was conservatively closed as unknown when the
		// transport disconnected. Retain the correlation through same-session
		// resume solely to consume the journaled late response; it must not
		// change the already-terminal core outcome.
		table.completeLocked(call)
		return true, nil
	}
	call.exchange.response <- append(json.RawMessage(nil), frame.RPC...)
	table.completeLocked(call)
	return true, nil
}

func (table *filesystemCallTable) abandonHolder(holder ConnectionHolder, cause error) {
	table.mu.Lock()
	defer table.mu.Unlock()
	for _, call := range table.byResponse {
		if sameHolder(call.holder, holder) {
			table.abandonLocked(call, cause)
		}
	}
}

func (table *filesystemCallTable) failHolder(holder ConnectionHolder, cause error) {
	table.mu.Lock()
	defer table.mu.Unlock()
	for _, call := range table.byResponse {
		if sameHolder(call.holder, holder) {
			table.failLocked(call, cause)
		}
	}
}

func (table *filesystemCallTable) completeLocked(call *filesystemCall) {
	if call.closed {
		return
	}
	call.closed = true
	delete(table.byResponse, call.responseKey)
	delete(table.byRouting, call.routingKey)
	if !call.exchangeDone {
		call.exchangeDone = true
		close(call.exchange.done)
	}
}

func (table *filesystemCallTable) failLocked(call *filesystemCall, cause error) {
	if call.closed {
		return
	}
	table.failExchangeLocked(call, cause)
	table.completeLocked(call)
}

func (table *filesystemCallTable) abandonLocked(call *filesystemCall, cause error) {
	if call.closed || call.abandoned {
		return
	}
	call.abandoned = true
	table.failExchangeLocked(call, cause)
}

func (table *filesystemCallTable) failExchangeLocked(call *filesystemCall, cause error) {
	if call.exchangeDone {
		return
	}
	if cause == nil {
		cause = errors.New("agentx filesystem exchange failed")
	}
	call.exchange.mu.Lock()
	call.exchange.terminal = cause
	call.exchange.mu.Unlock()
	call.exchange.failure <- cause
	call.exchangeDone = true
	close(call.exchange.done)
}

func filesystemRoutingKey(sessionID string, routing agentxconn.RoutingContext) string {
	return strings.Join([]string{
		sessionID,
		routing.WorkspaceID,
		routing.RunID,
		routing.RunAttemptID,
		strconv.FormatInt(routing.RunAttemptGeneration, 10),
		routing.ExecutionID,
		routing.OperationID,
		routing.EnvID,
		routing.MutationKey,
	}, "\x00")
}

var _ FilesystemDispatcher = (*Server)(nil)
