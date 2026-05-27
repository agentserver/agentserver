package main

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestParseEnvMcpArgs_HappyPath(t *testing.T) {
	args, err := parseEnvMcpArgs([]string{
		"--workspace-id", "ws_a",
		"--exec-gateway-url", "wss://exec-gw/bridge",
		"--app-gateway-internal", "http://127.0.0.1:8086",
		"--workspace-token-env", "CXG_WORKSPACE_TOKEN",
		"--loopback-token-env", "CXG_LOOPBACK_TOKEN",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if args.WorkspaceID != "ws_a" {
		t.Errorf("WorkspaceID = %q", args.WorkspaceID)
	}
	if args.ExecGatewayURL != "wss://exec-gw/bridge" {
		t.Errorf("ExecGatewayURL = %q", args.ExecGatewayURL)
	}
	if args.AppGatewayInternal != "http://127.0.0.1:8086" {
		t.Errorf("AppGatewayInternal = %q", args.AppGatewayInternal)
	}
	if args.WorkspaceTokenEnv != "CXG_WORKSPACE_TOKEN" {
		t.Errorf("WorkspaceTokenEnv = %q", args.WorkspaceTokenEnv)
	}
	if args.LoopbackTokenEnv != "CXG_LOOPBACK_TOKEN" {
		t.Errorf("LoopbackTokenEnv = %q", args.LoopbackTokenEnv)
	}
}

// TestParseEnvMcpArgs_MissingRequired sweeps the required-flag check.
func TestParseEnvMcpArgs_MissingRequired(t *testing.T) {
	full := map[string]string{
		"--workspace-id":         "w",
		"--exec-gateway-url":     "wss://x/bridge",
		"--app-gateway-internal": "http://127.0.0.1:8086",
		"--workspace-token-env":  "WT",
		"--loopback-token-env":   "LT",
	}
	for missing := range full {
		argv := make([]string, 0, len(full)*2)
		for k, v := range full {
			if k == missing {
				continue
			}
			argv = append(argv, k, v)
		}
		_, err := parseEnvMcpArgs(argv)
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("missing %s: want error naming the flag, got %v", missing, err)
		}
	}
}

func TestParseEnvMcpArgs_HelpFlag_ReturnsErrHelp(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{"--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("want flag.ErrHelp, got %v", err)
	}
}

func TestParseEnvMcpArgs_RejectsTrailingPositional(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{
		"--workspace-id", "w",
		"--exec-gateway-url", "wss://x/bridge",
		"--app-gateway-internal", "http://127.0.0.1:8086",
		"--workspace-token-env", "WT",
		"--loopback-token-env", "LT",
		"trailing",
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected positional") {
		t.Fatalf("want unexpected-positional error, got %v", err)
	}
}

func TestParseEnvMcpArgs_LogFile(t *testing.T) {
	args, err := parseEnvMcpArgs([]string{
		"--workspace-id", "w",
		"--exec-gateway-url", "wss://x/bridge",
		"--app-gateway-internal", "http://127.0.0.1:8086",
		"--workspace-token-env", "WT",
		"--loopback-token-env", "LT",
		"--log-file", "/tmp/env-mcp.log",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if args.LogFile != "/tmp/env-mcp.log" {
		t.Errorf("LogFile = %q", args.LogFile)
	}
}

func TestParseEnvMcpArgs_UnknownFlag_NoStderrLeak(t *testing.T) {
	_, err := parseEnvMcpArgs([]string{"--bogus", "x"})
	if err == nil {
		t.Fatal("want error on unknown flag")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should name the offending flag: %v", err)
	}
}
