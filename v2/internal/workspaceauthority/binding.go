// Package workspaceauthority defines the executor-backed workspace authority
// shared by Core, run manifests, run capabilities, and executor-gateway.
package workspaceauthority

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxWorkingDirectoryBytes = 4096
	MaxRootDescriptorBytes   = 64 * 1024
	maxSafeJSONInteger       = int64(1<<53 - 1)
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Binding is immutable authority for one run's executor-backed workspace.
// RootSHA256 fingerprints the exact PostgreSQL jsonb text projection returned
// to executor-gateway; an absolute host root is deliberately not carried in
// the run authority document.
type Binding struct {
	EnvironmentID           string
	EnvironmentVersion      int64
	RootSHA256              [sha256.Size]byte
	WorkingDirectory        string
	WorkingDirectoryVersion int64
}

func (binding Binding) IsZero() bool {
	return binding == (Binding{})
}

func (binding Binding) Validate() error {
	if !validUUID(binding.EnvironmentID) {
		return errors.New("workspace environment ID must be a non-zero canonical lowercase UUID")
	}
	if binding.EnvironmentVersion < 1 || binding.EnvironmentVersion > maxSafeJSONInteger {
		return errors.New("workspace environment version must be a positive JSON-safe integer")
	}
	if binding.RootSHA256 == ([sha256.Size]byte{}) {
		return errors.New("workspace root descriptor SHA-256 is required")
	}
	if err := ValidateWorkingDirectory(binding.WorkingDirectory); err != nil {
		return err
	}
	if binding.WorkingDirectoryVersion < 1 || binding.WorkingDirectoryVersion > maxSafeJSONInteger {
		return errors.New("workspace working-directory version must be a positive JSON-safe integer")
	}
	return nil
}

// ValidateWorkingDirectory accepts only a clean, slash-separated path within
// an environment root. The single value "." denotes that root. Backslashes
// are rejected so the same authority cannot resolve differently on Windows.
func ValidateWorkingDirectory(value string) error {
	if value == "" || len(value) > MaxWorkingDirectoryBytes || !utf8.ValidString(value) {
		return fmt.Errorf("working directory must contain between 1 and %d canonical UTF-8 bytes", MaxWorkingDirectoryBytes)
	}
	if value == "." {
		return nil
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, `\`) || path.Clean(value) != value {
		return errors.New("working directory must be a clean slash-separated relative path")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("working directory must not contain empty, current, or parent segments")
		}
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return errors.New("working directory must not contain NUL or control characters")
		}
	}
	return nil
}

// RootDescriptorSHA256 validates and fingerprints the exact bounded JSON
// object projection used by both Core and executor-gateway.
func RootDescriptorSHA256(raw []byte) ([sha256.Size]byte, error) {
	if len(raw) < 2 || len(raw) > MaxRootDescriptorBytes {
		return [sha256.Size]byte{}, fmt.Errorf("root descriptor must contain between 2 and %d bytes", MaxRootDescriptorBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		if err == nil {
			err = errors.New("JSON root is not an object")
		}
		return [sha256.Size]byte{}, fmt.Errorf("decode root descriptor: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("additional JSON value")
		}
		return [sha256.Size]byte{}, fmt.Errorf("finish root descriptor: %w", err)
	}
	return sha256.Sum256(raw), nil
}

func validUUID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && uuidPattern.MatchString(value)
}
