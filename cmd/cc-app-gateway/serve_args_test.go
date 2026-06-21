package main

import (
	"strings"
	"testing"
)

func TestParseServeArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		wantAddr    string
		wantBinPath string
	}{
		{
			name:        "empty args uses defaults",
			args:        []string{},
			wantErr:     false,
			wantAddr:    ":8087",
			wantBinPath: "/usr/local/bin/claude",
		},
		{
			name:        "both flags override defaults",
			args:        []string{"--listen-addr", ":9000", "--claude-bin", "/opt/claude"},
			wantErr:     false,
			wantAddr:    ":9000",
			wantBinPath: "/opt/claude",
		},
		{
			name:        "equals form parsed",
			args:        []string{"--listen-addr=:9000"},
			wantErr:     false,
			wantAddr:    ":9000",
			wantBinPath: "/usr/local/bin/claude",
		},
		{
			name:    "unknown flag returns error",
			args:    []string{"--unknown-flag"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, err := parseServeArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseServeArgs() expected error, got nil")
					return
				}
				// Verify that error message names the unknown flag
				if !strings.Contains(err.Error(), "unknown-flag") {
					t.Errorf("parseServeArgs() error message does not contain 'unknown-flag': %s", err.Error())
				}
				return
			}
			if err != nil {
				t.Errorf("parseServeArgs() unexpected error: %v", err)
				return
			}
			if flags.ListenAddr != tt.wantAddr {
				t.Errorf("parseServeArgs() ListenAddr = %s, want %s", flags.ListenAddr, tt.wantAddr)
			}
			if flags.ClaudeBin != tt.wantBinPath {
				t.Errorf("parseServeArgs() ClaudeBin = %s, want %s", flags.ClaudeBin, tt.wantBinPath)
			}
		})
	}
}
