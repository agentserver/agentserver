// Package runevent defines the canonical, persistence-neutral run event
// contract consumed by browser-gateway. The contract deliberately contains no
// AG-UI, A2UI, or stock Codex wire types.
package runevent

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

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
)

const (
	CurrentSchemaVersion  = 1
	MaxEncodedEventBytes  = 96 * 1024
	MaxInlinePayloadBytes = 64 * 1024
	maxSafeJSONInteger    = int64(1<<53 - 1)
)

var (
	uuidPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	kindPattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type ObjectPointer struct {
	ObjectID  string `json:"objectId"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType"`
}

// Event is the exact canonical envelope returned by the core event cursor.
// RunAttemptID and RunAttemptGeneration are both null for run-scoped facts and
// both non-null for attempt-scoped facts.
type Event struct {
	EventID              string          `json:"eventId"`
	SchemaVersion        int             `json:"schemaVersion"`
	Seq                  int64           `json:"seq"`
	WorkspaceID          string          `json:"workspaceId"`
	SessionID            string          `json:"sessionId"`
	RunID                string          `json:"runId"`
	RunAttemptID         *string         `json:"runAttemptId"`
	RunAttemptGeneration *int64          `json:"runAttemptGeneration"`
	ProducerInstanceID   string          `json:"producerInstanceId"`
	ProducerSeq          int64           `json:"producerSeq"`
	Source               string          `json:"source"`
	Kind                 string          `json:"kind"`
	CreatedAt            time.Time       `json:"createdAt"`
	Payload              json.RawMessage `json:"payload,omitempty"`
	Object               *ObjectPointer  `json:"object,omitempty"`
}

// Decode accepts ordinary JSON serialization but rejects duplicate keys,
// unknown fields, excessive complexity, and every semantic ambiguity in the
// canonical envelope.
func Decode(raw []byte) (Event, error) {
	if len(raw) == 0 {
		return Event{}, errors.New("canonical run event is empty")
	}
	if len(raw) > MaxEncodedEventBytes {
		return Event{}, fmt.Errorf("canonical run event exceeds %d bytes", MaxEncodedEventBytes)
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 16 * 1024
	limits.MaxJSONDepth = 32
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, MaxEncodedEventBytes, limits); err != nil {
		return Event{}, fmt.Errorf("validate canonical run event JSON: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		return Event{}, fmt.Errorf("decode canonical run event: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return Event{}, fmt.Errorf("finish canonical run event: %w", err)
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	if event.RunAttemptID != nil {
		value := *event.RunAttemptID
		event.RunAttemptID = &value
	}
	if event.RunAttemptGeneration != nil {
		value := *event.RunAttemptGeneration
		event.RunAttemptGeneration = &value
	}
	if event.Object != nil {
		value := *event.Object
		event.Object = &value
	}
	return event, nil
}

func (event Event) Validate() error {
	identities := []struct {
		field string
		value string
	}{
		{"eventId", event.EventID},
		{"workspaceId", event.WorkspaceID},
		{"sessionId", event.SessionID},
		{"runId", event.RunID},
		{"producerInstanceId", event.ProducerInstanceID},
	}
	for _, identity := range identities {
		field, value := identity.field, identity.value
		if !uuidPattern.MatchString(value) || value == "00000000-0000-0000-0000-000000000000" {
			return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
		}
	}
	if event.SchemaVersion < 1 {
		return errors.New("schemaVersion must be positive")
	}
	if event.Seq < 1 || event.Seq > maxSafeJSONInteger {
		return fmt.Errorf("seq must be between 1 and %d", maxSafeJSONInteger)
	}
	if event.ProducerSeq < 1 || event.ProducerSeq > maxSafeJSONInteger {
		return fmt.Errorf("producerSeq must be between 1 and %d", maxSafeJSONInteger)
	}
	if (event.RunAttemptID == nil) != (event.RunAttemptGeneration == nil) {
		return errors.New("runAttemptId and runAttemptGeneration must either both be null or both be present")
	}
	if event.RunAttemptID != nil {
		if !uuidPattern.MatchString(*event.RunAttemptID) || *event.RunAttemptID == "00000000-0000-0000-0000-000000000000" {
			return errors.New("runAttemptId must be a non-zero canonical lowercase UUID")
		}
		if *event.RunAttemptGeneration < 1 || *event.RunAttemptGeneration > maxSafeJSONInteger {
			return fmt.Errorf("runAttemptGeneration must be between 1 and %d", maxSafeJSONInteger)
		}
	}
	switch event.Source {
	case "brain", "executor", "system", "approval":
	default:
		return fmt.Errorf("source %q is not supported", event.Source)
	}
	if len(event.Kind) > 128 || !kindPattern.MatchString(event.Kind) {
		return errors.New("kind must be a lowercase dotted canonical name of at most 128 bytes")
	}
	if event.CreatedAt.IsZero() {
		return errors.New("createdAt is required")
	}
	if (len(event.Payload) == 0) == (event.Object == nil) {
		return errors.New("canonical run event must contain exactly one of payload or object")
	}
	if event.Object != nil {
		return event.Object.validate()
	}
	if len(event.Payload) > MaxInlinePayloadBytes {
		return fmt.Errorf("inline payload exceeds %d bytes", MaxInlinePayloadBytes)
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 8192
	limits.MaxJSONDepth = 24
	value, _, err := braincatalog.DecodeCanonicalJSON(event.Payload, MaxInlinePayloadBytes, limits)
	if err != nil {
		return fmt.Errorf("validate inline payload: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New("inline payload must be a JSON object")
	}
	return nil
}

func (pointer ObjectPointer) validate() error {
	if !uuidPattern.MatchString(pointer.ObjectID) || pointer.ObjectID == "00000000-0000-0000-0000-000000000000" {
		return errors.New("object.objectId must be a non-zero canonical lowercase UUID")
	}
	if !digestPattern.MatchString(pointer.SHA256) {
		return errors.New("object.sha256 must be lowercase 64-character SHA-256 hex")
	}
	if pointer.Size < 1 || pointer.Size > maxSafeJSONInteger {
		return fmt.Errorf("object.size must be between 1 and %d", maxSafeJSONInteger)
	}
	if pointer.MediaType == "" || len(pointer.MediaType) > 255 || !utf8.ValidString(pointer.MediaType) || strings.ContainsAny(pointer.MediaType, "\r\n") {
		return errors.New("object.mediaType must be valid bounded text without line breaks")
	}
	return nil
}

func finishJSON(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected trailing JSON value")
	}
	return err
}
