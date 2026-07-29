package runtimelock

import (
	"strings"
	"testing"
)

func TestAgentxLimitsValidateProcessStartBoundaries(t *testing.T) {
	limits := validManifest().AgentxLimits
	argvAtLimit := []string{strings.Repeat("a", int(limits.MaxArgvBytes))}
	envValueAtLimit := strings.Repeat("e", int(limits.MaxEnvBytes)-len("BOUND="))
	if err := limits.ValidateProcessStart(argvAtLimit, nil, map[string]string{"BOUND": envValueAtLimit}); err != nil {
		t.Fatalf("exact process/start bounds rejected: %v", err)
	}

	tests := []struct {
		name    string
		argv    []string
		arg0    *string
		env     map[string]string
		wantErr string
	}{
		{name: "empty argv", argv: nil, env: map[string]string{}, wantErr: "must not be empty"},
		{name: "argv elements", argv: make([]string, limits.MaxArgvElements+1), env: map[string]string{}, wantErr: "elements"},
		{name: "argv bytes", argv: []string{strings.Repeat("a", int(limits.MaxArgvBytes)+1)}, env: map[string]string{}, wantErr: "argv exceeds"},
		{name: "argv NUL", argv: []string{"bad\x00argument"}, env: map[string]string{}, wantErr: "NUL"},
		{name: "arg0 elements", argv: make([]string, limits.MaxArgvElements), arg0: stringPointer("extra"), env: map[string]string{}, wantErr: "elements"},
		{name: "arg0 bytes", argv: []string{"command"}, arg0: stringPointer(strings.Repeat("a", int(limits.MaxArgvBytes))), env: map[string]string{}, wantErr: "arg0 exceed"},
		{name: "arg0 NUL", argv: []string{"command"}, arg0: stringPointer("bad\x00arg0"), env: map[string]string{}, wantErr: "NUL"},
		{name: "env variables", argv: []string{"command"}, env: environmentWithCount(limits.MaxEnvVariables + 1), wantErr: "variables"},
		{name: "env bytes", argv: []string{"command"}, env: map[string]string{"BOUND": strings.Repeat("e", int(limits.MaxEnvBytes)-len("BOUND=")+1)}, wantErr: "environment exceeds"},
		{name: "empty env name", argv: []string{"command"}, env: map[string]string{"": "value"}, wantErr: "invalid environment"},
		{name: "env equals", argv: []string{"command"}, env: map[string]string{"BAD=NAME": "value"}, wantErr: "invalid environment"},
		{name: "env NUL", argv: []string{"command"}, env: map[string]string{"NAME": "bad\x00value"}, wantErr: "invalid environment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := limits.ValidateProcessStart(test.argv, test.arg0, test.env)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateProcessStart() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestAgentxLimitsValidateWriteID(t *testing.T) {
	limits := validManifest().AgentxLimits
	if err := limits.ValidateWriteID(strings.Repeat("w", limits.MaxWriteIDBytes)); err != nil {
		t.Fatalf("exact writeId bound rejected: %v", err)
	}
	for _, writeID := range []string{"", "bad\x00id", strings.Repeat("w", limits.MaxWriteIDBytes+1)} {
		if err := limits.ValidateWriteID(writeID); err == nil {
			t.Fatalf("ValidateWriteID(%q) succeeded, want error", writeID)
		}
	}
}

func environmentWithCount(count int) map[string]string {
	environment := make(map[string]string, count)
	for index := 0; index < count; index++ {
		environment[string(rune(0x1000+index))] = ""
	}
	return environment
}
