package runtimelock

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// ArgvEnvLimitTransportAndPlatformOnly means stock exec-server has no
	// dedicated argv or environment size/count guard. Its JSON transport limits
	// admission, and the eventual host process API supplies a platform-specific
	// launch limit. A host E2BIG/ERROR_FILENAME_EXCED_RANGE result is not a
	// portable protocol bound.
	ArgvEnvLimitTransportAndPlatformOnly = "transport-and-platform-only"

	maxBoundFrameBytes       = 1024 * 1024 * 1024
	maxBoundJSONValues       = 4 * 1024 * 1024
	maxBoundRetainedBytes    = 1024 * 1024 * 1024
	maxBoundRetainedItems    = 4 * 1024 * 1024
	maxBoundRetentionMillis  = 10 * 60 * 1000
	maxBoundOutputBufferSize = 4 * 1024 * 1024 * 1024
)

// ExecServerBounds records release-bound behavior of the stock exec-server.
// These values are observations to re-probe on every upgrade, not product
// limits that an untrusted remote caller is allowed to consume directly.
type ExecServerBounds struct {
	MaxStdioFrameBytes                 int64  `json:"maxStdioFrameBytes"`
	MaxJSONValues                      int    `json:"maxJsonValues"`
	ArgvEnvLimit                       string `json:"argvEnvLimit"`
	RetainedOutputBytesPerProcess      int64  `json:"retainedOutputBytesPerProcess"`
	RetainedOutputChunksPerProcess     int    `json:"retainedOutputChunksPerProcess"`
	RetainedStdinWriteIDsPerProcess    int    `json:"retainedStdinWriteIdsPerProcess"`
	ExitedProcessRetentionMilliseconds int64  `json:"exitedProcessRetentionMilliseconds"`
}

// AgentxLimits is the smaller fail-closed product envelope enforced before a
// request reaches stock exec-server. MaxArgvElements and MaxArgvBytes cover
// argv plus an optional arg0 override. MaxEnvBytes is the sum of UTF-8 name +
// '=' + value bytes. Element-count limits independently cover empty strings.
// MaxOutputBufferBytesPerProcess is raw output retained by agentx for WSS
// delivery/resume, not stock process/read replay.
type AgentxLimits struct {
	MaxFrameBytes                  int64 `json:"maxFrameBytes"`
	MaxJSONValues                  int   `json:"maxJsonValues"`
	MaxArgvElements                int   `json:"maxArgvElements"`
	MaxArgvBytes                   int64 `json:"maxArgvBytes"`
	MaxEnvVariables                int   `json:"maxEnvVariables"`
	MaxEnvBytes                    int64 `json:"maxEnvBytes"`
	MaxWriteIDBytes                int   `json:"maxWriteIdBytes"`
	MaxOutputBufferBytesPerProcess int64 `json:"maxOutputBufferBytesPerProcess"`
}

func (b ExecServerBounds) validate() error {
	if b.MaxStdioFrameBytes < 1 || b.MaxStdioFrameBytes > maxBoundFrameBytes {
		return fmt.Errorf("execServerBounds.maxStdioFrameBytes must be between 1 and %d", maxBoundFrameBytes)
	}
	if b.MaxJSONValues < 1 || b.MaxJSONValues > maxBoundJSONValues {
		return fmt.Errorf("execServerBounds.maxJsonValues must be between 1 and %d", maxBoundJSONValues)
	}
	if b.ArgvEnvLimit != ArgvEnvLimitTransportAndPlatformOnly {
		return fmt.Errorf("execServerBounds.argvEnvLimit must be %q", ArgvEnvLimitTransportAndPlatformOnly)
	}
	if b.RetainedOutputBytesPerProcess < 1 || b.RetainedOutputBytesPerProcess > maxBoundRetainedBytes {
		return fmt.Errorf("execServerBounds.retainedOutputBytesPerProcess must be between 1 and %d", maxBoundRetainedBytes)
	}
	if b.RetainedOutputChunksPerProcess < 1 || b.RetainedOutputChunksPerProcess > maxBoundRetainedItems {
		return fmt.Errorf("execServerBounds.retainedOutputChunksPerProcess must be between 1 and %d", maxBoundRetainedItems)
	}
	if b.RetainedStdinWriteIDsPerProcess < 1 || b.RetainedStdinWriteIDsPerProcess > maxBoundRetainedItems {
		return fmt.Errorf("execServerBounds.retainedStdinWriteIdsPerProcess must be between 1 and %d", maxBoundRetainedItems)
	}
	if b.ExitedProcessRetentionMilliseconds < 1 || b.ExitedProcessRetentionMilliseconds > maxBoundRetentionMillis {
		return fmt.Errorf("execServerBounds.exitedProcessRetentionMilliseconds must be between 1 and %d", maxBoundRetentionMillis)
	}
	return nil
}

func (l AgentxLimits) validate(stock ExecServerBounds) error {
	if l.MaxFrameBytes < 1 || l.MaxFrameBytes > stock.MaxStdioFrameBytes {
		return errors.New("agentxLimits.maxFrameBytes must be positive and no greater than execServerBounds.maxStdioFrameBytes")
	}
	if l.MaxJSONValues < 1 || l.MaxJSONValues > stock.MaxJSONValues {
		return errors.New("agentxLimits.maxJsonValues must be positive and no greater than execServerBounds.maxJsonValues")
	}
	if l.MaxArgvElements < 1 || l.MaxArgvElements > l.MaxJSONValues {
		return errors.New("agentxLimits.maxArgvElements must be positive and no greater than agentxLimits.maxJsonValues")
	}
	if l.MaxArgvBytes < 1 || l.MaxArgvBytes > l.MaxFrameBytes {
		return errors.New("agentxLimits.maxArgvBytes must be positive and no greater than agentxLimits.maxFrameBytes")
	}
	if l.MaxEnvVariables < 1 || l.MaxEnvVariables > l.MaxJSONValues {
		return errors.New("agentxLimits.maxEnvVariables must be positive and no greater than agentxLimits.maxJsonValues")
	}
	if l.MaxEnvBytes < 1 || l.MaxEnvBytes > l.MaxFrameBytes {
		return errors.New("agentxLimits.maxEnvBytes must be positive and no greater than agentxLimits.maxFrameBytes")
	}
	if l.MaxWriteIDBytes < 1 || int64(l.MaxWriteIDBytes) > l.MaxFrameBytes {
		return errors.New("agentxLimits.maxWriteIdBytes must be positive and no greater than agentxLimits.maxFrameBytes")
	}
	if l.MaxOutputBufferBytesPerProcess < 1 || l.MaxOutputBufferBytesPerProcess > maxBoundOutputBufferSize {
		return fmt.Errorf("agentxLimits.maxOutputBufferBytesPerProcess must be between 1 and %d", int64(maxBoundOutputBufferSize))
	}
	return nil
}

// ValidateProcessStart applies the manifest's deterministic argv/environment
// product bounds. environment must be the final materialized child map after
// agentx has applied its non-inheriting allowlist; a raw envPolicy input is not
// sufficient. Agentx must additionally apply owner policy, path authorization,
// and platform-safe launch checks.
func (l AgentxLimits) ValidateProcessStart(argv []string, arg0 *string, environment map[string]string) error {
	if len(argv) == 0 {
		return errors.New("argv must not be empty")
	}
	argumentElements := len(argv)
	if arg0 != nil {
		argumentElements++
	}
	if argumentElements > l.MaxArgvElements {
		return fmt.Errorf("argv and arg0 contain %d elements, limit is %d", argumentElements, l.MaxArgvElements)
	}
	var argvBytes int64
	for _, argument := range argv {
		if strings.ContainsRune(argument, '\x00') {
			return errors.New("argv contains NUL")
		}
		if !addWithinLimit(&argvBytes, len(argument), l.MaxArgvBytes) {
			return fmt.Errorf("argv exceeds %d UTF-8 bytes", l.MaxArgvBytes)
		}
	}
	if arg0 != nil {
		if strings.ContainsRune(*arg0, '\x00') {
			return errors.New("arg0 contains NUL")
		}
		if !addWithinLimit(&argvBytes, len(*arg0), l.MaxArgvBytes) {
			return fmt.Errorf("argv and arg0 exceed %d UTF-8 bytes", l.MaxArgvBytes)
		}
	}

	if len(environment) > l.MaxEnvVariables {
		return fmt.Errorf("environment contains %d variables, limit is %d", len(environment), l.MaxEnvVariables)
	}
	var envBytes int64
	for name, value := range environment {
		if name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("invalid environment variable %q", name)
		}
		entryBytes := len(name) + 1 + len(value)
		if !addWithinLimit(&envBytes, entryBytes, l.MaxEnvBytes) {
			return fmt.Errorf("environment exceeds %d UTF-8 name/value bytes", l.MaxEnvBytes)
		}
	}
	return nil
}

func (l AgentxLimits) ValidateWriteID(writeID string) error {
	if writeID == "" {
		return errors.New("writeId must not be empty")
	}
	if strings.ContainsRune(writeID, '\x00') {
		return errors.New("writeId contains NUL")
	}
	if len(writeID) > l.MaxWriteIDBytes {
		return fmt.Errorf("writeId exceeds %d UTF-8 bytes", l.MaxWriteIDBytes)
	}
	return nil
}

func addWithinLimit(total *int64, itemBytes int, limit int64) bool {
	if itemBytes < 0 || int64(itemBytes) > limit-*total {
		return false
	}
	*total += int64(itemBytes)
	return true
}
