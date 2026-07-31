// Package harnessbootstrap defines the one-shot, worker-only bootstrap sent
// over an inherited pipe by the local harness process launcher.
package harnessbootstrap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/ucarion/jcs"
)

const (
	CurrentVersion = 1
	MaximumBytes   = 2 << 20
)

type Envelope struct {
	Version           int
	SignedManifest    runmanifest.SignedManifest
	ControlCapability string
}

type wireEnvelope struct {
	Version           int             `json:"version"`
	SignedManifest    json.RawMessage `json:"signedManifest"`
	ControlCapability string          `json:"controlCapability"`
}

// Encode returns canonical JSON suitable for a single inherited pipe. The
// returned bytes contain a secret and must never be logged or persisted.
func Encode(envelope Envelope) ([]byte, error) {
	if envelope.Version != CurrentVersion {
		return nil, fmt.Errorf("harness bootstrap version must be %d", CurrentVersion)
	}
	if err := ValidateControlCapability(envelope.ControlCapability); err != nil {
		return nil, err
	}
	signed, err := json.Marshal(envelope.SignedManifest)
	if err != nil {
		return nil, errors.New("encode signed run manifest")
	}
	if _, err := runmanifest.ParseSigned(signed); err != nil {
		return nil, fmt.Errorf("validate signed run manifest: %w", err)
	}
	raw, err := json.Marshal(wireEnvelope{
		Version: envelope.Version, SignedManifest: signed,
		ControlCapability: envelope.ControlCapability,
	})
	if err != nil {
		return nil, errors.New("encode harness bootstrap")
	}
	value, _, err := braincatalog.DecodeCanonicalJSON(raw, MaximumBytes, bootstrapLimits())
	if err != nil {
		return nil, fmt.Errorf("validate harness bootstrap JSON: %w", err)
	}
	canonical, err := jcs.Append(nil, value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize harness bootstrap: %w", err)
	}
	if len(canonical) > MaximumBytes {
		return nil, fmt.Errorf("harness bootstrap exceeds %d bytes", MaximumBytes)
	}
	return canonical, nil
}

// Decode accepts only canonical, closed-world JSON. It validates the signed
// envelope shape, while signature trust is deliberately checked by the worker
// keyring after bootstrap.
func Decode(raw []byte) (Envelope, error) {
	if len(raw) == 0 {
		return Envelope{}, errors.New("harness bootstrap is empty")
	}
	if len(raw) > MaximumBytes {
		return Envelope{}, fmt.Errorf("harness bootstrap exceeds %d bytes", MaximumBytes)
	}
	_, canonical, err := braincatalog.DecodeCanonicalJSON(raw, MaximumBytes, bootstrapLimits())
	if err != nil {
		return Envelope{}, fmt.Errorf("validate harness bootstrap JSON: %w", err)
	}
	if !bytes.Equal(raw, canonical) {
		return Envelope{}, errors.New("harness bootstrap is not RFC 8785 canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire wireEnvelope
	if err := decoder.Decode(&wire); err != nil {
		return Envelope{}, fmt.Errorf("decode harness bootstrap: %w", err)
	}
	if err := finishJSON(decoder); err != nil {
		return Envelope{}, fmt.Errorf("finish harness bootstrap: %w", err)
	}
	if wire.Version != CurrentVersion {
		return Envelope{}, fmt.Errorf("harness bootstrap version must be %d", CurrentVersion)
	}
	if len(wire.SignedManifest) == 0 {
		return Envelope{}, errors.New("harness bootstrap signedManifest is required")
	}
	signed, err := runmanifest.ParseSigned(wire.SignedManifest)
	if err != nil {
		return Envelope{}, fmt.Errorf("parse signed run manifest: %w", err)
	}
	if err := ValidateControlCapability(wire.ControlCapability); err != nil {
		return Envelope{}, err
	}
	return Envelope{
		Version: wire.Version, SignedManifest: signed,
		ControlCapability: wire.ControlCapability,
	}, nil
}

// Read consumes one bootstrap through EOF with a hard size bound.
func Read(reader io.Reader) (Envelope, error) {
	if reader == nil {
		return Envelope{}, errors.New("harness bootstrap reader is required")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaximumBytes+1))
	if err != nil {
		return Envelope{}, fmt.Errorf("read harness bootstrap: %w", err)
	}
	return Decode(raw)
}

func ValidateControlCapability(value string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return errors.New("harness bootstrap control capability must be canonical 256-bit base64url")
	}
	return nil
}

func bootstrapLimits() braincatalog.Limits {
	limits := braincatalog.DefaultLimits()
	limits.MaxCatalogBytes = MaximumBytes
	limits.MaxSchemaBytes = MaximumBytes
	limits.MaxJSONValues += 32
	limits.MaxJSONDepth += 2
	return limits
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
