package sandboxgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

const (
	defaultMaxRequestBytes   int64 = 8 * 1024 * 1024
	operationStreamMediaType       = "application/x-ndjson"
)

type Handler struct {
	service         *Service
	authorizer      Authorizer
	maxRequestBytes int64
	now             func() time.Time
}

func NewHandler(service *Service, authorizer Authorizer, maxRequestBytes int64) (*Handler, error) {
	if service == nil || authorizer == nil {
		return nil, errors.New("sandbox gateway service and authorizer are required")
	}
	if maxRequestBytes == 0 {
		maxRequestBytes = defaultMaxRequestBytes
	}
	if maxRequestBytes < 1024 || maxRequestBytes > 32*1024*1024 {
		return nil, errors.New("sandbox gateway request size limit is invalid")
	}
	return &Handler{service: service, authorizer: authorizer, maxRequestBytes: maxRequestBytes, now: service.now}, nil
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == sandboxcontract.EnsureSandboxPath {
		if request.Method != http.MethodPost {
			handler.notFound(response)
			return
		}
		handler.ensure(response, request)
		return
	}
	route, ok := parseSandboxRoute(request.URL.Path)
	if !ok {
		handler.notFound(response)
		return
	}
	switch {
	case route.action == ActionGet && request.Method == http.MethodGet:
		handler.get(response, request, route.sandboxID)
	case route.action == ActionGet && request.Method == http.MethodDelete:
		handler.delete(response, request, route.sandboxID)
	case route.action == ActionRenewActivity && request.Method == http.MethodPost:
		handler.renew(response, request, route.sandboxID)
	case route.action == ActionReleaseActivity && request.Method == http.MethodPost:
		handler.release(response, request, route.sandboxID)
	case route.action == ActionSetTimeout && request.Method == http.MethodPost:
		handler.setTimeout(response, request, route.sandboxID)
	case route.action == ActionRunCommand && request.Method == http.MethodPost:
		handler.runCommand(response, request, route.sandboxID)
	case route.action == ActionReadFile && request.Method == http.MethodPost:
		handler.readFile(response, request, route.sandboxID)
	case route.action == ActionSignalCommand && request.Method == http.MethodPost:
		handler.signal(response, request, route.sandboxID, route.processID)
	default:
		handler.notFound(response)
	}
}

func (handler *Handler) ensure(response http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authorize(response, request, ActionEnsure)
	if !ok {
		return
	}
	var command sandboxcontract.EnsureSandboxRequest
	if !handler.decode(response, request, &command) {
		return
	}
	result, err := handler.service.EnsureSandbox(request.Context(), principal, command)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	handler.writeJSON(response, result)
}

func (handler *Handler) get(response http.ResponseWriter, request *http.Request, sandboxID string) {
	principal, ok := handler.authorize(response, request, ActionGet)
	if !ok {
		return
	}
	var command sandboxcontract.GetSandboxRequest
	if !handler.decode(response, request, &command) || !handler.matchSandboxPath(response, sandboxID, command.Ref) {
		return
	}
	result, err := handler.service.GetSandbox(request.Context(), principal, command)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	handler.writeJSON(response, result)
}

func (handler *Handler) renew(response http.ResponseWriter, request *http.Request, sandboxID string) {
	principal, ok := handler.authorize(response, request, ActionRenewActivity)
	if !ok {
		return
	}
	var command sandboxcontract.RenewSandboxActivityRequest
	if !handler.decode(response, request, &command) || !handler.matchSandboxPath(response, sandboxID, command.Ref) {
		return
	}
	result, err := handler.service.RenewSandboxActivity(request.Context(), principal, command)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	handler.writeJSON(response, result)
}

func (handler *Handler) release(response http.ResponseWriter, request *http.Request, sandboxID string) {
	principal, ok := handler.authorize(response, request, ActionReleaseActivity)
	if !ok {
		return
	}
	var command sandboxcontract.ReleaseSandboxActivityRequest
	if !handler.decode(response, request, &command) || !handler.matchSandboxPath(response, sandboxID, command.Ref) {
		return
	}
	result, err := handler.service.ReleaseSandboxActivity(request.Context(), principal, command)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	handler.writeJSON(response, result)
}

func (handler *Handler) setTimeout(response http.ResponseWriter, request *http.Request, sandboxID string) {
	principal, ok := handler.authorize(response, request, ActionSetTimeout)
	if !ok {
		return
	}
	var command sandboxcontract.SetSandboxTimeoutRequest
	if !handler.decode(response, request, &command) || !handler.matchSandboxPath(response, sandboxID, command.Ref) {
		return
	}
	result, err := handler.service.SetSandboxTimeout(request.Context(), principal, command)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	handler.writeJSON(response, result)
}

func (handler *Handler) delete(response http.ResponseWriter, request *http.Request, sandboxID string) {
	principal, ok := handler.authorize(response, request, ActionDelete)
	if !ok {
		return
	}
	var command sandboxcontract.DeleteSandboxRequest
	if !handler.decode(response, request, &command) || !handler.matchSandboxPath(response, sandboxID, command.Ref) {
		return
	}
	result, err := handler.service.DeleteSandbox(request.Context(), principal, command)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	handler.writeJSON(response, result)
}

func (handler *Handler) runCommand(response http.ResponseWriter, request *http.Request, sandboxID string) {
	principal, ok := handler.authorize(response, request, ActionRunCommand)
	if !ok {
		return
	}
	var command sandboxcontract.RunCommandRequest
	if !handler.decode(response, request, &command) || !handler.matchSandboxPath(response, sandboxID, command.Ref) {
		return
	}
	exchange, err := handler.service.RunCommand(request.Context(), principal, command)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	handler.streamExchange(response, request, command.Identity, command.Ref, exchange, command.OutputLimitBytes)
}

func (handler *Handler) readFile(response http.ResponseWriter, request *http.Request, sandboxID string) {
	principal, ok := handler.authorize(response, request, ActionReadFile)
	if !ok {
		return
	}
	var command sandboxcontract.ReadFileRequest
	if !handler.decode(response, request, &command) || !handler.matchSandboxPath(response, sandboxID, command.Ref) {
		return
	}
	exchange, err := handler.service.ReadFile(request.Context(), principal, command)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	handler.streamExchange(response, request, command.Identity, command.Ref, exchange, int64(command.Limit))
}

func (handler *Handler) signal(response http.ResponseWriter, request *http.Request, sandboxID, processID string) {
	principal, ok := handler.authorize(response, request, ActionSignalCommand)
	if !ok {
		return
	}
	var command sandboxcontract.SignalCommandRequest
	if !handler.decode(response, request, &command) || !handler.matchSandboxPath(response, sandboxID, command.Ref) {
		return
	}
	if command.ProcessID != processID {
		handler.writeError(response, invalidRequest(errors.New("processId differs from the request path")))
		return
	}
	exchange, err := handler.service.SignalCommand(request.Context(), principal, command)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	handler.streamExchange(response, request, command.Identity, command.Ref, exchange, 1024)
}

func (handler *Handler) streamExchange(response http.ResponseWriter, request *http.Request, identity sandboxcontract.OperationIdentity, ref sandboxcontract.SandboxRef, exchange executionbackend.Exchange, byteLimit int64) {
	acknowledgement, err := exchange.AwaitAcknowledgement(request.Context())
	if err != nil {
		handler.writeError(response, err)
		return
	}
	if err := acknowledgement.Validate(); err != nil {
		handler.writeError(response, dispatchUnknown("invalid_provider_acknowledgement", err))
		return
	}
	response.Header().Set("Content-Type", operationStreamMediaType)
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(response)
	encoder.SetEscapeHTML(false)
	flush, _ := response.(http.Flusher)
	writeFrame := func(frame sandboxcontract.OperationFrame) bool {
		if err := frame.Validate(); err != nil {
			return false
		}
		if err := encoder.Encode(frame); err != nil {
			return false
		}
		if flush != nil {
			flush.Flush()
		}
		return true
	}
	if !writeFrame(sandboxcontract.OperationFrame{
		Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameAcknowledgement,
		Identity: identity, Ref: ref, Acknowledgement: &acknowledgement,
	}) {
		return
	}
	var lastSequence uint64
	var outputBytes int64
	for {
		event, eventErr := exchange.NextEvent(request.Context())
		if errors.Is(eventErr, io.EOF) {
			break
		}
		if eventErr != nil {
			handler.writeUnknownTerminal(writeFrame, identity, ref, "provider_stream_error")
			return
		}
		if err := event.Validate(); err != nil || event.Sequence != lastSequence+1 {
			handler.writeUnknownTerminal(writeFrame, identity, ref, "provider_stream_invalid")
			return
		}
		lastSequence = event.Sequence
		outputBytes += int64(len(event.Data))
		if outputBytes > byteLimit {
			handler.writeUnknownTerminal(writeFrame, identity, ref, "output_limit_exceeded")
			return
		}
		if !writeFrame(sandboxcontract.OperationFrame{
			Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameEvent,
			Identity: identity, Ref: ref, Event: &event,
		}) {
			return
		}
	}
	terminal, err := exchange.AwaitTerminal(request.Context())
	if err != nil {
		handler.writeUnknownTerminal(writeFrame, identity, ref, "provider_terminal_unknown")
		return
	}
	if err := terminal.Validate(); err != nil {
		handler.writeUnknownTerminal(writeFrame, identity, ref, "provider_terminal_invalid")
		return
	}
	writeFrame(sandboxcontract.OperationFrame{
		Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameTerminal,
		Identity: identity, Ref: ref, Terminal: &terminal,
	})
}

func (handler *Handler) writeUnknownTerminal(writeFrame func(sandboxcontract.OperationFrame) bool, identity sandboxcontract.OperationIdentity, ref sandboxcontract.SandboxRef, reason string) {
	terminal := executionbackend.TerminalResult{
		Status: executionbackend.TerminalUnknown, ReasonCode: reason,
		OutputComplete: false, CompletedAt: handler.now().UTC(),
	}
	writeFrame(sandboxcontract.OperationFrame{
		Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameTerminal,
		Identity: identity, Ref: ref, Terminal: &terminal,
	})
}

func (handler *Handler) authorize(response http.ResponseWriter, request *http.Request, action string) (Principal, bool) {
	principal, err := handler.authorizer.Authorize(request, action)
	if err != nil {
		handler.writeError(response, forbidden(err))
		return Principal{}, false
	}
	return principal, true
}

func (handler *Handler) decode(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		handler.writeError(response, invalidRequest(errors.New("Content-Type must be application/json")))
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, handler.maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		handler.writeError(response, invalidRequest(fmt.Errorf("decode JSON command: %w", err)))
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("request contains more than one JSON value")
		}
		handler.writeError(response, invalidRequest(err))
		return false
	}
	return true
}

func (handler *Handler) matchSandboxPath(response http.ResponseWriter, sandboxID string, ref sandboxcontract.SandboxRef) bool {
	if ref.SandboxID != sandboxID {
		handler.writeError(response, invalidRequest(errors.New("sandboxId differs from the request path")))
		return false
	}
	return true
}

func (handler *Handler) writeJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(response)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func (handler *Handler) writeError(response http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	code := "internal_unavailable"
	message := "sandbox request could not be completed"
	outcome := executionbackend.OutcomeOf(err)
	var serviceError *Error
	if errors.As(err, &serviceError) {
		status, code, message = serviceError.HTTPStatus, serviceError.Code, serviceError.Message
		if serviceError.Outcome != "" {
			outcome = serviceError.Outcome
		}
	} else {
		var dispatchError *executionbackend.DispatchError
		if errors.As(err, &dispatchError) {
			code = dispatchError.Code
			switch dispatchError.Outcome {
			case executionbackend.OutcomeNotSent:
				status = http.StatusBadRequest
			case executionbackend.OutcomeRejected:
				status = http.StatusConflict
			case executionbackend.OutcomeUnknown:
				status = http.StatusServiceUnavailable
			}
		}
	}
	if status < 400 || status > 599 {
		status = http.StatusServiceUnavailable
	}
	if code == "" {
		code = "internal_unavailable"
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(sandboxcontract.ErrorResponse{
		Code: code, Message: message, Outcome: string(outcome),
	})
}

func (handler *Handler) notFound(response http.ResponseWriter) {
	handler.writeError(response, &Error{HTTPStatus: http.StatusNotFound, Code: "not_found", Message: "sandbox endpoint not found", Outcome: executionbackend.OutcomeNotSent})
}

type sandboxRoute struct {
	sandboxID string
	processID string
	action    string
}

func parseSandboxRoute(value string) (sandboxRoute, bool) {
	if !strings.HasPrefix(value, sandboxcontract.SandboxPathPrefix) {
		return sandboxRoute{}, false
	}
	remainder := strings.TrimPrefix(value, sandboxcontract.SandboxPathPrefix)
	if remainder == "" || strings.Contains(remainder, "//") {
		return sandboxRoute{}, false
	}
	if !strings.Contains(remainder, "/") {
		if strings.HasSuffix(remainder, ":renew-activity") {
			return boundedSandboxRoute(strings.TrimSuffix(remainder, ":renew-activity"), "", ActionRenewActivity)
		}
		if strings.HasSuffix(remainder, ":release-activity") {
			return boundedSandboxRoute(strings.TrimSuffix(remainder, ":release-activity"), "", ActionReleaseActivity)
		}
		if strings.HasSuffix(remainder, ":set-timeout") {
			return boundedSandboxRoute(strings.TrimSuffix(remainder, ":set-timeout"), "", ActionSetTimeout)
		}
		return boundedSandboxRoute(remainder, "", ActionGet)
	}
	parts := strings.Split(remainder, "/")
	if len(parts) == 2 && parts[1] == "commands:run" {
		return boundedSandboxRoute(parts[0], "", ActionRunCommand)
	}
	if len(parts) == 2 && parts[1] == "files:read" {
		return boundedSandboxRoute(parts[0], "", ActionReadFile)
	}
	if len(parts) == 3 && parts[1] == "processes" && strings.HasSuffix(parts[2], ":signal") {
		return boundedSandboxRoute(parts[0], strings.TrimSuffix(parts[2], ":signal"), ActionSignalCommand)
	}
	return sandboxRoute{}, false
}

func boundedSandboxRoute(sandboxID, processID, action string) (sandboxRoute, bool) {
	if sandboxID == "" || len(sandboxID) > 256 || strings.ContainsAny(sandboxID, "/?#") {
		return sandboxRoute{}, false
	}
	if processID != "" && (len(processID) > 256 || strings.ContainsAny(processID, "/?#")) {
		return sandboxRoute{}, false
	}
	return sandboxRoute{sandboxID: sandboxID, processID: processID, action: action}, true
}
