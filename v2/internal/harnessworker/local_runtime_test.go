package harnessworker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

func TestRenderCodexConfigContainsOnlyFrozenModelRouteAndDisabledLocalTools(t *testing.T) {
	catalog := runnerTestCatalog(t)
	manifest, _, _ := oneShotSignedManifest(t, catalog, []byte(oneShotPrompt))
	raw, err := renderCodexConfig(CodexConfigProfileStable0146, manifest)
	if err != nil {
		t.Fatal(err)
	}
	config := string(raw)
	for _, wanted := range []string{
		`model = "gpt-5"`,
		`model_provider = "llmproxy"`,
		`base_url = "https://llmproxy.agentserver.test/v1"`,
		`env_key = "` + AppServerModelCapabilityEnvironment + `"`,
		`approval_policy = "never"`,
		`[tools.update_plan]`,
		`[tools.experimental_request_user_input]`,
		`shell_tool = false`,
		`unified_exec = false`,
		`multi_agent = false`,
	} {
		if !strings.Contains(config, wanted) {
			t.Errorf("rendered Codex config omits %q", wanted)
		}
	}
	for _, forbidden := range []string{
		manifest.ExecutorMCP.Endpoint,
		manifest.ExecutorMCP.TLSIdentity,
		"mcp_servers",
		"exec_command",
	} {
		if strings.Contains(config, forbidden) {
			t.Errorf("rendered Codex config contains forbidden executor authority %q", forbidden)
		}
	}
}

func TestLocalWorkerRuntimePreparerRejectsProfileAndDigestDrift(t *testing.T) {
	attemptRoot := t.TempDir()
	if err := os.Chmod(attemptRoot, 0o701); err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	config := LocalWorkerRuntimePreparerConfig{
		AttemptRoot: attemptRoot, RuntimeManifest: localRuntimeTestManifest(),
		RuntimeManifestSHA256: strings.Repeat("a", 64),
		VerifiedRuntime: runtimelock.VerifiedRuntime{Codex: runtimelock.VerifiedFile{
			Path: filepath.Join(artifactRoot, "codex"), SHA256: strings.Repeat("b", 64), SizeBytes: 1024,
		}},
		FinalExec: runtimelock.VerifiedFile{
			Path: filepath.Join(artifactRoot, "harness-final-exec"), SHA256: strings.Repeat("c", 64), SizeBytes: 2048,
		},
		CodexConfigProfile:     CodexConfigProfileStable0146,
		TLSRootCertificateFile: filepath.Join(artifactRoot, "ca.crt"),
		WorkerUID:              65531, WorkerGID: 65531, AppUID: 65532, AppGID: 65532,
	}
	if _, err := NewLocalWorkerRuntimePreparer(config); err != nil {
		t.Fatalf("valid local runtime preparer config: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*LocalWorkerRuntimePreparerConfig)
		want   string
	}{
		{name: "uppercase manifest digest", mutate: func(c *LocalWorkerRuntimePreparerConfig) {
			c.RuntimeManifestSHA256 = strings.Repeat("A", 64)
		}, want: "canonical lowercase"},
		{name: "relative Codex artifact", mutate: func(c *LocalWorkerRuntimePreparerConfig) {
			c.VerifiedRuntime.Codex.Path = "codex"
		}, want: "stock Codex artifact"},
		{name: "invalid final exec digest", mutate: func(c *LocalWorkerRuntimePreparerConfig) {
			c.FinalExec.SHA256 = "not-a-digest"
		}, want: "harness-final-exec artifact"},
		{name: "wrong stable profile", mutate: func(c *LocalWorkerRuntimePreparerConfig) {
			c.CodexConfigProfile = "future"
		}, want: "stable stock Codex 0.146.0"},
		{name: "relative TLS root", mutate: func(c *LocalWorkerRuntimePreparerConfig) {
			c.TLSRootCertificateFile = "ca.crt"
		}, want: "TLS root"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := config
			test.mutate(&candidate)
			if _, err := NewLocalWorkerRuntimePreparer(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewLocalWorkerRuntimePreparer() error = %v, want %q", err, test.want)
			}
		})
	}
}

func localRuntimeTestManifest() runtimelock.Manifest {
	return runtimelock.Manifest{
		ManifestVersion:                runtimelock.CurrentManifestVersion,
		CodexRelease:                   "0.146.0",
		CodexCommit:                    strings.Repeat("d", 40),
		AppServerSchemaSHA256:          strings.Repeat("e", 64),
		AppServerSchemaDigestAlgorithm: runtimelock.AppServerSchemaDigestAlgorithmV1,
		ExecProtocolSourceSHA256:       strings.Repeat("f", 64),
		ExecServerBounds: runtimelock.ExecServerBounds{
			MaxStdioFrameBytes: 64 * 1024 * 1024, MaxJSONValues: 256 * 1024,
			ArgvEnvLimit:                  runtimelock.ArgvEnvLimitTransportAndPlatformOnly,
			RetainedOutputBytesPerProcess: 1024 * 1024, RetainedOutputChunksPerProcess: 50_000,
			RetainedStdinWriteIDsPerProcess: 4096, ExitedProcessRetentionMilliseconds: 30_000,
		},
		AgentxLimits: runtimelock.AgentxLimits{
			MaxFrameBytes: 8 * 1024 * 1024, MaxJSONValues: 64 * 1024,
			MaxArgvElements: 256, MaxArgvBytes: 16 * 1024,
			MaxEnvVariables: 256, MaxEnvBytes: 16 * 1024,
			MaxWriteIDBytes: 128, MaxOutputBufferBytesPerProcess: 8 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: 1,
		AgentxProtocolVersion:      "2.0",
		Artifacts: map[string]runtimelock.PlatformArtifacts{
			"linux-amd64": {
				Codex: runtimelock.FileArtifact{
					Path: "bin/codex", SourceURL: "https://example.test/codex-0.146.0",
					SHA256: strings.Repeat("1", 64), SizeBytes: 1024,
				},
				ExternalExecutables: map[string]runtimelock.FileArtifact{},
			},
		},
	}
}
