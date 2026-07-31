package coredb

import (
	"errors"
	"fmt"
)

// StateErrorCode is a stable machine-readable domain command outcome.
type StateErrorCode string

const (
	ErrorInvalidArgument     StateErrorCode = "invalid_argument"
	ErrorForbidden           StateErrorCode = "forbidden"
	ErrorNotFound            StateErrorCode = "not_found"
	ErrorVersionConflict     StateErrorCode = "version_conflict"
	ErrorIdempotencyConflict StateErrorCode = "idempotency_conflict"
	ErrorActiveRun           StateErrorCode = "active_run"
	ErrorInvalidState        StateErrorCode = "invalid_state"
	ErrorLeaseHeld           StateErrorCode = "lease_held"
	ErrorLeaseLost           StateErrorCode = "lease_lost"
	ErrorConnectionFenced    StateErrorCode = "connection_fenced"
	ErrorEventConflict       StateErrorCode = "event_conflict"
	ErrorOutboxClaimLost     StateErrorCode = "outbox_claim_lost"
	ErrorConflict            StateErrorCode = "conflict"
	ErrorDatabase            StateErrorCode = "database_error"
)

// StateError describes a domain command rejection without exposing SQL or
// database credentials. The optional current fields support API conflict
// responses and retry decisions.
type StateError struct {
	Code              StateErrorCode
	Operation         string
	Resource          string
	ResourceID        string
	Message           string
	CurrentRunID      string
	CurrentVersion    int64
	CurrentGeneration int64
	cause             error
}

func (e *StateError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := string(e.Code)
	if e.Operation != "" {
		message = e.Operation + ": " + message
	}
	if e.Resource != "" {
		message += " " + e.Resource
		if e.ResourceID != "" {
			message += " " + e.ResourceID
		}
	}
	if e.Message != "" {
		message += ": " + e.Message
	}
	return message
}

func (e *StateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// HasStateErrorCode reports whether err contains a StateError with code.
func HasStateErrorCode(err error, code StateErrorCode) bool {
	var stateError *StateError
	return errors.As(err, &stateError) && stateError.Code == code
}

func commandError(code StateErrorCode, operation, resource, resourceID, message string) error {
	return &StateError{
		Code:       code,
		Operation:  operation,
		Resource:   resource,
		ResourceID: resourceID,
		Message:    message,
	}
}

func databaseError(operation string, err error) error {
	return &StateError{
		Code:      ErrorDatabase,
		Operation: operation,
		Message:   fmt.Sprintf("PostgreSQL command failed: %v", err),
		cause:     err,
	}
}
