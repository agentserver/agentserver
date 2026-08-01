package browsergateway

import (
	"context"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/runevent"
)

type StartRunRequest struct {
	BearerToken    string
	WorkspaceID    string
	SessionID      string
	IdempotencyKey string
	ClientRunID    string
	Prompt         string
	ResumeCursor   string
}

type StartRunResult struct {
	WorkspaceID       string
	SessionID         string
	RunID             string
	CreatedAt         time.Time
	Cursor            string
	LastEventSequence int64
	RebaseSnapshot    any
}

type ReadRunEventsRequest struct {
	BearerToken string
	WorkspaceID string
	SessionID   string
	RunID       string
	After       string
	Limit       int
	Wait        time.Duration
}

type ReadRunEventsResult struct {
	Events       []runevent.Event
	EventCursors []string
	NextCursor   string
}

type CancelRunRequest struct {
	BearerToken string
	WorkspaceID string
	RunID       string
}

type CancelRunResult struct {
	WorkspaceID string
	SessionID   string
	RunID       string
	Status      string
	RunVersion  int64
	Terminal    bool
	Changed     bool
}

// RunBackend is the only authority-facing surface used by browser-gateway.
// Implementations create/idempotently recover a core run and long-poll only
// already committed canonical events. There is intentionally no CancelRun
// method on the SSE path: a browser disconnect only ends projection.
type RunBackend interface {
	StartRun(context.Context, StartRunRequest) (StartRunResult, error)
	ReadRunEvents(context.Context, ReadRunEventsRequest) (ReadRunEventsResult, error)
}

// RunCommandBackend is deliberately separate from the SSE backend contract:
// dropping a projection request never invokes CancelRun. Only the explicit
// authenticated command route may cross this boundary.
type RunCommandBackend interface {
	CancelRun(context.Context, CancelRunRequest) (CancelRunResult, error)
}

// CursorExpiredError carries an authorized state snapshot and a new canonical
// lifecycle-boundary cursor. It is returned by RunBackend, not accepted from a
// browser.
type CursorExpiredError struct {
	Snapshot          any
	RebaseCursor      string
	LastEventSequence int64
}

func (err *CursorExpiredError) Error() string {
	return "canonical run event cursor expired"
}

// BackendHTTPError lets a backend return a bounded public error before SSE
// starts, for example 409 active_run. Internal error details must stay in the
// wrapped/logged error rather than Message.
type BackendHTTPError struct {
	Status       int
	Code         string
	Message      string
	CurrentRunID string
	Err          error
}

func (err *BackendHTTPError) Error() string {
	if err.Err != nil {
		return fmt.Sprintf("%s: %v", err.Code, err.Err)
	}
	return err.Code
}

func (err *BackendHTTPError) Unwrap() error { return err.Err }
