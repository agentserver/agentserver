package adapter

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/managedruntime"
)

// ControlSession is the deliberately small projection of a TAE Session used
// by the provider-neutral sandbox gateway. It must never contain credentials.
type ControlSession struct {
	ID              string
	Status          string
	ExpiresAt       time.Time
	Deleted         bool
	SandboxdEnabled bool
	Command         string
	Metadata        map[string]string
	RequestID       string
}

// RuntimeCommandConflicts reports an authoritative provider conflict. TAE's
// Session create/get/search responses do not consistently echo the command
// fixed by the Terminal Sandbox revision, so an empty value means "not
// reported" rather than "the process started without a command". Session
// creation deliberately cannot supply a command override.
func RuntimeCommandConflicts(command string) bool {
	return command != "" && command != managedruntime.ExecutablePath
}

func ValidTerminalIdentity(value string) bool {
	return sessionDNSLabelPattern.MatchString(value) && strings.ToLower(value) == value
}

type CreateInput struct {
	TTL      time.Duration
	Metadata map[string]string
}

type SearchInput struct {
	Metadata map[string]string
	Limit    int
}

type SearchResult struct {
	Sessions []ControlSession
	// Total is the provider-reported number of matches before pagination. It is
	// needed to prove that recovery cleanup has enumerated every exact match.
	Total int
}

// ControlPlane is implemented by the pinned official TAE SDK adapter. Keeping
// this interface narrow makes provider behaviour testable without a real TAE
// allocation and keeps SDK types out of the main module.
type ControlPlane interface {
	Create(context.Context, CreateInput) (ControlSession, error)
	Get(context.Context, string) (ControlSession, error)
	Search(context.Context, SearchInput) (SearchResult, error)
	UpdateTTL(context.Context, string, time.Duration) error
	Delete(context.Context, string) error
}

type StartProcessInput struct {
	RequestID        string
	Executable       string
	Arguments        []string
	WorkingDirectory string
	Environment      map[string]string
	Timeout          time.Duration
}

type StreamEvent struct {
	Name string
	Data map[string]any
}

type EventStream interface {
	Next(context.Context) (StreamEvent, error)
	RequestID() string
	Close() error
}

type FileInfo struct {
	Type          string
	Size          int64
	SymlinkTarget string
}

type Download struct {
	Body          io.ReadCloser
	ContentLength int64
	RequestID     string
}

// DataPlane is intentionally not implemented with the SDK's Terminal helper:
// SDK error logging may include the complete process request body, while that
// body contains a short-lived Lark placeholder. The production implementation
// below follows the documented HTTP/SSE contract and never logs payloads.
type DataPlane interface {
	StartProcess(context.Context, string, StartProcessInput) (EventStream, error)
	ConnectProcess(context.Context, string, int) (EventStream, error)
	SignalProcess(context.Context, string, int, int) (string, error)
	Stat(context.Context, string, string) (FileInfo, string, error)
	Download(context.Context, string, string) (Download, error)
}

var ErrSessionNotFound = errors.New("tae session not found")

// RequestError contains only bounded, non-secret provider observations. Cause
// must never include a request/response body, Authorization, or provider JWT.
type RequestError struct {
	WroteRequest bool
	StatusCode   int
	Code         string
	ProviderCode string
	RequestID    string
	Cause        error
}

func (requestError *RequestError) Error() string {
	if requestError == nil {
		return "<nil>"
	}
	message := "tae request failed"
	if requestError.Code != "" {
		message += ": " + requestError.Code
	}
	if requestError.StatusCode != 0 {
		message += ": http status " + statusText(requestError.StatusCode)
	}
	if requestError.ProviderCode != "" {
		message += ": provider code " + requestError.ProviderCode
	}
	if requestError.Cause != nil {
		message += ": " + requestError.Cause.Error()
	}
	return message
}

func (requestError *RequestError) Unwrap() error {
	if requestError == nil {
		return nil
	}
	return requestError.Cause
}

func statusText(status int) string {
	if status < 100 || status > 999 {
		return "invalid"
	}
	hundreds := status / 100
	tens := (status / 10) % 10
	ones := status % 10
	return string([]byte{byte('0' + hundreds), byte('0' + tens), byte('0' + ones)})
}
