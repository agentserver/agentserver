package executorgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"nhooyr.io/websocket"
)

var ErrDispatchAmbiguous = errors.New("agentx process dispatch is ambiguous")

var ErrExecutorUnavailable = errors.New("executor environment is not connected and ready")

type ProcessDispatchRequest struct {
	ExecutorID                   string
	ExpectedConnectionGeneration int64
	Context                      agentxconn.RoutingContext
	Directives                   *agentxconn.DispatchDirectives
	RPC                          json.RawMessage
}

// DispatchProcess journals and writes one deterministic process/start or
// process/terminate frame under an explicitly fenced connection generation.
// A non-nil exchange paired with ErrDispatchAmbiguous means the session
// journal admitted the frame, so the caller must reconcile or close the core
// operation as unknown; it must never issue the mutation again.
func (s *Server) DispatchProcess(ctx context.Context, request ProcessDispatchRequest) (*ProcessExchange, error) {
	if ctx == nil {
		return nil, errors.New("process dispatch context is required")
	}
	if err := request.Context.Validate(); err != nil {
		return nil, err
	}
	if request.Context.EnvID == "" || request.ExecutorID == "" {
		return nil, errors.New("process dispatch executor and environment are required")
	}
	if request.ExpectedConnectionGeneration < 1 {
		return nil, errors.New("expected connection generation must be positive")
	}
	runtime, connection, holder, err := s.readyProcessRuntime(request.ExecutorID, request.Context.EnvID, request.ExpectedConnectionGeneration)
	if err != nil {
		return nil, err
	}
	exchange, err := s.processCalls.register(holder, request)
	if err != nil {
		return nil, err
	}
	frame, err := runtime.session.Send(agentxconn.Payload{
		Type:       agentxconn.MessageTypeRPC,
		Context:    &request.Context,
		Directives: request.Directives,
		RPC:        append(json.RawMessage(nil), request.RPC...),
	})
	if err != nil {
		s.processCalls.cancel(exchange, err)
		return nil, err
	}
	if err := s.writeValue(ctx, runtime, connection, frame); err != nil {
		return exchange, fmt.Errorf("%w: %v", ErrDispatchAmbiguous, err)
	}
	return exchange, nil
}

func (s *Server) readyProcessRuntime(executorID, environmentID string, expectedGeneration int64) (*sessionRuntime, *websocket.Conn, ConnectionHolder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown {
		return nil, nil, ConnectionHolder{}, errServerShuttingDown
	}
	runtime := s.byExecutor[executorID]
	if runtime == nil {
		return nil, nil, ConnectionHolder{}, ErrExecutorUnavailable
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.phase != connectionReady || runtime.connection == nil || runtime.holder.Status != "online" {
		return nil, nil, ConnectionHolder{}, ErrExecutorUnavailable
	}
	if runtime.holder.Generation != expectedGeneration {
		return nil, nil, ConnectionHolder{}, ErrConnectionFenced
	}
	found := false
	for _, environment := range runtime.environments {
		if environment.ID == environmentID {
			found = true
			break
		}
	}
	if !found {
		return nil, nil, ConnectionHolder{}, ErrExecutorUnavailable
	}
	return runtime, runtime.connection, runtime.holder, nil
}

// ProcessExchange retains one matching start/terminate response. A start
// exchange additionally retains exact ordered process notifications until
// process/closed and, when configured, one correlated timeout-due signal.
// Every exchange is scoped to one connection generation and routing context.
type ProcessExchange struct {
	holder ConnectionHolder

	response   chan json.RawMessage
	events     chan json.RawMessage
	timeoutDue chan ProcessTimeoutDue
	failure    chan error
	done       chan struct{}

	mu       sync.Mutex
	terminal error
}

func (exchange *ProcessExchange) Holder() ConnectionHolder { return exchange.holder }

func (exchange *ProcessExchange) AwaitResponse(ctx context.Context) (json.RawMessage, error) {
	if ctx == nil {
		return nil, errors.New("process response context is required")
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
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (exchange *ProcessExchange) NextEvent(ctx context.Context) (json.RawMessage, error) {
	if ctx == nil {
		return nil, errors.New("process event context is required")
	}
	select {
	case event, ok := <-exchange.events:
		if ok {
			return append(json.RawMessage(nil), event...), nil
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

type ProcessTimeoutDue struct {
	ProcessID string
	Context   agentxconn.RoutingContext
}

func (exchange *ProcessExchange) AwaitTimeoutDue(ctx context.Context) (ProcessTimeoutDue, error) {
	if ctx == nil {
		return ProcessTimeoutDue{}, errors.New("process timeout context is required")
	}
	if exchange.timeoutDue == nil {
		return ProcessTimeoutDue{}, errors.New("process exchange has no timeout directive")
	}
	select {
	case due, ok := <-exchange.timeoutDue:
		if ok {
			return due, nil
		}
		exchange.mu.Lock()
		err := exchange.terminal
		exchange.mu.Unlock()
		if err != nil {
			return ProcessTimeoutDue{}, err
		}
		return ProcessTimeoutDue{}, io.EOF
	case <-ctx.Done():
		return ProcessTimeoutDue{}, ctx.Err()
	}
}

func (exchange *ProcessExchange) Done() <-chan struct{} { return exchange.done }

type processCall struct {
	exchange       *ProcessExchange
	holder         ConnectionHolder
	routing        agentxconn.RoutingContext
	requestID      string
	processID      string
	responseKey    string
	processKey     string
	timeoutRouting *agentxconn.RoutingContext

	responseSeen        bool
	timeoutDueSeen      bool
	terminateRegistered bool
	exited              bool
	lastSequence        uint64
	eventCount          int
	eventBytes          int
	closed              bool
}

type processCommandCall struct {
	exchange    *ProcessExchange
	owner       *processCall
	holder      ConnectionHolder
	routing     agentxconn.RoutingContext
	requestID   string
	responseKey string
	closed      bool
}

type processCallTable struct {
	mu sync.Mutex

	maxCalls          int
	maxEvents         int
	maxEventBytes     int
	byResponse        map[string]*processCall
	byCommandResponse map[string]*processCommandCall
	byProcess         map[string]*processCall
}

func newProcessCallTable(maxCalls, maxEvents, maxEventBytes int) (*processCallTable, error) {
	if maxCalls < 1 || maxCalls > 4096 || maxEvents < 1 || maxEvents > 50_000 || maxEventBytes < 1 {
		return nil, errors.New("process dispatch retention bounds are invalid")
	}
	return &processCallTable{
		maxCalls:          maxCalls,
		maxEvents:         maxEvents,
		maxEventBytes:     maxEventBytes,
		byResponse:        make(map[string]*processCall),
		byCommandResponse: make(map[string]*processCommandCall),
		byProcess:         make(map[string]*processCall),
	}, nil
}

func (table *processCallTable) register(holder ConnectionHolder, request ProcessDispatchRequest) (*ProcessExchange, error) {
	message, err := codexwire.Parse(request.RPC)
	if err != nil {
		return nil, fmt.Errorf("parse process dispatch RPC: %w", err)
	}
	if message.Kind != codexwire.KindRequest || (message.Method != execprofile.MethodProcessStart && message.Method != execprofile.MethodProcessTerminate) {
		return nil, errors.New("process dispatch accepts only process/start or process/terminate requests")
	}
	requestID, err := canonicalRPCID(message.ID)
	if err != nil {
		return nil, err
	}
	var params struct {
		ProcessID string `json:"processId"`
	}
	if err := message.DecodeParams(&params); err != nil || params.ProcessID == "" {
		return nil, fmt.Errorf("%s requires processId", message.Method)
	}
	responseKey := holder.SessionID + "\x00" + requestID
	processKey := holder.SessionID + "\x00" + params.ProcessID

	table.mu.Lock()
	defer table.mu.Unlock()
	if table.byResponse[responseKey] != nil || table.byCommandResponse[responseKey] != nil {
		return nil, errors.New("process request id is already pending in this agentx session")
	}
	if message.Method == execprofile.MethodProcessTerminate {
		return table.registerTerminateLocked(holder, request, requestID, responseKey, processKey)
	}
	if len(table.byProcess) >= table.maxCalls {
		return nil, &agentxconn.ProtocolError{Code: agentxconn.ErrorJournalFull, Message: "pending process table is full", Terminal: false}
	}
	if table.byProcess[processKey] != nil {
		return nil, errors.New("process id is already pending in this agentx session")
	}
	var timeoutRouting *agentxconn.RoutingContext
	if request.Directives != nil && request.Directives.ProcessTimeout != nil {
		routing := request.Context
		routing.OperationID = request.Directives.ProcessTimeout.OperationID
		routing.MutationKey = request.Directives.ProcessTimeout.MutationKey
		timeoutRouting = &routing
	}
	var timeoutDue chan ProcessTimeoutDue
	if timeoutRouting != nil {
		timeoutDue = make(chan ProcessTimeoutDue, 1)
	}
	exchange := &ProcessExchange{
		holder:     holder,
		response:   make(chan json.RawMessage, 1),
		events:     make(chan json.RawMessage, table.maxEvents),
		timeoutDue: timeoutDue,
		failure:    make(chan error, 1),
		done:       make(chan struct{}),
	}
	call := &processCall{
		exchange:       exchange,
		holder:         holder,
		routing:        request.Context,
		requestID:      requestID,
		processID:      params.ProcessID,
		responseKey:    responseKey,
		processKey:     processKey,
		timeoutRouting: timeoutRouting,
	}
	table.byResponse[responseKey] = call
	table.byProcess[processKey] = call
	return exchange, nil
}

func (table *processCallTable) registerTerminateLocked(holder ConnectionHolder, request ProcessDispatchRequest, requestID, responseKey, processKey string) (*ProcessExchange, error) {
	owner := table.byProcess[processKey]
	if owner == nil || owner.closed {
		return nil, errors.New("process/terminate requires a live process/start exchange")
	}
	if owner.terminateRegistered {
		return nil, errors.New("process/terminate is already registered for this process")
	}
	if !sameHolder(owner.holder, holder) {
		return nil, ErrConnectionFenced
	}
	if !sameProcessRoutingBase(owner.routing, request.Context) {
		return nil, errors.New("process/terminate routing is outside the process owner execution")
	}
	if len(table.byCommandResponse) >= table.maxCalls {
		return nil, &agentxconn.ProtocolError{Code: agentxconn.ErrorJournalFull, Message: "pending process command table is full", Terminal: false}
	}
	exchange := &ProcessExchange{
		holder:   holder,
		response: make(chan json.RawMessage, 1),
		events:   make(chan json.RawMessage, 1),
		failure:  make(chan error, 1),
		done:     make(chan struct{}),
	}
	call := &processCommandCall{
		exchange:    exchange,
		owner:       owner,
		holder:      holder,
		routing:     request.Context,
		requestID:   requestID,
		responseKey: responseKey,
	}
	owner.terminateRegistered = true
	table.byCommandResponse[responseKey] = call
	return exchange, nil
}

func (table *processCallTable) cancel(exchange *ProcessExchange, cause error) {
	table.mu.Lock()
	defer table.mu.Unlock()
	for _, call := range table.byProcess {
		if call.exchange == exchange {
			table.failLocked(call, cause)
			return
		}
	}
	for _, call := range table.byCommandResponse {
		if call.exchange == exchange {
			table.failCommandLocked(call, cause)
			return
		}
	}
}

func (table *processCallTable) handle(holder ConnectionHolder, frame agentxconn.Frame) (bool, error) {
	message, err := codexwire.Parse(frame.RPC)
	if err != nil {
		return false, err
	}
	table.mu.Lock()
	defer table.mu.Unlock()
	switch message.Kind {
	case codexwire.KindResponse, codexwire.KindError:
		requestID, err := canonicalRPCID(message.ID)
		if err != nil {
			return false, err
		}
		call := table.byResponse[holder.SessionID+"\x00"+requestID]
		if call == nil {
			command := table.byCommandResponse[holder.SessionID+"\x00"+requestID]
			if command == nil {
				return false, nil
			}
			if err := matchProcessRouting(command.holder, command.routing, holder, frame.Context); err != nil {
				return true, err
			}
			command.exchange.response <- append(json.RawMessage(nil), frame.RPC...)
			table.completeCommandLocked(command)
			return true, nil
		}
		if err := matchProcessCall(call, holder, frame.Context); err != nil {
			return true, err
		}
		if call.responseSeen {
			return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorSequenceConflict, Message: "agentx sent a second process response", Terminal: true}
		}
		if message.Kind == codexwire.KindResponse {
			var result struct {
				ProcessID string `json:"processId"`
			}
			if err := decodeExactJSON(message.Result, &result); err != nil || result.ProcessID != call.processID {
				return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorMutationConflict, Message: "process/start response does not match the registered process", Terminal: true}
			}
		}
		call.responseSeen = true
		call.exchange.response <- append(json.RawMessage(nil), frame.RPC...)
		delete(table.byResponse, call.responseKey)
		if message.Kind == codexwire.KindError {
			table.completeLocked(call)
		}
		return true, nil
	case codexwire.KindNotification:
		if message.Method == agentxconn.NotificationAgentxTimeoutDue {
			var params struct {
				ProcessID string `json:"processId"`
			}
			if err := message.DecodeParams(&params); err != nil {
				return false, err
			}
			call := table.byProcess[holder.SessionID+"\x00"+params.ProcessID]
			if call == nil {
				return false, nil
			}
			if call.timeoutRouting == nil {
				return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorMutationConflict, Message: "process has no registered timeout directive", Terminal: true}
			}
			if err := matchProcessRouting(call.holder, *call.timeoutRouting, holder, frame.Context); err != nil {
				return true, err
			}
			if call.timeoutDueSeen {
				return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorSequenceConflict, Message: "agentx sent timeout-due more than once", Terminal: true}
			}
			call.timeoutDueSeen = true
			select {
			case call.exchange.timeoutDue <- ProcessTimeoutDue{ProcessID: params.ProcessID, Context: *frame.Context}:
			default:
				return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorBufferOverflow, Message: "process timeout signal retention bound exceeded", Terminal: true}
			}
			return true, nil
		}
		var params struct {
			ProcessID string `json:"processId"`
			Sequence  uint64 `json:"seq"`
		}
		if err := message.DecodeParams(&params); err != nil {
			return false, err
		}
		call := table.byProcess[holder.SessionID+"\x00"+params.ProcessID]
		if call == nil {
			return false, nil
		}
		if err := matchProcessCall(call, holder, frame.Context); err != nil {
			return true, err
		}
		if params.Sequence <= call.lastSequence {
			return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorSequenceConflict, Message: "process notification sequence regressed", Terminal: true}
		}
		if params.Sequence != call.lastSequence+1 {
			from := call.lastSequence + 1
			to := params.Sequence - 1
			return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorOutputGap, Message: "process notification sequence contains a gap", Terminal: true, LostFrom: &from, LostTo: &to}
		}
		if call.eventCount == table.maxEvents || len(frame.RPC) > table.maxEventBytes-call.eventBytes {
			lost := params.Sequence
			return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorBufferOverflow, Message: "process notification retention bound exceeded", Terminal: true, LostFrom: &lost, LostTo: &lost}
		}
		switch message.Method {
		case execprofile.NotificationProcessOutput:
		case execprofile.NotificationProcessExited:
			if call.exited {
				return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorSequenceConflict, Message: "process emitted more than one exited notification", Terminal: true}
			}
			call.exited = true
		case execprofile.NotificationProcessClosed:
			if !call.exited {
				return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorSequenceConflict, Message: "process closed before exited evidence", Terminal: true}
			}
		default:
			return false, nil
		}
		call.lastSequence = params.Sequence
		call.eventCount++
		call.eventBytes += len(frame.RPC)
		select {
		case call.exchange.events <- append(json.RawMessage(nil), frame.RPC...):
		default:
			lost := params.Sequence
			return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorBufferOverflow, Message: "process notification consumer did not keep up", Terminal: true, LostFrom: &lost, LostTo: &lost}
		}
		if message.Method == execprofile.NotificationProcessClosed {
			table.completeLocked(call)
		}
		return true, nil
	default:
		return false, nil
	}
}

func (table *processCallTable) failHolder(holder ConnectionHolder, cause error) {
	table.mu.Lock()
	defer table.mu.Unlock()
	for _, call := range table.byProcess {
		if sameHolder(call.holder, holder) {
			table.failLocked(call, cause)
		}
	}
	for _, call := range table.byCommandResponse {
		if sameHolder(call.holder, holder) {
			table.failCommandLocked(call, cause)
		}
	}
}

func (table *processCallTable) completeLocked(call *processCall) {
	if call.closed {
		return
	}
	call.closed = true
	delete(table.byResponse, call.responseKey)
	delete(table.byProcess, call.processKey)
	for _, command := range table.byCommandResponse {
		if command.owner == call {
			table.failCommandLocked(command, errors.New("process reached terminal before command response"))
		}
	}
	close(call.exchange.events)
	if call.exchange.timeoutDue != nil {
		close(call.exchange.timeoutDue)
	}
	close(call.exchange.done)
}

func (table *processCallTable) failLocked(call *processCall, cause error) {
	if call.closed {
		return
	}
	if cause == nil {
		cause = errors.New("agentx process exchange failed")
	}
	call.exchange.mu.Lock()
	call.exchange.terminal = cause
	call.exchange.mu.Unlock()
	call.exchange.failure <- cause
	table.completeLocked(call)
}

func (table *processCallTable) completeCommandLocked(call *processCommandCall) {
	if call.closed {
		return
	}
	call.closed = true
	delete(table.byCommandResponse, call.responseKey)
	close(call.exchange.events)
	close(call.exchange.done)
}

func (table *processCallTable) failCommandLocked(call *processCommandCall, cause error) {
	if call.closed {
		return
	}
	if cause == nil {
		cause = errors.New("agentx process command failed")
	}
	call.exchange.mu.Lock()
	call.exchange.terminal = cause
	call.exchange.mu.Unlock()
	call.exchange.failure <- cause
	table.completeCommandLocked(call)
}

func matchProcessCall(call *processCall, holder ConnectionHolder, routing *agentxconn.RoutingContext) error {
	return matchProcessRouting(call.holder, call.routing, holder, routing)
}

func matchProcessRouting(expectedHolder ConnectionHolder, expectedRouting agentxconn.RoutingContext, holder ConnectionHolder, routing *agentxconn.RoutingContext) error {
	if !sameHolder(expectedHolder, holder) {
		return &agentxconn.ProtocolError{Code: agentxconn.ErrorStaleGeneration, Message: "process evidence came from a different connection holder", Terminal: true}
	}
	if routing == nil || *routing != expectedRouting {
		return &agentxconn.ProtocolError{Code: agentxconn.ErrorMutationConflict, Message: "process evidence routing context differs from the dispatched operation", Terminal: true}
	}
	return nil
}

func sameProcessRoutingBase(left, right agentxconn.RoutingContext) bool {
	return left.WorkspaceID == right.WorkspaceID && left.RunID == right.RunID &&
		left.RunAttemptID == right.RunAttemptID && left.RunAttemptGeneration == right.RunAttemptGeneration &&
		left.ExecutionID == right.ExecutionID && left.EnvID == right.EnvID
}

func canonicalRPCID(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decode process request id: %w", err)
	}
	switch value := value.(type) {
	case string:
		return "s:" + value, nil
	case json.Number:
		integer, err := strconv.ParseInt(value.String(), 10, 64)
		if err != nil {
			return "", errors.New("process request id number must be a signed 64-bit integer")
		}
		return "n:" + strconv.FormatInt(integer, 10), nil
	default:
		return "", errors.New("process request id must be a string or signed 64-bit integer")
	}
}

func decodeExactJSON(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("additional JSON value")
		}
		return err
	}
	return nil
}
