// Package runcursor issues authenticated opaque positions in one canonical
// run-event ledger. A cursor is a continuation capability, not authorization:
// core must still recheck the user and workspace on every read.
package runcursor

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	cursorVersion         = byte(1)
	cursorPayloadBytes    = 1 + 16 + 16 + 16 + 8
	cursorAuthenticated   = cursorPayloadBytes + sha256.Size
	maximumCursorSequence = int64(1<<53 - 2)
	tokenPrefix           = "v1."
	macDomain             = "agentserver-v2/run-event-cursor/v1\x00"
)

type Scope struct {
	WorkspaceID string
	SessionID   string
	RunID       string
}

type Position struct {
	Scope         Scope
	AfterSequence int64
}

type Codec struct {
	key []byte
}

func NewCodec(key []byte) (*Codec, error) {
	if len(key) < sha256.Size || len(key) > 1024 {
		return nil, errors.New("run cursor key must contain between 32 and 1024 bytes")
	}
	return &Codec{key: append([]byte(nil), key...)}, nil
}

func (codec *Codec) Encode(scope Scope, afterSequence int64) (string, error) {
	if codec == nil || len(codec.key) < sha256.Size {
		return "", errors.New("run cursor codec is not initialized")
	}
	if afterSequence < 0 || afterSequence > maximumCursorSequence {
		return "", fmt.Errorf("run cursor sequence must be between 0 and %d", maximumCursorSequence)
	}
	payload := make([]byte, cursorPayloadBytes)
	payload[0] = cursorVersion
	for _, field := range []struct {
		name   string
		value  string
		offset int
	}{
		{"workspaceId", scope.WorkspaceID, 1},
		{"sessionId", scope.SessionID, 17},
		{"runId", scope.RunID, 33},
	} {
		raw, err := decodeUUID(field.name, field.value)
		if err != nil {
			return "", err
		}
		copy(payload[field.offset:field.offset+16], raw[:])
	}
	binary.BigEndian.PutUint64(payload[49:57], uint64(afterSequence))

	authenticated := make([]byte, 0, cursorAuthenticated)
	authenticated = append(authenticated, payload...)
	authenticated = append(authenticated, codec.mac(payload)...)
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(authenticated), nil
}

// Decode verifies the MAC and exact workspace/session/run binding before
// returning the continuation sequence. All malformed, forged, or cross-scope
// tokens have the same public error class.
func (codec *Codec) Decode(token string, expected Scope) (int64, error) {
	position, err := codec.DecodePosition(token)
	if err != nil || position.Scope != expected {
		return 0, errors.New("invalid run event cursor")
	}
	return position.AfterSequence, nil
}

// DecodePosition authenticates a cursor before exposing its embedded scope.
// The caller must compare that scope with the URL and the authorized database
// result; this variant exists because the public event path does not contain a
// session ID.
func (codec *Codec) DecodePosition(token string) (Position, error) {
	if codec == nil || len(codec.key) < sha256.Size {
		return Position{}, errors.New("run cursor codec is not initialized")
	}
	if !strings.HasPrefix(token, tokenPrefix) || strings.ContainsAny(token, "\x00\r\n") {
		return Position{}, errors.New("invalid run event cursor")
	}
	encoded := strings.TrimPrefix(token, tokenPrefix)
	authenticated, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(authenticated) != cursorAuthenticated || base64.RawURLEncoding.EncodeToString(authenticated) != encoded {
		return Position{}, errors.New("invalid run event cursor")
	}
	payload := authenticated[:cursorPayloadBytes]
	providedMAC := authenticated[cursorPayloadBytes:]
	wantMAC := codec.mac(payload)
	if subtle.ConstantTimeCompare(providedMAC, wantMAC) != 1 || payload[0] != cursorVersion {
		return Position{}, errors.New("invalid run event cursor")
	}

	actual := Scope{
		WorkspaceID: encodeUUID(payload[1:17]),
		SessionID:   encodeUUID(payload[17:33]),
		RunID:       encodeUUID(payload[33:49]),
	}
	sequence := binary.BigEndian.Uint64(payload[49:57])
	if sequence > uint64(maximumCursorSequence) {
		return Position{}, errors.New("invalid run event cursor")
	}
	return Position{Scope: actual, AfterSequence: int64(sequence)}, nil
}

func (codec *Codec) mac(payload []byte) []byte {
	hasher := hmac.New(sha256.New, codec.key)
	_, _ = hasher.Write([]byte(macDomain))
	_, _ = hasher.Write(payload)
	return hasher.Sum(nil)
}

func decodeUUID(field, value string) ([16]byte, error) {
	var result [16]byte
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || strings.ToLower(value) != value {
		return result, fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
	}
	compact := value[0:8] + value[9:13] + value[14:18] + value[19:23] + value[24:36]
	if _, err := hex.Decode(result[:], []byte(compact)); err != nil {
		return result, fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
	}
	var nonzero byte
	for _, value := range result {
		nonzero |= value
	}
	if nonzero == 0 {
		return [16]byte{}, fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
	}
	return result, nil
}

func encodeUUID(raw []byte) string {
	var encoded [36]byte
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded[:])
}
