package agentxconn

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
)

const (
	maxProcessArgvItems    = 256
	maxProcessStringRunes  = 16 * 1024
	maxProcessCWDRunes     = 4096
	maxProcessEnvVariables = 256
	maxProcessReadBytes    = 1024 * 1024
	maxProcessReadWaitMS   = 30_000
	maxProcessWriteIDRunes = 128
	maxSandboxEntries      = 64
	maxWorkspaceRoots      = 32
	maxNetworkHostRunes    = 253
	maxProcessTimeoutMS    = 3_600_000
	maxFilesystemReadLen   = 1024 * 1024
	maxFilesystemOffset    = 9_007_199_254_740_991
)

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type processStartParams struct {
	ProcessID             string            `json:"processId"`
	Argv                  []string          `json:"argv"`
	CWD                   string            `json:"cwd"`
	Env                   map[string]string `json:"env"`
	EnvPolicy             cleanEnvPolicy    `json:"envPolicy"`
	TTY                   *bool             `json:"tty"`
	PipeStdin             *bool             `json:"pipeStdin"`
	Arg0                  *string           `json:"arg0"`
	Sandbox               sandboxContext    `json:"sandbox"`
	EnforceManagedNetwork *bool             `json:"enforceManagedNetwork"`
}

type cleanEnvPolicy struct {
	Inherit               string            `json:"inherit"`
	IgnoreDefaultExcludes *bool             `json:"ignoreDefaultExcludes"`
	Exclude               []string          `json:"exclude"`
	Set                   map[string]string `json:"set"`
	IncludeOnly           []string          `json:"includeOnly"`
}

type sandboxContext struct {
	Permissions                  sandboxPermissions `json:"permissions"`
	CWD                          string             `json:"cwd"`
	WorkspaceRoots               []string           `json:"workspaceRoots"`
	WindowsSandboxLevel          string             `json:"windowsSandboxLevel"`
	WindowsSandboxPrivateDesktop *bool              `json:"windowsSandboxPrivateDesktop"`
	UseLegacyLandlock            *bool              `json:"useLegacyLandlock"`
}

type sandboxPermissions struct {
	Type       string            `json:"type"`
	FileSystem sandboxFileSystem `json:"file_system"`
	Network    string            `json:"network"`
}

type sandboxFileSystem struct {
	Type    string         `json:"type"`
	Entries []sandboxEntry `json:"entries"`
}

type sandboxEntry struct {
	Path   sandboxPath `json:"path"`
	Access string      `json:"access"`
}

type sandboxPath struct {
	Type  string          `json:"type"`
	Path  json.RawMessage `json:"path,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
}

type processReadParams struct {
	ProcessID string `json:"processId"`
	AfterSeq  uint64 `json:"afterSeq"`
	MaxBytes  uint64 `json:"maxBytes"`
	WaitMS    uint64 `json:"waitMs"`
}

type processWriteParams struct {
	ProcessID string `json:"processId"`
	Chunk     string `json:"chunk"`
	WriteID   string `json:"writeId"`
}

type processTerminateParams struct {
	ProcessID string `json:"processId"`
}

type filesystemReadFileBlockParams struct {
	Path   string `json:"path"`
	Offset uint64 `json:"offset"`
	Length uint64 `json:"len"`
}

type processOutputParams struct {
	ProcessID string `json:"processId"`
	Sequence  uint64 `json:"seq"`
	Stream    string `json:"stream"`
	Chunk     string `json:"chunk"`
}

type processExitedParams struct {
	ProcessID     string `json:"processId"`
	Sequence      uint64 `json:"seq"`
	ExitCode      int32  `json:"exitCode"`
	SandboxDenied *bool  `json:"sandboxDenied"`
}

type processClosedParams struct {
	ProcessID string `json:"processId"`
	Sequence  uint64 `json:"seq"`
}

type timeoutDueParams struct {
	ProcessID string `json:"processId"`
}

type networkPolicyRequestParams struct {
	ProcessID string               `json:"processId"`
	Request   networkPolicyRequest `json:"request"`
}

type networkPolicyRequest struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     uint16 `json:"port"`
}

// validateStockRPCEnvelope closes the gap between codexwire's deliberately
// forward-compatible parser and this versioned outer profile. Unknown stock
// envelope/error fields must not cross process-v1 merely because a future
// Codex build learned how to parse them.
func validateStockRPCEnvelope(rpc codexwire.Message) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rpc.Raw, &fields); err != nil || fields == nil {
		return protocolError(ErrorMalformedFrame, true, "decode stock RPC envelope: %v", err)
	}
	allowed := map[string]struct{}{}
	switch rpc.Kind {
	case codexwire.KindRequest:
		allowed = map[string]struct{}{"id": {}, "method": {}, "params": {}}
	case codexwire.KindNotification:
		allowed = map[string]struct{}{"method": {}, "params": {}}
	case codexwire.KindResponse:
		allowed = map[string]struct{}{"id": {}, "result": {}}
	case codexwire.KindError:
		allowed = map[string]struct{}{"id": {}, "error": {}}
	default:
		return protocolError(ErrorMalformedFrame, true, "unknown stock RPC kind %s", rpc.Kind)
	}
	for field := range fields {
		if _, ok := allowed[field]; !ok {
			return protocolError(ErrorMalformedFrame, true, "unknown stock RPC field %q", field)
		}
	}
	if len(rpc.ID) != 0 {
		if err := validateRPCID(rpc.ID); err != nil {
			return err
		}
	}
	if rpc.Kind != codexwire.KindError {
		return nil
	}
	var rpcError struct {
		Code    int64           `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data,omitempty"`
	}
	if err := decodeRequiredObject(fields["error"], &rpcError, "code", "message"); err != nil {
		return protocolError(ErrorMalformedFrame, true, "decode stock RPC error: %v", err)
	}
	if err := validateText("stock RPC error message", rpcError.Message, maxProtocolTextBytes); err != nil {
		return err
	}
	return nil
}

func validateProcessRequestParams(rpc codexwire.Message) error {
	switch rpc.Method {
	case execprofile.MethodProcessStart:
		var params processStartParams
		if err := decodeRequiredObjectAllowNull(rpc.Params, &params, map[string]struct{}{"arg0": {}},
			"processId", "argv", "cwd", "env", "envPolicy", "tty", "pipeStdin", "arg0", "sandbox", "enforceManagedNetwork"); err != nil {
			return malformedMethodParams(rpc.Method, err)
		}
		return params.validate()
	case execprofile.MethodProcessRead:
		var params processReadParams
		if err := decodeRequiredObject(rpc.Params, &params, "processId", "afterSeq", "maxBytes", "waitMs"); err != nil {
			return malformedMethodParams(rpc.Method, err)
		}
		if err := validateUUID("processId", params.ProcessID); err != nil {
			return err
		}
		if params.MaxBytes == 0 || params.MaxBytes > maxProcessReadBytes {
			return malformedMethodParams(rpc.Method, fmt.Errorf("maxBytes must be between 1 and %d", maxProcessReadBytes))
		}
		if params.WaitMS > maxProcessReadWaitMS {
			return malformedMethodParams(rpc.Method, fmt.Errorf("waitMs exceeds %d", maxProcessReadWaitMS))
		}
		return nil
	case execprofile.MethodProcessWrite:
		var params processWriteParams
		if err := decodeRequiredObject(rpc.Params, &params, "processId", "chunk", "writeId"); err != nil {
			return malformedMethodParams(rpc.Method, err)
		}
		if err := validateUUID("processId", params.ProcessID); err != nil {
			return err
		}
		if err := validateBase64("process/write chunk", params.Chunk, maxProcessReadBytes); err != nil {
			return malformedMethodParams(rpc.Method, err)
		}
		if err := validateWireString("writeId", params.WriteID, 1, maxProcessWriteIDRunes); err != nil {
			return malformedMethodParams(rpc.Method, err)
		}
		return nil
	case execprofile.MethodProcessTerminate:
		var params processTerminateParams
		if err := decodeRequiredObject(rpc.Params, &params, "processId"); err != nil {
			return malformedMethodParams(rpc.Method, err)
		}
		return validateUUID("processId", params.ProcessID)
	default:
		return protocolError(ErrorMethodNotNegotiated, true, "process request method %q is outside %s", rpc.Method, execprofile.Version)
	}
}

func validateFilesystemReadRequestParams(rpc codexwire.Message) error {
	if rpc.Method != execprofile.MethodFilesystemReadFileBlock {
		return protocolError(ErrorMethodNotNegotiated, true, "filesystem request method %q is outside %s", rpc.Method, execprofile.FilesystemReadVersion)
	}
	var params filesystemReadFileBlockParams
	if err := decodeRequiredObject(rpc.Params, &params, "path", "offset", "len"); err != nil {
		return malformedMethodParams(rpc.Method, err)
	}
	if err := validateFileURI("filesystem read path", params.Path); err != nil {
		return malformedMethodParams(rpc.Method, err)
	}
	if params.Offset > maxFilesystemOffset {
		return malformedMethodParams(rpc.Method, fmt.Errorf("offset exceeds %d", uint64(maxFilesystemOffset)))
	}
	if params.Length < 1 || params.Length > maxFilesystemReadLen {
		return malformedMethodParams(rpc.Method, fmt.Errorf("len must be between 1 and %d", maxFilesystemReadLen))
	}
	return nil
}

func validateProcessNotificationParams(rpc codexwire.Message) error {
	switch rpc.Method {
	case execprofile.NotificationProcessOutput:
		var params processOutputParams
		if err := decodeRequiredObject(rpc.Params, &params, "processId", "seq", "stream", "chunk"); err != nil {
			return malformedMethodParams(rpc.Method, err)
		}
		if err := validateUUID("processId", params.ProcessID); err != nil {
			return err
		}
		if params.Sequence == 0 {
			return malformedMethodParams(rpc.Method, fmt.Errorf("seq must be positive"))
		}
		if params.Stream != "stdout" && params.Stream != "stderr" && params.Stream != "pty" {
			return malformedMethodParams(rpc.Method, fmt.Errorf("stream %q is unsupported", params.Stream))
		}
		if err := validateBase64("process/output chunk", params.Chunk, -1); err != nil {
			return malformedMethodParams(rpc.Method, err)
		}
		return nil
	case execprofile.NotificationProcessExited:
		var params processExitedParams
		if err := decodeRequiredObject(rpc.Params, &params, "processId", "seq", "exitCode", "sandboxDenied"); err != nil {
			return malformedMethodParams(rpc.Method, err)
		}
		if err := validateUUID("processId", params.ProcessID); err != nil {
			return err
		}
		if params.Sequence == 0 || params.SandboxDenied == nil {
			return malformedMethodParams(rpc.Method, fmt.Errorf("seq must be positive and sandboxDenied must be a boolean"))
		}
		return nil
	case execprofile.NotificationProcessClosed:
		var params processClosedParams
		if err := decodeRequiredObject(rpc.Params, &params, "processId", "seq"); err != nil {
			return malformedMethodParams(rpc.Method, err)
		}
		if err := validateUUID("processId", params.ProcessID); err != nil {
			return err
		}
		if params.Sequence == 0 {
			return malformedMethodParams(rpc.Method, fmt.Errorf("seq must be positive"))
		}
		return nil
	default:
		return protocolError(ErrorMethodNotNegotiated, true, "process notification method %q is outside %s", rpc.Method, execprofile.Version)
	}
}

func validateTimeoutDueNotificationParams(rpc codexwire.Message) error {
	var params timeoutDueParams
	if err := decodeRequiredObject(rpc.Params, &params, "processId"); err != nil {
		return malformedMethodParams(rpc.Method, err)
	}
	return validateUUID("processId", params.ProcessID)
}

func validateNetworkPolicyRequestParams(rpc codexwire.Message) error {
	var params networkPolicyRequestParams
	if err := decodeRequiredObject(rpc.Params, &params, "processId", "request"); err != nil {
		return malformedMethodParams(rpc.Method, err)
	}
	if err := validateUUID("processId", params.ProcessID); err != nil {
		return err
	}
	if params.Request.Protocol != "http" && params.Request.Protocol != "https_connect" &&
		params.Request.Protocol != "socks5_tcp" && params.Request.Protocol != "socks5_udp" {
		return malformedMethodParams(rpc.Method, fmt.Errorf("protocol %q is unsupported", params.Request.Protocol))
	}
	if err := validateWireString("network host", params.Request.Host, 1, maxNetworkHostRunes); err != nil {
		return malformedMethodParams(rpc.Method, err)
	}
	for _, character := range params.Request.Host {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return malformedMethodParams(rpc.Method, fmt.Errorf("host contains control or whitespace characters"))
		}
	}
	if params.Request.Port == 0 {
		return malformedMethodParams(rpc.Method, fmt.Errorf("port must be between 1 and 65535"))
	}
	return nil
}

func (params processStartParams) validate() error {
	if err := validateUUID("processId", params.ProcessID); err != nil {
		return err
	}
	if len(params.Argv) == 0 || len(params.Argv) > maxProcessArgvItems {
		return malformedMethodParams(execprofile.MethodProcessStart, fmt.Errorf("argv must contain between 1 and %d entries", maxProcessArgvItems))
	}
	for index, argument := range params.Argv {
		if err := validateWireString(fmt.Sprintf("argv[%d]", index), argument, 0, maxProcessStringRunes); err != nil {
			return malformedMethodParams(execprofile.MethodProcessStart, err)
		}
	}
	if err := validateFileURI("cwd", params.CWD); err != nil {
		return malformedMethodParams(execprofile.MethodProcessStart, err)
	}
	if params.Env == nil || len(params.Env) > maxProcessEnvVariables {
		return malformedMethodParams(execprofile.MethodProcessStart, fmt.Errorf("env must be an object with at most %d entries", maxProcessEnvVariables))
	}
	for name, value := range params.Env {
		if !environmentNamePattern.MatchString(name) {
			return malformedMethodParams(execprofile.MethodProcessStart, fmt.Errorf("invalid environment variable name %q", name))
		}
		if err := validateWireString("environment value", value, 0, maxProcessStringRunes); err != nil {
			return malformedMethodParams(execprofile.MethodProcessStart, err)
		}
	}
	if err := params.EnvPolicy.validate(); err != nil {
		return malformedMethodParams(execprofile.MethodProcessStart, err)
	}
	if params.TTY == nil || params.PipeStdin == nil {
		return malformedMethodParams(execprofile.MethodProcessStart, fmt.Errorf("tty and pipeStdin must be booleans"))
	}
	if params.Arg0 != nil {
		if err := validateWireString("arg0", *params.Arg0, 0, maxProcessStringRunes); err != nil {
			return malformedMethodParams(execprofile.MethodProcessStart, err)
		}
	}
	if err := params.Sandbox.validate(); err != nil {
		return malformedMethodParams(execprofile.MethodProcessStart, err)
	}
	if params.EnforceManagedNetwork == nil || !*params.EnforceManagedNetwork {
		return malformedMethodParams(execprofile.MethodProcessStart, fmt.Errorf("enforceManagedNetwork must be true"))
	}
	return nil
}

func (policy cleanEnvPolicy) validate() error {
	if policy.Inherit != "none" {
		return fmt.Errorf("envPolicy.inherit must be none")
	}
	if policy.IgnoreDefaultExcludes == nil || *policy.IgnoreDefaultExcludes {
		return fmt.Errorf("envPolicy.ignoreDefaultExcludes must be false")
	}
	if policy.Exclude == nil || len(policy.Exclude) != 0 || policy.Set == nil || len(policy.Set) != 0 || policy.IncludeOnly == nil || len(policy.IncludeOnly) != 0 {
		return fmt.Errorf("envPolicy exclude, set, and includeOnly must be explicit and empty")
	}
	return nil
}

func (sandbox sandboxContext) validate() error {
	if sandbox.Permissions.Type != "managed" || sandbox.Permissions.FileSystem.Type != "restricted" || sandbox.Permissions.Network != "restricted" {
		return fmt.Errorf("sandbox must use managed restricted filesystem and network permissions")
	}
	if len(sandbox.Permissions.FileSystem.Entries) == 0 || len(sandbox.Permissions.FileSystem.Entries) > maxSandboxEntries {
		return fmt.Errorf("sandbox entries must contain between 1 and %d entries", maxSandboxEntries)
	}
	for index, entry := range sandbox.Permissions.FileSystem.Entries {
		if entry.Access != "read" && entry.Access != "write" {
			return fmt.Errorf("sandbox entry %d access %q is unsupported", index, entry.Access)
		}
		if err := entry.Path.validate(); err != nil {
			return fmt.Errorf("sandbox entry %d: %w", index, err)
		}
		if entry.Path.Type == "special" && entry.Access != "read" {
			return fmt.Errorf("sandbox entry %d: special minimal path must be read-only", index)
		}
	}
	if err := validateFileURI("sandbox cwd", sandbox.CWD); err != nil {
		return err
	}
	if len(sandbox.WorkspaceRoots) == 0 || len(sandbox.WorkspaceRoots) > maxWorkspaceRoots {
		return fmt.Errorf("workspaceRoots must contain between 1 and %d entries", maxWorkspaceRoots)
	}
	for _, root := range sandbox.WorkspaceRoots {
		if err := validateFileURI("workspace root", root); err != nil {
			return err
		}
	}
	if sandbox.WindowsSandboxLevel != "disabled" && sandbox.WindowsSandboxLevel != "restricted-token" && sandbox.WindowsSandboxLevel != "elevated" {
		return fmt.Errorf("windowsSandboxLevel %q is unsupported", sandbox.WindowsSandboxLevel)
	}
	if sandbox.WindowsSandboxPrivateDesktop == nil || sandbox.UseLegacyLandlock == nil {
		return fmt.Errorf("windowsSandboxPrivateDesktop and useLegacyLandlock must be booleans")
	}
	return nil
}

func (path sandboxPath) validate() error {
	switch path.Type {
	case "path":
		if len(path.Value) != 0 {
			return fmt.Errorf("path sandbox entry cannot carry value")
		}
		var uri string
		if len(path.Path) == 0 {
			return fmt.Errorf("path sandbox entry requires path")
		}
		if err := decodeStrict(path.Path, &uri); err != nil {
			return fmt.Errorf("decode sandbox path: %w", err)
		}
		return validateFileURI("sandbox path", uri)
	case "special":
		if len(path.Path) != 0 || len(path.Value) == 0 {
			return fmt.Errorf("special sandbox entry must be exactly minimal")
		}
		var value struct {
			Kind string `json:"kind"`
		}
		if err := decodeRequiredObject(path.Value, &value, "kind"); err != nil || value.Kind != "minimal" {
			return fmt.Errorf("special sandbox entry must be exactly minimal")
		}
		return nil
	default:
		return fmt.Errorf("sandbox path type %q is unsupported", path.Type)
	}
}

func decodeRequiredObject(raw json.RawMessage, destination any, required ...string) error {
	if err := requireObjectFields(raw, required...); err != nil {
		return err
	}
	return decodeStrict(raw, destination)
}

func decodeRequiredObjectAllowNull(raw json.RawMessage, destination any, nullable map[string]struct{}, required ...string) error {
	fields, err := requiredObjectFields(raw)
	if err != nil {
		return err
	}
	for _, field := range required {
		value, exists := fields[field]
		if !exists {
			return fmt.Errorf("required field %q is missing", field)
		}
		if isJSONNull(value) {
			if _, allowed := nullable[field]; !allowed {
				return fmt.Errorf("required field %q cannot be null", field)
			}
		}
	}
	return decodeStrict(raw, destination)
}

func requireObjectFields(raw json.RawMessage, required ...string) error {
	fields, err := requiredObjectFields(raw)
	if err != nil {
		return err
	}
	for _, field := range required {
		value, exists := fields[field]
		if !exists {
			return fmt.Errorf("required field %q is missing", field)
		}
		if isJSONNull(value) {
			return fmt.Errorf("required field %q cannot be null", field)
		}
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func requiredObjectFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("object is missing")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("value must be an object")
	}
	return fields, nil
}

func decodeRequiredArray(raw json.RawMessage, destination any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return fmt.Errorf("value must be an array")
	}
	return decodeStrict(raw, destination)
}

func malformedMethodParams(method string, cause error) error {
	return protocolError(ErrorMalformedFrame, true, "%s params: %v", method, cause)
}

func validateWireString(name, value string, minimum, maximum int) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s must be valid UTF-8 without NUL", name)
	}
	length := utf8.RuneCountInString(value)
	if length < minimum || length > maximum {
		return fmt.Errorf("%s must contain between %d and %d characters", name, minimum, maximum)
	}
	return nil
}

func validateFileURI(name, value string) error {
	if err := validateWireString(name, value, len("file:"), maxProcessCWDRunes); err != nil {
		return err
	}
	if !strings.HasPrefix(value, "file:") {
		return fmt.Errorf("%s must be a file URI", name)
	}
	return nil
}

func validateBase64(name, value string, maximumDecodedBytes int) error {
	if maximumDecodedBytes >= 0 && len(value) > base64.StdEncoding.EncodedLen(maximumDecodedBytes) {
		return fmt.Errorf("%s exceeds %d decoded bytes", name, maximumDecodedBytes)
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("%s is not canonical base64: %w", name, err)
	}
	if base64.StdEncoding.EncodeToString(decoded) != value {
		return fmt.Errorf("%s is not canonical base64", name)
	}
	if maximumDecodedBytes >= 0 && len(decoded) > maximumDecodedBytes {
		return fmt.Errorf("%s exceeds %d decoded bytes", name, maximumDecodedBytes)
	}
	return nil
}
