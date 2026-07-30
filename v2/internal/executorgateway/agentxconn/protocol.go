package agentxconn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
)

const (
	maxProtocolVersions  = 8
	maxEnvironments      = 256
	maxActiveProcesses   = 256
	maxProtocolTextBytes = 4096
	maxProtocolDepth     = 256
)

var (
	uuidPattern     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	digestPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
	releasePattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
	commitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	platformPattern = regexp.MustCompile(`^(linux|darwin|windows)-(amd64|arm64)$`)
)

func Decode(raw []byte, limits Limits) (Message, error) {
	if err := validateLimits(limits); err != nil {
		return Message{}, err
	}
	if len(raw) == 0 {
		return Message{}, protocolError(ErrorMalformedFrame, true, "message is empty")
	}
	if len(raw) > limits.MaxFrameBytes {
		return Message{}, protocolError(ErrorMalformedFrame, true, "message is %d bytes, limit is %d", len(raw), limits.MaxFrameBytes)
	}
	if !utf8.Valid(raw) {
		return Message{}, protocolError(ErrorMalformedFrame, true, "message is not valid UTF-8")
	}
	if err := validateJSONDocument(raw, limits.MaxJSONValues, limits.MaxJSONDepth); err != nil {
		return Message{}, protocolError(ErrorMalformedFrame, true, "%v", err)
	}
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return Message{}, protocolError(ErrorMalformedFrame, true, "decode message type: %v", err)
	}
	message := Message{Type: discriminator.Type, Raw: append(json.RawMessage(nil), raw...)}
	switch discriminator.Type {
	case MessageTypeHello:
		var value Hello
		if err := decodeRequiredObject(raw, &value, "type", "connectionId", "protocolVersions", "agentxVersion", "runtimeManifestSha256", "execProtocolSourceSha256", "environments"); err != nil {
			return Message{}, protocolError(ErrorMalformedFrame, true, "decode hello: %v", err)
		}
		if err := validateHelloFieldPresence(raw, value); err != nil {
			return Message{}, protocolError(ErrorMalformedFrame, true, "decode hello: %v", err)
		}
		if err := value.Validate(); err != nil {
			return Message{}, err
		}
		message.Hello = &value
	case MessageTypeWelcome:
		var value Welcome
		if err := decodeRequiredObject(raw, &value, "type", "protocolVersion", "gatewayInstanceId", "sessionId", "generation", "resumeStatus", "resumeWindowMs", "gatewaySentThrough", "gatewayReceivedThrough"); err != nil {
			return Message{}, protocolError(ErrorMalformedFrame, true, "decode welcome: %v", err)
		}
		if err := value.Validate(); err != nil {
			return Message{}, err
		}
		message.Welcome = &value
	case MessageTypeLifecycle, MessageTypeRPC:
		var value Frame
		required := []string{"type", "sessionId", "sessionSeq", "ack", "generation", "rpc"}
		if discriminator.Type == MessageTypeRPC {
			required = append(required, "context")
		}
		if err := decodeRequiredObject(raw, &value, required...); err != nil {
			return Message{}, protocolError(ErrorMalformedFrame, true, "decode sequenced frame: %v", err)
		}
		if discriminator.Type == MessageTypeLifecycle {
			fields, err := requiredObjectFields(raw)
			if err != nil {
				return Message{}, protocolError(ErrorMalformedFrame, true, "decode lifecycle frame: %v", err)
			}
			if _, present := fields["context"]; present {
				return Message{}, protocolError(ErrorMalformedFrame, true, "lifecycle frame cannot contain context")
			}
			if _, present := fields["directives"]; present {
				return Message{}, protocolError(ErrorMalformedFrame, true, "lifecycle frame cannot contain directives")
			}
		} else {
			fields, err := requiredObjectFields(raw)
			if err != nil {
				return Message{}, protocolError(ErrorMalformedFrame, true, "decode business frame: %v", err)
			}
			if _, present := fields["directives"]; present && value.Directives == nil {
				return Message{}, protocolError(ErrorMalformedFrame, true, "business frame directives cannot be null")
			}
		}
		if err := value.validateStructure(); err != nil {
			return Message{}, err
		}
		message.Frame = &value
	case MessageTypeAck:
		var value Ack
		if err := decodeRequiredObject(raw, &value, "type", "sessionId", "generation", "ack"); err != nil {
			return Message{}, protocolError(ErrorMalformedFrame, true, "decode ack: %v", err)
		}
		if err := value.Validate(); err != nil {
			return Message{}, err
		}
		message.Ack = &value
	case MessageTypeSessionError:
		var value SessionError
		if err := decodeRequiredObject(raw, &value, "type", "code", "message", "terminal"); err != nil {
			return Message{}, protocolError(ErrorMalformedFrame, true, "decode session error: %v", err)
		}
		fields, err := requiredObjectFields(raw)
		if err != nil {
			return Message{}, protocolError(ErrorMalformedFrame, true, "decode session error: %v", err)
		}
		if _, present := fields["lostFrom"]; present && value.LostFrom == nil {
			return Message{}, protocolError(ErrorMalformedFrame, true, "lostFrom cannot be null")
		}
		if _, present := fields["lostTo"]; present && value.LostTo == nil {
			return Message{}, protocolError(ErrorMalformedFrame, true, "lostTo cannot be null")
		}
		if err := value.Validate(); err != nil {
			return Message{}, err
		}
		message.SessionError = &value
	default:
		return Message{}, protocolError(ErrorMalformedFrame, true, "unknown message type %q", discriminator.Type)
	}
	return message, nil
}

func validateHelloFieldPresence(raw []byte, hello Hello) error {
	fields, err := requiredObjectFields(raw)
	if err != nil {
		return err
	}
	if _, present := fields["resume"]; present && hello.Resume == nil {
		return errors.New("resume cannot be null")
	}
	if hello.Resume != nil {
		if err := requireObjectFields(fields["resume"], "gatewayInstanceId", "sessionId", "generation", "agentxSentThrough", "agentxReceivedThrough"); err != nil {
			return fmt.Errorf("resume: %w", err)
		}
	}
	var environments []json.RawMessage
	if err := decodeRequiredArray(fields["environments"], &environments); err != nil {
		return fmt.Errorf("environments: %w", err)
	}
	for index, rawEnvironment := range environments {
		environmentFields, err := requiredObjectFields(rawEnvironment)
		if err != nil {
			return fmt.Errorf("environment %d: %w", index, err)
		}
		for _, required := range []string{"envId", "platform", "codexRelease", "codexCommit", "codexSha256", "outerProfileVersion", "processMethods", "activeProcesses", "insecureDev"} {
			value, present := environmentFields[required]
			if !present {
				return fmt.Errorf("environment %d: required field %q is missing", index, required)
			}
			if isJSONNull(value) {
				return fmt.Errorf("environment %d: required field %q cannot be null", index, required)
			}
		}
		var activeProcesses []json.RawMessage
		if err := decodeRequiredArray(environmentFields["activeProcesses"], &activeProcesses); err != nil {
			return fmt.Errorf("environment %d activeProcesses: %w", index, err)
		}
	}
	return nil
}

func Encode(value any, limits Limits) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode agentx message: %w", err)
	}
	if _, err := Decode(raw, limits); err != nil {
		return nil, err
	}
	return raw, nil
}

func (h Hello) Validate() error {
	if h.Type != MessageTypeHello {
		return protocolError(ErrorMalformedFrame, true, "hello type = %q", h.Type)
	}
	if err := validateUUID("connectionId", h.ConnectionID); err != nil {
		return err
	}
	if len(h.ProtocolVersions) == 0 || len(h.ProtocolVersions) > maxProtocolVersions {
		return protocolError(ErrorMalformedFrame, true, "protocolVersions must contain between 1 and %d entries", maxProtocolVersions)
	}
	seenVersions := make(map[string]struct{}, len(h.ProtocolVersions))
	for _, version := range h.ProtocolVersions {
		if !versionPattern.MatchString(version) {
			return protocolError(ErrorMalformedFrame, true, "invalid protocol version %q", version)
		}
		if _, duplicate := seenVersions[version]; duplicate {
			return protocolError(ErrorMalformedFrame, true, "duplicate protocol version %q", version)
		}
		seenVersions[version] = struct{}{}
	}
	if err := validateText("agentxVersion", h.AgentxVersion, 256); err != nil {
		return err
	}
	if err := validateDigest("runtimeManifestSha256", h.RuntimeManifestSHA256); err != nil {
		return err
	}
	if err := validateDigest("execProtocolSourceSha256", h.ExecProtocolSourceSHA256); err != nil {
		return err
	}
	if len(h.Environments) == 0 || len(h.Environments) > maxEnvironments {
		return protocolError(ErrorMalformedFrame, true, "environments must contain between 1 and %d entries", maxEnvironments)
	}
	seenEnvironments := make(map[string]struct{}, len(h.Environments))
	for index, environment := range h.Environments {
		if err := environment.validate(); err != nil {
			return protocolError(ErrorMalformedFrame, true, "environment %d: %v", index, err)
		}
		if _, duplicate := seenEnvironments[environment.EnvID]; duplicate {
			return protocolError(ErrorMalformedFrame, true, "duplicate envId %q", environment.EnvID)
		}
		seenEnvironments[environment.EnvID] = struct{}{}
	}
	if h.Resume == nil {
		for _, environment := range h.Environments {
			if len(environment.ActiveProcesses) != 0 {
				return protocolError(ErrorMalformedFrame, true, "fresh hello cannot claim active processes")
			}
		}
		return nil
	}
	return h.Resume.Validate()
}

func (environment HelloEnvironment) validate() error {
	if err := validateUUID("envId", environment.EnvID); err != nil {
		return err
	}
	if !platformPattern.MatchString(environment.Platform) {
		return fmt.Errorf("platform %q is unsupported", environment.Platform)
	}
	if !releasePattern.MatchString(environment.CodexRelease) {
		return fmt.Errorf("codexRelease %q is invalid", environment.CodexRelease)
	}
	if !commitPattern.MatchString(environment.CodexCommit) {
		return fmt.Errorf("codexCommit is not a lowercase 40-character Git SHA")
	}
	if !digestPattern.MatchString(environment.CodexSHA256) {
		return fmt.Errorf("codexSha256 is not lowercase SHA-256 hex")
	}
	if !execprofile.AllowsEnvironmentProfile(environment.OuterProfileVersion) {
		return fmt.Errorf("outerProfileVersion = %q is unsupported", environment.OuterProfileVersion)
	}
	if !slices.Equal(environment.ProcessMethods, execprofile.ProcessMethods()) {
		return fmt.Errorf("processMethods = %q, want exact %q", environment.ProcessMethods, execprofile.ProcessMethods())
	}
	if len(environment.ActiveProcesses) > maxActiveProcesses {
		return fmt.Errorf("activeProcesses exceeds %d entries", maxActiveProcesses)
	}
	seenProcesses := make(map[string]struct{}, len(environment.ActiveProcesses))
	seenInstances := make(map[string]struct{}, len(environment.ActiveProcesses))
	for _, process := range environment.ActiveProcesses {
		if err := validateUUID("processId", process.ProcessID); err != nil {
			return err
		}
		if err := validateUUID("localExecInstanceId", process.LocalExecInstanceID); err != nil {
			return err
		}
		if _, duplicate := seenProcesses[process.ProcessID]; duplicate {
			return fmt.Errorf("duplicate processId %q", process.ProcessID)
		}
		if _, duplicate := seenInstances[process.LocalExecInstanceID]; duplicate {
			return fmt.Errorf("duplicate localExecInstanceId %q", process.LocalExecInstanceID)
		}
		seenProcesses[process.ProcessID] = struct{}{}
		seenInstances[process.LocalExecInstanceID] = struct{}{}
	}
	return nil
}

func (cursor ResumeCursor) Validate() error {
	if err := validateUUID("gatewayInstanceId", cursor.GatewayInstanceID); err != nil {
		return err
	}
	if err := validateUUID("sessionId", cursor.SessionID); err != nil {
		return err
	}
	if cursor.Generation < 1 {
		return protocolError(ErrorMalformedFrame, true, "resume generation must be positive")
	}
	return nil
}

func (w Welcome) Validate() error {
	if w.Type != MessageTypeWelcome {
		return protocolError(ErrorMalformedFrame, true, "welcome type = %q", w.Type)
	}
	if w.ProtocolVersion != CurrentProtocolVersion {
		return protocolError(ErrorProtocolVersionUnsupported, true, "welcome protocolVersion = %q", w.ProtocolVersion)
	}
	if err := validateUUID("gatewayInstanceId", w.GatewayInstanceID); err != nil {
		return err
	}
	if err := validateUUID("sessionId", w.SessionID); err != nil {
		return err
	}
	if w.Generation < 1 {
		return protocolError(ErrorMalformedFrame, true, "welcome generation must be positive")
	}
	if w.ResumeStatus != "fresh" && w.ResumeStatus != "resumed" {
		return protocolError(ErrorMalformedFrame, true, "resumeStatus must be fresh or resumed")
	}
	if w.ResumeWindowMillis != ResumeWindowMillis {
		return protocolError(ErrorMalformedFrame, true, "resumeWindowMs = %d, want %d", w.ResumeWindowMillis, ResumeWindowMillis)
	}
	if w.ResumeStatus == "fresh" && (w.GatewaySentThrough != 0 || w.GatewayReceivedThrough != 0) {
		return protocolError(ErrorMalformedFrame, true, "fresh welcome must have zero sequence cursors")
	}
	return nil
}

func (frame Frame) validateStructure() error {
	if frame.Type != MessageTypeLifecycle && frame.Type != MessageTypeRPC {
		return protocolError(ErrorMalformedFrame, true, "sequenced frame type = %q", frame.Type)
	}
	if err := validateUUID("sessionId", frame.SessionID); err != nil {
		return err
	}
	if frame.SessionSeq == 0 {
		return protocolError(ErrorMalformedFrame, true, "sessionSeq must be positive")
	}
	if frame.Generation < 1 {
		return protocolError(ErrorMalformedFrame, true, "generation must be positive")
	}
	if len(frame.RPC) == 0 {
		return protocolError(ErrorMalformedFrame, true, "rpc is required")
	}
	if frame.Type == MessageTypeLifecycle {
		if frame.Context != nil || frame.Directives != nil {
			return protocolError(ErrorMalformedFrame, true, "lifecycle frame cannot carry routing context or dispatch directives")
		}
		if _, err := parseStandardRPC(frame.RPC); err != nil {
			return err
		}
		return nil
	}
	if frame.Context == nil {
		return protocolError(ErrorMalformedFrame, true, "business RPC frame requires routing context")
	}
	if err := frame.Context.Validate(); err != nil {
		return err
	}
	rpc, err := codexwire.Parse(frame.RPC)
	if err != nil {
		return protocolError(ErrorMalformedFrame, true, "invalid inner business RPC: %v", err)
	}
	if frame.Directives != nil {
		if rpc.Kind != codexwire.KindRequest {
			return protocolError(ErrorMalformedFrame, true, "dispatch directives require a process/start request")
		}
		if err := validateDispatchDirectives(*frame.Context, frame.Directives, rpc.Method); err != nil {
			return err
		}
	}
	return nil
}

// ValidateForReceiver applies direction-specific method ownership. It is
// separate from Decode because recorded fixtures may be decoded before their
// direction is known.
func (frame Frame) ValidateForReceiver(receiver Role) error {
	if receiver != RoleGateway && receiver != RoleAgentx {
		return protocolError(ErrorMalformedFrame, true, "invalid receiver role %q", receiver)
	}
	if err := frame.validateStructure(); err != nil {
		return err
	}
	sender := receiver.peer()
	if frame.Type == MessageTypeLifecycle {
		rpc, err := parseStandardRPC(frame.RPC)
		if err != nil {
			return err
		}
		return validateLifecycleDirection(frame.SessionID, sender, rpc)
	}
	rpc, err := codexwire.Parse(frame.RPC)
	if err != nil {
		return protocolError(ErrorMalformedFrame, true, "invalid inner business RPC: %v", err)
	}
	if err := validateStockRPCEnvelope(rpc); err != nil {
		return err
	}
	switch sender {
	case RoleGateway:
		switch rpc.Kind {
		case codexwire.KindRequest:
			switch {
			case execprofile.AllowsProcessMethod(rpc.Method):
				if err := validateProcessRequestParams(rpc); err != nil {
					return err
				}
			case execprofile.AllowsFilesystemReadMethod(rpc.Method):
				if err := validateFilesystemReadRequestParams(rpc); err != nil {
					return err
				}
			default:
				return protocolError(ErrorMethodNotNegotiated, true, "gateway method %q is outside %s", rpc.Method, execprofile.Version)
			}
			if err := validateDispatchDirectives(*frame.Context, frame.Directives, rpc.Method); err != nil {
				return err
			}
		case codexwire.KindResponse, codexwire.KindError:
			if frame.Directives != nil {
				return protocolError(ErrorMalformedFrame, true, "gateway response cannot carry dispatch directives")
			}
			// Responses to an agentx-originated network/policyRequest are
			// correlated and type-checked by the request table.
		default:
			return protocolError(ErrorMethodNotNegotiated, true, "gateway cannot send business %s", rpc.Kind)
		}
	case RoleAgentx:
		if frame.Directives != nil {
			return protocolError(ErrorMalformedFrame, true, "agentx frame cannot carry gateway dispatch directives")
		}
		switch rpc.Kind {
		case codexwire.KindRequest:
			if rpc.Method != execprofile.ReverseMethodNetworkPolicyRequest {
				return protocolError(ErrorMethodNotNegotiated, true, "agentx reverse method %q is not negotiated", rpc.Method)
			}
			if err := validateNetworkPolicyRequestParams(rpc); err != nil {
				return err
			}
		case codexwire.KindNotification:
			if rpc.Method == NotificationAgentxTimeoutDue {
				if err := validateTimeoutDueNotificationParams(rpc); err != nil {
					return err
				}
				break
			}
			if !execprofile.AllowsProcessNotification(rpc.Method) {
				return protocolError(ErrorMethodNotNegotiated, true, "agentx notification %q is not negotiated", rpc.Method)
			}
			if err := validateProcessNotificationParams(rpc); err != nil {
				return err
			}
		case codexwire.KindResponse, codexwire.KindError:
		default:
			return protocolError(ErrorMethodNotNegotiated, true, "agentx business RPC kind %s is not negotiated", rpc.Kind)
		}
	}
	return nil
}

func validateDispatchDirectives(context RoutingContext, directives *DispatchDirectives, method string) error {
	if directives == nil {
		return nil
	}
	if method != execprofile.MethodProcessStart {
		return protocolError(ErrorMalformedFrame, true, "dispatch directives are valid only for process/start")
	}
	if directives.ProcessTimeout == nil {
		return protocolError(ErrorMalformedFrame, true, "process/start directives require processTimeout")
	}
	timeout := directives.ProcessTimeout
	if timeout.AfterMillis < 1 || timeout.AfterMillis > maxProcessTimeoutMS {
		return protocolError(ErrorMalformedFrame, true, "processTimeout.afterMs must be between 1 and %d", maxProcessTimeoutMS)
	}
	if err := validateUUID("processTimeout.operationId", timeout.OperationID); err != nil {
		return err
	}
	if err := validateUUID("processTimeout.mutationKey", timeout.MutationKey); err != nil {
		return err
	}
	if timeout.OperationID == context.OperationID || timeout.MutationKey == context.MutationKey {
		return protocolError(ErrorMalformedFrame, true, "processTimeout must use a distinct preallocated operation and mutation key")
	}
	return nil
}

func (context RoutingContext) Validate() error {
	identifiers := []struct {
		name  string
		value string
	}{
		{"workspaceId", context.WorkspaceID},
		{"runId", context.RunID},
		{"runAttemptId", context.RunAttemptID},
		{"executionId", context.ExecutionID},
		{"operationId", context.OperationID},
		{"envId", context.EnvID},
		{"mutationKey", context.MutationKey},
	}
	for _, identifier := range identifiers {
		if err := validateUUID(identifier.name, identifier.value); err != nil {
			return err
		}
	}
	if context.RunAttemptGeneration < 1 {
		return protocolError(ErrorMalformedFrame, true, "runAttemptGeneration must be positive")
	}
	return nil
}

func (ack Ack) Validate() error {
	if ack.Type != MessageTypeAck {
		return protocolError(ErrorMalformedFrame, true, "ack type = %q", ack.Type)
	}
	if err := validateUUID("sessionId", ack.SessionID); err != nil {
		return err
	}
	if ack.Generation < 1 {
		return protocolError(ErrorMalformedFrame, true, "ack generation must be positive")
	}
	return nil
}

func (sessionError SessionError) Validate() error {
	if sessionError.Type != MessageTypeSessionError {
		return protocolError(ErrorMalformedFrame, true, "session error type = %q", sessionError.Type)
	}
	if !knownErrorCode(sessionError.Code) {
		return protocolError(ErrorMalformedFrame, true, "unknown session error code %q", sessionError.Code)
	}
	if err := validateText("message", sessionError.Message, maxProtocolTextBytes); err != nil {
		return err
	}
	if (sessionError.LostFrom == nil) != (sessionError.LostTo == nil) {
		return protocolError(ErrorMalformedFrame, true, "lostFrom and lostTo must be present together")
	}
	hasLostRange := sessionError.LostFrom != nil
	requiresLostRange := sessionError.Code == ErrorResumeGap || sessionError.Code == ErrorOutputGap || sessionError.Code == ErrorBufferOverflow
	if requiresLostRange && !hasLostRange {
		return protocolError(ErrorMalformedFrame, true, "gap/overflow error requires an exact lost range")
	}
	if sessionError.LostFrom != nil {
		if sessionError.Code != ErrorResumeGap && sessionError.Code != ErrorOutputGap && sessionError.Code != ErrorBufferOverflow {
			return protocolError(ErrorMalformedFrame, true, "lost range is valid only for a gap/overflow error")
		}
		if *sessionError.LostFrom == 0 || *sessionError.LostTo < *sessionError.LostFrom {
			return protocolError(ErrorMalformedFrame, true, "lost range must be non-empty")
		}
	}
	return nil
}

func validateLifecycleDirection(sessionID string, sender Role, rpc standardRPC) error {
	switch sender {
	case RoleGateway:
		switch rpc.Kind {
		case standardRPCRequest:
			if rpc.Method != "initialize" {
				return protocolError(ErrorMethodNotNegotiated, true, "gateway lifecycle request %q is not negotiated", rpc.Method)
			}
			var params struct {
				ProtocolVersion     string   `json:"protocolVersion"`
				ClientName          string   `json:"clientName"`
				OuterProfileVersion string   `json:"outerProfileVersion"`
				ProcessMethods      []string `json:"processMethods"`
			}
			if err := decodeRequiredObject(rpc.Params, &params, "protocolVersion", "clientName", "outerProfileVersion", "processMethods"); err != nil {
				return protocolError(ErrorMalformedFrame, true, "decode initialize params: %v", err)
			}
			if params.ProtocolVersion != CurrentProtocolVersion || params.ClientName != "agentserver-executor-gateway" || params.OuterProfileVersion != execprofile.Version || !slices.Equal(params.ProcessMethods, execprofile.ProcessMethods()) {
				return protocolError(ErrorProtocolVersionUnsupported, true, "initialize profile does not match protocol %s/%s", CurrentProtocolVersion, execprofile.Version)
			}
		case standardRPCNotification:
			if rpc.Method != "initialized" {
				return protocolError(ErrorMethodNotNegotiated, true, "gateway lifecycle notification %q is not negotiated", rpc.Method)
			}
			var params struct{}
			if err := decodeRequiredObject(rpc.Params, &params); err != nil {
				return protocolError(ErrorMalformedFrame, true, "decode initialized params: %v", err)
			}
		default:
			return protocolError(ErrorMethodNotNegotiated, true, "gateway lifecycle kind is not request/notification")
		}
	case RoleAgentx:
		switch rpc.Kind {
		case standardRPCResponse:
			var result struct {
				SessionID           string   `json:"sessionId"`
				ProtocolVersion     string   `json:"protocolVersion"`
				ServerName          string   `json:"serverName"`
				OuterProfileVersion string   `json:"outerProfileVersion"`
				ProcessMethods      []string `json:"processMethods"`
			}
			if err := decodeRequiredObject(rpc.Result, &result, "sessionId", "protocolVersion", "serverName", "outerProfileVersion", "processMethods"); err != nil {
				return protocolError(ErrorMalformedFrame, true, "decode initialize result: %v", err)
			}
			if result.SessionID != sessionID || result.ProtocolVersion != CurrentProtocolVersion || result.ServerName != "agentx" || result.OuterProfileVersion != execprofile.Version || !slices.Equal(result.ProcessMethods, execprofile.ProcessMethods()) {
				return protocolError(ErrorProtocolVersionUnsupported, true, "initialize result does not match negotiated session/profile")
			}
		case standardRPCError:
		default:
			return protocolError(ErrorMethodNotNegotiated, true, "agentx lifecycle must be an initialize response")
		}
	}
	return nil
}

func parseStandardRPC(raw json.RawMessage) (standardRPC, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return standardRPC{}, protocolError(ErrorMalformedFrame, true, "decode lifecycle RPC: %v", err)
	}
	allowed := map[string]struct{}{"jsonrpc": {}, "id": {}, "method": {}, "params": {}, "result": {}, "error": {}}
	for field := range fields {
		if _, ok := allowed[field]; !ok {
			return standardRPC{}, protocolError(ErrorMalformedFrame, true, "unknown lifecycle RPC field %q", field)
		}
	}
	var version string
	if err := json.Unmarshal(fields["jsonrpc"], &version); err != nil || version != "2.0" {
		return standardRPC{}, protocolError(ErrorMalformedFrame, true, "lifecycle RPC requires jsonrpc 2.0")
	}
	_, hasID := fields["id"]
	methodRaw, hasMethod := fields["method"]
	params, hasParams := fields["params"]
	result, hasResult := fields["result"]
	errorRaw, hasError := fields["error"]
	if hasID {
		if err := validateRPCID(fields["id"]); err != nil {
			return standardRPC{}, err
		}
	}
	rpc := standardRPC{ID: append(json.RawMessage(nil), fields["id"]...)}
	if hasMethod {
		if hasResult || hasError {
			return standardRPC{}, protocolError(ErrorMalformedFrame, true, "lifecycle request cannot contain result/error")
		}
		if err := json.Unmarshal(methodRaw, &rpc.Method); err != nil || rpc.Method == "" {
			return standardRPC{}, protocolError(ErrorMalformedFrame, true, "lifecycle method must be non-empty")
		}
		if !hasParams {
			return standardRPC{}, protocolError(ErrorMalformedFrame, true, "lifecycle method requires params")
		}
		rpc.Params = append(json.RawMessage(nil), params...)
		if hasID {
			rpc.Kind = standardRPCRequest
		} else {
			rpc.Kind = standardRPCNotification
		}
		return rpc, nil
	}
	if !hasID || hasParams || hasResult == hasError {
		return standardRPC{}, protocolError(ErrorMalformedFrame, true, "invalid lifecycle response shape")
	}
	if hasResult {
		rpc.Kind = standardRPCResponse
		rpc.Result = append(json.RawMessage(nil), result...)
		return rpc, nil
	}
	var rpcError codexwire.RPCError
	if err := decodeRequiredObject(errorRaw, &rpcError, "code", "message"); err != nil {
		return standardRPC{}, protocolError(ErrorMalformedFrame, true, "invalid lifecycle error response")
	}
	if err := validateText("lifecycle RPC error message", rpcError.Message, maxProtocolTextBytes); err != nil {
		return standardRPC{}, err
	}
	rpc.Kind = standardRPCError
	rpc.Error = &rpcError
	return rpc, nil
}

func validateRPCID(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return protocolError(ErrorMalformedFrame, true, "decode RPC id: %v", err)
	}
	switch typed := value.(type) {
	case string:
		return validateText("RPC id", typed, 256)
	case json.Number:
		if _, err := strconv.ParseInt(typed.String(), 10, 64); err != nil {
			return protocolError(ErrorMalformedFrame, true, "numeric RPC id is outside int64")
		}
		return nil
	default:
		return protocolError(ErrorMalformedFrame, true, "RPC id must be a string or signed int64")
	}
}

func validateLimits(limits Limits) error {
	if limits.MaxFrameBytes < 1 || limits.MaxJSONValues < 1 || limits.MaxJSONDepth < 1 || limits.MaxJSONDepth > maxProtocolDepth {
		return fmt.Errorf("agentx protocol limits must be positive and depth must not exceed %d", maxProtocolDepth)
	}
	return nil
}

func validateUUID(name, value string) error {
	if value == "00000000-0000-0000-0000-000000000000" || !uuidPattern.MatchString(value) {
		return protocolError(ErrorMalformedFrame, true, "%s must be a non-zero canonical lowercase UUID", name)
	}
	return nil
}

func validateDigest(name, value string) error {
	if !digestPattern.MatchString(value) {
		return protocolError(ErrorMalformedFrame, true, "%s must be lowercase SHA-256 hex", name)
	}
	return nil
}

func validateText(name, value string, maximum int) error {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return protocolError(ErrorMalformedFrame, true, "%s must contain between 1 and %d valid UTF-8 bytes without NUL", name, maximum)
	}
	return nil
}

func knownErrorCode(code ErrorCode) bool {
	switch code {
	case ErrorMalformedFrame, ErrorProtocolVersionUnsupported, ErrorMethodNotNegotiated,
		ErrorSessionMismatch, ErrorStaleGeneration, ErrorAckOutOfRange,
		ErrorAckRegression, ErrorSequenceConflict, ErrorResumeGap, ErrorOutputGap, ErrorBufferOverflow,
		ErrorResumeRejected, ErrorResumeExpired, ErrorJournalFull,
		ErrorMutationConflict, ErrorSessionClosed, ErrorAmbiguous:
		return true
	default:
		return false
	}
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("more than one JSON value")
		}
		return err
	}
	return nil
}

func validateJSONDocument(raw []byte, maxValues, maxDepth int) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	values := 0
	if err := validateJSONValue(decoder, &values, maxValues, 0, maxDepth); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains more than one value")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, values *int, maxValues, depth, maxDepth int) error {
	(*values)++
	if *values > maxValues {
		return fmt.Errorf("JSON exceeds %d values", maxValues)
	}
	if depth > maxDepth {
		return fmt.Errorf("JSON nesting exceeds %d", maxDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode JSON key: %w", err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder, values, maxValues, depth+1, maxDepth); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("JSON object did not terminate")
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder, values, maxValues, depth+1, maxDepth); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("JSON array did not terminate")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}
