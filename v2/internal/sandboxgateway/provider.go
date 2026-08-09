// Package sandboxgateway implements the provider-neutral control and data
// plane behind the unified execution gateway. Provider SDK types must remain
// behind the Provider interface.
package sandboxgateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

type ProviderSandboxState string

const (
	ProviderSandboxReady    ProviderSandboxState = "ready"
	ProviderSandboxCreating ProviderSandboxState = "creating"
	ProviderSandboxDeleting ProviderSandboxState = "deleting"
	ProviderSandboxDeleted  ProviderSandboxState = "deleted"
	ProviderSandboxFailed   ProviderSandboxState = "failed"
	ProviderSandboxUnknown  ProviderSandboxState = "unknown"
)

type ProviderSandbox struct {
	SessionRef string
	State      ProviderSandboxState
	Root       string
	ExpiresAt  time.Time
	RequestID  string
}

type CreateSandboxRequest struct {
	// SessionRef is empty when the provider assigns its own session identity.
	// It is retained for providers that support caller-assigned identities and
	// for adopting a previously observed create result.
	SessionRef           string
	SandboxID            string
	IdempotencyKey       string
	WorkspaceID          string
	SessionID            string
	EnvironmentID        string
	Region               string
	PSM                  string
	RuntimeProfileSHA256 string
	PackSetSHA256        string
	TTL                  time.Duration
}

// FindSandboxRequest identifies one prior create without relying on a
// caller-assigned provider session ID. Production providers must persist these
// values as non-secret provider metadata and perform an exact lookup. More than
// one match is an error: callers must never guess which sandbox to adopt.
type FindSandboxRequest struct {
	SandboxID            string
	IdempotencyKey       string
	WorkspaceID          string
	SessionID            string
	EnvironmentID        string
	Region               string
	PSM                  string
	RuntimeProfileSHA256 string
	PackSetSHA256        string
}

type SetSandboxTimeoutProviderRequest struct {
	SessionRef string
	TTL        time.Duration
}

// DeleteSandboxProviderRequest carries both the provider reference, when Core
// has durably observed it, and the immutable create identity. The latter is
// required to clean up a create whose request reached the provider but whose
// response was lost before provider_session_ref could be persisted. Providers
// must delete every exact live match in that recovery case; they must never
// broaden the lookup or guess between non-exact resources.
type DeleteSandboxProviderRequest struct {
	SessionRef string
	Identity   FindSandboxRequest
}

type StartProcessProviderRequest struct {
	SessionRef string
	Request    executionbackend.StartProcessRequest
}

type SignalProcessProviderRequest struct {
	SessionRef string
	Request    executionbackend.SignalProcessRequest
}

type ReadFileProviderRequest struct {
	SessionRef string
	Request    executionbackend.ReadFileRequest
}

type Provider interface {
	CreateSandbox(context.Context, CreateSandboxRequest) (ProviderSandbox, error)
	FindSandbox(context.Context, FindSandboxRequest) (ProviderSandbox, error)
	GetSandbox(context.Context, string) (ProviderSandbox, error)
	SetSandboxTimeout(context.Context, SetSandboxTimeoutProviderRequest) (ProviderSandbox, error)
	DeleteSandbox(context.Context, DeleteSandboxProviderRequest) error
	StartProcess(context.Context, StartProcessProviderRequest) (executionbackend.Exchange, error)
	SignalProcess(context.Context, SignalProcessProviderRequest) (executionbackend.Exchange, error)
	ReadFile(context.Context, ReadFileProviderRequest) (executionbackend.Exchange, error)
}

var ErrProviderSandboxNotFound = errors.New("provider sandbox not found")

type ProviderError struct {
	Code      string
	Ambiguous bool
	Cause     error
}

func (providerError *ProviderError) Error() string {
	if providerError == nil {
		return "<nil>"
	}
	message := "sandbox provider error"
	if providerError.Code != "" {
		message += ": " + providerError.Code
	}
	if providerError.Cause != nil {
		message += ": " + providerError.Cause.Error()
	}
	return message
}

func (providerError *ProviderError) Unwrap() error {
	if providerError == nil {
		return nil
	}
	return providerError.Cause
}

func validateProviderSandbox(sandbox ProviderSandbox) error {
	if sandbox.SessionRef == "" || len(sandbox.SessionRef) > 1024 {
		return errors.New("provider sandbox session reference is invalid")
	}
	switch sandbox.State {
	case ProviderSandboxReady:
		if sandbox.Root == "" || sandbox.ExpiresAt.IsZero() {
			return errors.New("ready provider sandbox is missing root or expiry")
		}
	case ProviderSandboxCreating, ProviderSandboxDeleting, ProviderSandboxDeleted, ProviderSandboxFailed, ProviderSandboxUnknown:
	default:
		return fmt.Errorf("unsupported provider sandbox state %q", sandbox.State)
	}
	return nil
}
