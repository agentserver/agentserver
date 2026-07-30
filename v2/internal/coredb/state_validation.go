package coredb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxInlineEventPayloadBytes = 64 * 1024
	MaxAttemptEventsPerAppend  = 256
	MaxOutboxClaimBatch        = 100
	MaxLeaseTTL                = time.Hour
	MaxOutboxRetryDelay        = 24 * time.Hour
)

var canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const zeroUUID = "00000000-0000-0000-0000-000000000000"

func validateUUID(field, value string) error {
	if value == zeroUUID || !canonicalUUIDPattern.MatchString(value) {
		return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
	}
	return nil
}

func validateBoundedText(field, value string, maximum int) error {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%s must contain between 1 and %d valid UTF-8 bytes without NUL", field, maximum)
	}
	return nil
}

func validateTransitionRecord(record TransitionRecord) error {
	if err := validateUUID("record.event_id", record.EventID); err != nil {
		return err
	}
	if err := validateUUID("record.producer_instance_id", record.ProducerInstanceID); err != nil {
		return err
	}
	if record.ProducerSeq < 1 {
		return errors.New("record.producer_seq must be positive")
	}
	if err := validateUUID("record.outbox_id", record.OutboxID); err != nil {
		return err
	}
	return nil
}

func durationMilliseconds(field string, duration, maximum time.Duration) (int64, error) {
	if duration < time.Millisecond || duration > maximum {
		return 0, fmt.Errorf("%s must be between 1ms and %s", field, maximum)
	}
	return duration.Milliseconds(), nil
}

func validateInlinePayload(payload json.RawMessage) error {
	if len(payload) == 0 {
		return errors.New("inline event payload is empty")
	}
	if len(payload) > MaxInlineEventPayloadBytes {
		return fmt.Errorf("inline event payload exceeds %d bytes", MaxInlineEventPayloadBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("inline event payload is not valid JSON: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New("inline event payload must be a JSON object")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("inline event payload contains more than one JSON value")
		}
		return fmt.Errorf("finish inline event payload: %w", err)
	}
	return nil
}

func validEventSource(source string) bool {
	switch source {
	case EventSourceBrain, EventSourceExecutor, EventSourceSystem, EventSourceApproval:
		return true
	default:
		return false
	}
}

func validAttemptEventStatus(status string) bool {
	switch status {
	case AttemptStatusLeased, AttemptStatusStarting, AttemptStatusRunning, AttemptStatusFinalizing:
		return true
	default:
		return false
	}
}

func validRunLeaseStatus(status string) bool {
	switch status {
	case RunStatusStarting, RunStatusRunning, RunStatusFinalizing, RunStatusCancelling:
		return true
	default:
		return false
	}
}
