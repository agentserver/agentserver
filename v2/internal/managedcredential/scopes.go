package managedcredential

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

const ScopeBindingsEnvironment = "AGENTSERVER_V2_MANAGED_CREDENTIAL_SCOPE_BINDINGS"

// ScopeBinding is deployment-owned authority, never a model/workspace option.
type ScopeBinding struct {
	EnvironmentID string `json:"environmentId"`
	Scope         string `json:"scope"`
}

// ScopeBindings is immutable and deliberately has no default/fallback route.
type ScopeBindings struct{ byEnvironment map[string]string }

var scopeEnvironmentPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var scopeNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

func ParseScopeBindings(raw string) (*ScopeBindings, error) {
	if len(raw) == 0 || len(raw) > 16*1024 {
		return nil, errors.New("managed credential scope bindings are required and bounded")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	var bindings []ScopeBinding
	if err := decoder.Decode(&bindings); err != nil {
		return nil, errors.New("invalid managed credential scope bindings")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || len(bindings) < 1 || len(bindings) > 16 {
		return nil, errors.New("invalid managed credential scope bindings document")
	}
	result := &ScopeBindings{byEnvironment: make(map[string]string, len(bindings))}
	for _, binding := range bindings {
		if !scopeEnvironmentPattern.MatchString(binding.EnvironmentID) || binding.EnvironmentID == "00000000-0000-0000-0000-000000000000" || !scopeNamePattern.MatchString(binding.Scope) || result.byEnvironment[binding.EnvironmentID] != "" {
			return nil, errors.New("invalid or duplicate managed credential scope binding")
		}
		result.byEnvironment[binding.EnvironmentID] = binding.Scope
	}
	return result, nil
}

func (bindings *ScopeBindings) Scope(environmentID string) (string, bool) {
	if bindings == nil {
		return "", false
	}
	scope, ok := bindings.byEnvironment[environmentID]
	return scope, ok
}
