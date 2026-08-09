package sandboxcontract

import (
	"errors"
	"fmt"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

type Limits struct {
	MinSandboxTTLSeconds    int64
	MaxSandboxTTLSeconds    int64
	MaxActivityTTLSeconds   int64
	MaxCommandTimeoutMillis int64
	MaxOutputBytes          int64
	MaxReadFileBytes        uint64
}

func DefaultLimits() Limits {
	return Limits{
		MinSandboxTTLSeconds:    30,
		MaxSandboxTTLSeconds:    24 * 60 * 60,
		MaxActivityTTLSeconds:   5 * 60,
		MaxCommandTimeoutMillis: 60 * 60 * 1000,
		MaxOutputBytes:          executionbackend.MaxOutputBytes,
		MaxReadFileBytes:        executionbackend.MaxReadFileBytes,
	}
}

func (limits Limits) Validate() error {
	if limits.MinSandboxTTLSeconds < 1 || limits.MaxSandboxTTLSeconds < limits.MinSandboxTTLSeconds {
		return errors.New("sandbox TTL limits are invalid")
	}
	if limits.MaxActivityTTLSeconds < 1 || limits.MaxActivityTTLSeconds > limits.MaxSandboxTTLSeconds {
		return errors.New("sandbox activity TTL limit is invalid")
	}
	if limits.MaxCommandTimeoutMillis < 1 || limits.MaxCommandTimeoutMillis > int64(executionbackend.MaxOperationTimeout.Milliseconds()) {
		return errors.New("sandbox command timeout limit is invalid")
	}
	if limits.MaxOutputBytes < 1 || limits.MaxOutputBytes > executionbackend.MaxOutputBytes {
		return fmt.Errorf("sandbox output limit must be between 1 and %d", executionbackend.MaxOutputBytes)
	}
	if limits.MaxReadFileBytes < 1 || limits.MaxReadFileBytes > executionbackend.MaxReadFileBytes {
		return fmt.Errorf("sandbox read-file limit must be between 1 and %d", executionbackend.MaxReadFileBytes)
	}
	return nil
}
