package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/taepolicy"
)

type taePolicyBindingsDocument struct {
	Bindings []taepolicy.Binding `json:"bindings"`
}

func parseTAEPolicyBindings(raw []byte, allowedPSM string) ([]taepolicy.Binding, error) {
	if len(raw) == 0 || len(raw) > 128*1024 {
		return nil, errors.New("TAE policy binding catalog must contain between 1 and 131072 bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document taePolicyBindingsDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode TAE policy binding catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("TAE policy binding catalog contains trailing data")
	}
	if len(document.Bindings) < 1 || len(document.Bindings) > len(managedsandboxprofile.Regions()) {
		return nil, errors.New("TAE policy binding catalog must contain between one and four bindings")
	}
	seen := make(map[string]struct{}, len(document.Bindings))
	for _, binding := range document.Bindings {
		if !managedsandboxprofile.ValidRegion(binding.Region) {
			return nil, fmt.Errorf("TAE policy binding region %q is invalid", binding.Region)
		}
		if _, duplicate := seen[binding.Region]; duplicate {
			return nil, fmt.Errorf("TAE policy binding region %q is repeated", binding.Region)
		}
		if err := binding.Validate(binding.Region, allowedPSM, larkegresspolicy.SHA256Hex()); err != nil {
			return nil, fmt.Errorf("TAE policy binding for %s: %w", binding.Region, err)
		}
		seen[binding.Region] = struct{}{}
	}
	return append([]taepolicy.Binding(nil), document.Bindings...), nil
}
