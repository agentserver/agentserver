// Package trajectorycursor issues authenticated backward-pagination positions
// for one creator-owned session trajectory. A cursor is not authorization:
// Core still rechecks workspace membership and session ownership on every read.
package trajectorycursor

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	cursorVersion      = 1
	tokenPrefix        = "v1."
	macDomain          = "agentserver-v2/session-trajectory-cursor/v1\x00"
	maximumPayloadSize = 2048
	maximumRecordID    = 512
	maximumSafeInteger = int64(1<<53 - 1)
)

type Scope struct {
	WorkspaceID string
	SessionID   string
	ActorID     string
}

type Position struct {
	Scope        Scope
	RunID        string
	RunCreatedAt time.Time
	AnchorSeq    int64
	Rank         int
	RecordID     string
}

type wirePosition struct {
	Version               int    `json:"v"`
	WorkspaceID           string `json:"w"`
	SessionID             string `json:"s"`
	ActorID               string `json:"a"`
	RunID                 string `json:"r"`
	RunCreatedAtUnixMicro int64  `json:"t"`
	AnchorSeq             int64  `json:"q"`
	Rank                  int    `json:"k"`
	RecordID              string `json:"i"`
}

type Codec struct {
	key []byte
}

func NewCodec(key []byte) (*Codec, error) {
	if len(key) < sha256.Size || len(key) > 1024 {
		return nil, errors.New("trajectory cursor key must contain between 32 and 1024 bytes")
	}
	return &Codec{key: append([]byte(nil), key...)}, nil
}

func (codec *Codec) Encode(position Position) (string, error) {
	if codec == nil || len(codec.key) < sha256.Size {
		return "", errors.New("trajectory cursor codec is not initialized")
	}
	if err := validatePosition(position); err != nil {
		return "", err
	}
	wire := wirePosition{
		Version: cursorVersion, WorkspaceID: position.Scope.WorkspaceID,
		SessionID: position.Scope.SessionID, ActorID: position.Scope.ActorID,
		RunID: position.RunID, RunCreatedAtUnixMicro: position.RunCreatedAt.UTC().UnixMicro(),
		AnchorSeq: position.AnchorSeq, Rank: position.Rank, RecordID: position.RecordID,
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("encode trajectory cursor: %w", err)
	}
	if len(payload) > maximumPayloadSize {
		return "", errors.New("trajectory cursor payload exceeds its size limit")
	}
	authenticated := append(append([]byte(nil), payload...), codec.mac(payload)...)
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(authenticated), nil
}

func (codec *Codec) Decode(token string, expected Scope) (Position, error) {
	position, err := codec.DecodePosition(token)
	if err != nil || position.Scope != expected {
		return Position{}, errors.New("invalid trajectory cursor")
	}
	return position, nil
}

func (codec *Codec) DecodePosition(token string) (Position, error) {
	if codec == nil || len(codec.key) < sha256.Size {
		return Position{}, errors.New("trajectory cursor codec is not initialized")
	}
	if !strings.HasPrefix(token, tokenPrefix) || len(token) > 4096 || strings.ContainsAny(token, "\x00\r\n") {
		return Position{}, errors.New("invalid trajectory cursor")
	}
	encoded := strings.TrimPrefix(token, tokenPrefix)
	authenticated, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(authenticated) <= sha256.Size || len(authenticated) > maximumPayloadSize+sha256.Size ||
		base64.RawURLEncoding.EncodeToString(authenticated) != encoded {
		return Position{}, errors.New("invalid trajectory cursor")
	}
	payload := authenticated[:len(authenticated)-sha256.Size]
	providedMAC := authenticated[len(authenticated)-sha256.Size:]
	if subtle.ConstantTimeCompare(providedMAC, codec.mac(payload)) != 1 {
		return Position{}, errors.New("invalid trajectory cursor")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var wire wirePosition
	if err := decoder.Decode(&wire); err != nil || wire.Version != cursorVersion {
		return Position{}, errors.New("invalid trajectory cursor")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Position{}, errors.New("invalid trajectory cursor")
	}
	position := Position{
		Scope: Scope{WorkspaceID: wire.WorkspaceID, SessionID: wire.SessionID, ActorID: wire.ActorID},
		RunID: wire.RunID, RunCreatedAt: time.UnixMicro(wire.RunCreatedAtUnixMicro).UTC(),
		AnchorSeq: wire.AnchorSeq, Rank: wire.Rank, RecordID: wire.RecordID,
	}
	if err := validatePosition(position); err != nil {
		return Position{}, errors.New("invalid trajectory cursor")
	}
	return position, nil
}

func (codec *Codec) mac(payload []byte) []byte {
	hasher := hmac.New(sha256.New, codec.key)
	_, _ = hasher.Write([]byte(macDomain))
	_, _ = hasher.Write(payload)
	return hasher.Sum(nil)
}

func validatePosition(position Position) error {
	for name, value := range map[string]string{
		"workspaceId": position.Scope.WorkspaceID,
		"sessionId":   position.Scope.SessionID,
		"actorId":     position.Scope.ActorID,
		"runId":       position.RunID,
	} {
		if err := validateUUID(name, value); err != nil {
			return err
		}
	}
	if position.RunCreatedAt.IsZero() || position.RunCreatedAt.UnixMicro() <= 0 {
		return errors.New("trajectory cursor run timestamp is invalid")
	}
	if position.AnchorSeq < 0 || position.AnchorSeq > maximumSafeInteger {
		return errors.New("trajectory cursor anchor sequence is invalid")
	}
	if position.Rank < 0 || position.Rank > 1024 {
		return errors.New("trajectory cursor rank is invalid")
	}
	if position.RecordID == "" || len(position.RecordID) > maximumRecordID ||
		!utf8.ValidString(position.RecordID) || strings.ContainsAny(position.RecordID, "\x00\r\n") {
		return errors.New("trajectory cursor record ID is invalid")
	}
	return nil
}

func validateUUID(field, value string) error {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || strings.ToLower(value) != value {
		return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
	}
	compact := value[0:8] + value[9:13] + value[14:18] + value[19:23] + value[24:36]
	var raw [16]byte
	if _, err := hex.Decode(raw[:], []byte(compact)); err != nil {
		return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
	}
	var nonzero byte
	for _, current := range raw {
		nonzero |= current
	}
	if nonzero == 0 {
		return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
	}
	return nil
}
