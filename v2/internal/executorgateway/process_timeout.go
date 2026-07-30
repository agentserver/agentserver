package executorgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
)

type ProcessTimeoutSource string

const (
	ProcessTimeoutSourceGatewayTimer ProcessTimeoutSource = "gateway_timer"
	ProcessTimeoutSourceAgentx       ProcessTimeoutSource = "agentx_timeout_due"
)

type OperationDispatchAuthority interface {
	BeginOperationDispatch(context.Context, BeginOperationDispatchRequest) (BeginOperationDispatchResult, error)
}

type ProcessDispatcher interface {
	DispatchProcess(context.Context, ProcessDispatchRequest) (*ProcessExchange, error)
}

// ProcessTimeoutDispatchRequest contains the already-frozen timeout operation
// and transition record. The coordinator does not allocate identities or
// infer operation params after the deadline has fired.
type ProcessTimeoutDispatchRequest struct {
	ExecutorID                   string
	ExpectedConnectionGeneration int64
	Context                      agentxconn.RoutingContext
	Deadline                     time.Time
	RPCRequestID                 json.RawMessage
	Begin                        BeginOperationDispatchRequest
}

type ProcessTimeoutDispatchResult struct {
	Source                        ProcessTimeoutSource
	ProcessTerminalBeforeDeadline bool
	Begin                         BeginOperationDispatchResult
	Terminate                     *ProcessExchange
}

// ProcessTimeoutCoordinator merges the independent gateway timer and the
// trusted agentx timeout-due signal into one core dispatch boundary. Neither
// signal is permission to send process/terminate: only Began=true is.
type ProcessTimeoutCoordinator struct {
	authority  OperationDispatchAuthority
	dispatcher ProcessDispatcher
}

func NewProcessTimeoutCoordinator(authority OperationDispatchAuthority, dispatcher ProcessDispatcher) (*ProcessTimeoutCoordinator, error) {
	if authority == nil || dispatcher == nil {
		return nil, errors.New("timeout core authority and process dispatcher are required")
	}
	return &ProcessTimeoutCoordinator{authority: authority, dispatcher: dispatcher}, nil
}

func (coordinator *ProcessTimeoutCoordinator) Run(ctx context.Context, start *ProcessExchange, request ProcessTimeoutDispatchRequest) (ProcessTimeoutDispatchResult, error) {
	if ctx == nil || start == nil {
		return ProcessTimeoutDispatchResult{}, errors.New("timeout context and start exchange are required")
	}
	processID, terminateRPC, err := validateTimeoutDispatchRequest(start, request)
	if err != nil {
		return ProcessTimeoutDispatchResult{}, err
	}

	wait := time.Until(request.Deadline)
	if wait < 0 {
		wait = 0
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	result := ProcessTimeoutDispatchResult{}
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case <-start.done:
		if cause := processExchangeTerminal(start); cause != nil {
			return result, cause
		}
		result.ProcessTerminalBeforeDeadline = true
		return result, nil
	case due, ok := <-start.timeoutDue:
		if !ok {
			if cause := processExchangeTerminal(start); cause != nil {
				return result, cause
			}
			result.ProcessTerminalBeforeDeadline = true
			return result, nil
		}
		if due.ProcessID != processID || due.Context != request.Context {
			return result, errors.New("retained agentx timeout signal differs from the frozen timeout operation")
		}
		result.Source = ProcessTimeoutSourceAgentx
	case <-timer.C:
		result.Source = ProcessTimeoutSourceGatewayTimer
	}

	begin, err := coordinator.authority.BeginOperationDispatch(ctx, request.Begin)
	result.Begin = begin
	if err != nil {
		return result, err
	}
	if err := validateTimeoutBeginResult(request, begin); err != nil {
		return result, err
	}
	if !begin.Began {
		return result, nil
	}
	terminate, err := coordinator.dispatcher.DispatchProcess(ctx, ProcessDispatchRequest{
		ExecutorID:                   request.ExecutorID,
		ExpectedConnectionGeneration: request.ExpectedConnectionGeneration,
		Context:                      request.Context,
		RPC:                          terminateRPC,
	})
	result.Terminate = terminate
	if err != nil {
		return result, err
	}
	return result, nil
}

func validateTimeoutDispatchRequest(start *ProcessExchange, request ProcessTimeoutDispatchRequest) (string, json.RawMessage, error) {
	if request.ExecutorID == "" || request.ExpectedConnectionGeneration < 1 || request.Deadline.IsZero() {
		return "", nil, errors.New("timeout executor, connection generation, and deadline are required")
	}
	if start.timeoutDue == nil {
		return "", nil, errors.New("start exchange has no registered timeout directive")
	}
	holder := start.Holder()
	if holder.ExecutorID != request.ExecutorID || holder.Generation != request.ExpectedConnectionGeneration {
		return "", nil, ErrConnectionFenced
	}
	if err := request.Context.Validate(); err != nil {
		return "", nil, err
	}
	begin := request.Begin
	if begin.OperationID != request.Context.OperationID || begin.ExecutionID != request.Context.ExecutionID ||
		begin.RunID != request.Context.RunID || begin.RunAttemptID != request.Context.RunAttemptID ||
		begin.RunAttemptGeneration != request.Context.RunAttemptGeneration ||
		begin.ConnectionGeneration != request.ExpectedConnectionGeneration {
		return "", nil, errors.New("timeout core dispatch identity differs from the frozen routing context")
	}
	var params struct {
		ProcessID string `json:"processId"`
	}
	if err := decodeExactJSON(begin.Params, &params); err != nil || params.ProcessID == "" {
		return "", nil, errors.New("timeout operation params must be exactly one processId")
	}
	if _, err := canonicalRPCID(request.RPCRequestID); err != nil {
		return "", nil, fmt.Errorf("invalid timeout RPC request id: %w", err)
	}
	rpc, err := json.Marshal(struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}{
		ID:     append(json.RawMessage(nil), request.RPCRequestID...),
		Method: execprofile.MethodProcessTerminate,
		Params: append(json.RawMessage(nil), begin.Params...),
	})
	if err != nil {
		return "", nil, fmt.Errorf("encode process/terminate: %w", err)
	}
	message, err := codexwire.Parse(rpc)
	if err != nil || message.Kind != codexwire.KindRequest || message.Method != execprofile.MethodProcessTerminate {
		return "", nil, errors.New("constructed timeout RPC is not process/terminate")
	}
	return params.ProcessID, rpc, nil
}

func validateTimeoutBeginResult(request ProcessTimeoutDispatchRequest, result BeginOperationDispatchResult) error {
	operation := result.Operation
	execution := result.Execution
	if operation.OperationID != request.Context.OperationID || operation.ExecutionID != request.Context.ExecutionID ||
		operation.MutationKey != request.Context.MutationKey {
		return errors.New("core timeout operation differs from the frozen routing context")
	}
	if execution.ExecutionID != request.Context.ExecutionID || execution.RunID != request.Context.RunID ||
		execution.RunAttemptID != request.Context.RunAttemptID || execution.RunAttemptGeneration != request.Context.RunAttemptGeneration ||
		execution.ExecutorID != request.ExecutorID || execution.EnvironmentID != request.Context.EnvID {
		return errors.New("core timeout execution differs from the frozen routing context")
	}
	if result.Began && (operation.Status != "dispatching" || operation.ConnectionGeneration != request.ExpectedConnectionGeneration) {
		return errors.New("core timeout dispatch permission has the wrong status or connection generation")
	}
	return nil
}

func processExchangeTerminal(exchange *ProcessExchange) error {
	exchange.mu.Lock()
	defer exchange.mu.Unlock()
	return exchange.terminal
}
