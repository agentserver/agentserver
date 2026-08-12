package executionbackend

import (
	"errors"
	"fmt"
)

type DispatchOutcome string

const (
	OutcomeNotSent  DispatchOutcome = "not_sent"
	OutcomeRejected DispatchOutcome = "rejected"
	OutcomeAccepted DispatchOutcome = "accepted"
	OutcomeUnknown  DispatchOutcome = "unknown"
)

func (outcome DispatchOutcome) Validate() error {
	switch outcome {
	case OutcomeNotSent, OutcomeRejected, OutcomeAccepted, OutcomeUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported dispatch outcome %q", outcome)
	}
}

// DispatchError records what is known about an attempted provider send. Cause
// and Code must already be safe for internal logs; provider bodies and headers
// containing credentials must not be retained here.
type DispatchError struct {
	Outcome           DispatchOutcome
	Code              string
	ProviderRequestID string
	ProviderCode      string
	HTTPStatus        int
	RequestWritten    *bool
	Cause             error
}

func NewDispatchError(outcome DispatchOutcome, code string, cause error) *DispatchError {
	return &DispatchError{Outcome: outcome, Code: code, Cause: cause}
}

func (dispatchError *DispatchError) Error() string {
	if dispatchError == nil {
		return "<nil>"
	}
	message := fmt.Sprintf("execution backend dispatch %s", dispatchError.Outcome)
	if dispatchError.Code != "" {
		message += ": " + dispatchError.Code
	}
	if dispatchError.Cause != nil {
		message += ": " + dispatchError.Cause.Error()
	}
	return message
}

func (dispatchError *DispatchError) Unwrap() error {
	if dispatchError == nil {
		return nil
	}
	return dispatchError.Cause
}

func (dispatchError *DispatchError) Validate() error {
	if dispatchError == nil {
		return errors.New("dispatch error is required")
	}
	if err := dispatchError.Outcome.Validate(); err != nil {
		return err
	}
	if dispatchError.Outcome == OutcomeAccepted {
		return errors.New("accepted dispatch cannot be represented as an error")
	}
	if !reasonCodePattern.MatchString(dispatchError.Code) {
		return fmt.Errorf("dispatch error code %q is invalid", dispatchError.Code)
	}
	if dispatchError.ProviderRequestID != "" {
		if err := validateText("dispatch provider request ID", dispatchError.ProviderRequestID, 1, 1024); err != nil {
			return err
		}
	}
	if dispatchError.ProviderCode != "" {
		if err := validateText("dispatch provider code", dispatchError.ProviderCode, 1, 128); err != nil {
			return err
		}
	}
	if dispatchError.ProviderRequestID != "" && containsUnsafeLogRune(dispatchError.ProviderRequestID) {
		return errors.New("dispatch provider request ID contains unsafe log characters")
	}
	if dispatchError.ProviderCode != "" && containsUnsafeLogRune(dispatchError.ProviderCode) {
		return errors.New("dispatch provider code contains unsafe log characters")
	}
	if dispatchError.HTTPStatus != 0 && (dispatchError.HTTPStatus < 100 || dispatchError.HTTPStatus > 599) {
		return fmt.Errorf("dispatch HTTP status %d is invalid", dispatchError.HTTPStatus)
	}
	if dispatchError.Cause == nil {
		return errors.New("dispatch error cause is required")
	}
	return nil
}

func containsUnsafeLogRune(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func OutcomeOf(err error) DispatchOutcome {
	var dispatchError *DispatchError
	if errors.As(err, &dispatchError) && dispatchError != nil {
		return dispatchError.Outcome
	}
	return OutcomeUnknown
}

// ProvesNotSent is deliberately narrow. It is the only provider error outcome
// that may be used as evidence that the external operation was not delivered.
func ProvesNotSent(err error) bool {
	return OutcomeOf(err) == OutcomeNotSent
}
