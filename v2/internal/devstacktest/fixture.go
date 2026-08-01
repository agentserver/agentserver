// Package devstacktest creates complete on-disk development bundles for
// cross-command contract tests. It is not imported by runtime code.
package devstacktest

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/devstack"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

type Fixture struct {
	Root     string
	Output   string
	Config   devstack.ConfigDocument
	Prepared devstack.Result
}

func Prepare(root string) (Fixture, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Fixture{}, err
	}
	workspace := filepath.Join(resolved, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o700); err != nil {
		return Fixture{}, err
	}
	bundle := filepath.Join(resolved, "runtime-bundle")
	if err := os.MkdirAll(filepath.Join(bundle, "bin"), 0o700); err != nil {
		return Fixture{}, err
	}
	codexPath := filepath.Join(bundle, "bin", "codex")
	if err := os.WriteFile(codexPath, []byte("pinned-stock-codex-0.146.0"), 0o500); err != nil {
		return Fixture{}, err
	}
	codexDigest, codexSize, err := runtimelock.HashFile(codexPath)
	if err != nil {
		return Fixture{}, err
	}
	manifestBytes, err := json.Marshal(runtimeManifest(codexDigest, codexSize))
	if err != nil {
		return Fixture{}, err
	}
	manifestPath := filepath.Join(resolved, "runtime-manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o400); err != nil {
		return Fixture{}, err
	}
	binary := func(name string) (string, error) {
		path := filepath.Join(resolved, name)
		if err := os.WriteFile(path, []byte("test-binary-"+name), 0o500); err != nil {
			return "", err
		}
		return path, nil
	}
	agentx, err := binary("agentx")
	if err != nil {
		return Fixture{}, err
	}
	worker, err := binary("harness-worker")
	if err != nil {
		return Fixture{}, err
	}
	finalExec, err := binary("harness-final-exec")
	if err != nil {
		return Fixture{}, err
	}
	document := devstack.ConfigDocument{
		Version:     devstack.CurrentConfigVersion,
		DatabaseURL: "postgres://agentserver:development@127.0.0.1:5432/agentserver?sslmode=disable",
		Authority: devstack.AuthorityDocument{
			WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: "50000000-0000-4000-8000-000000000005",
			ActorID: "10000000-0000-4000-8000-000000000001", ExecutorID: "20000000-0000-4000-8000-000000000002",
			EnvironmentID: "60000000-0000-4000-8000-000000000006", AgentxVersion: "0.1.0-dev",
			WorkspaceRoot: workspace, DisplayName: "Local workspace", Description: "cross-loader fixture", DefaultCWD: "src",
		},
		Runtime: devstack.RuntimeDocument{
			ManifestFile: manifestPath, BundleRoot: bundle, AgentxBinary: agentx,
			HarnessWorkerBinary: worker, HarnessFinalExecBinary: finalExec,
		},
		Network: devstack.NetworkDocument{
			CoreListenAddress: "127.0.0.1:27443", BrowserGatewayListenAddress: "127.0.0.1:27444",
			ExecutorGatewayListenAddress: "127.0.0.1:27445", HarnessPoolListenAddress: "127.0.0.1:27446",
			HydraIntrospectionURL: "http://127.0.0.1:27447/oauth2/introspect", LLMProxyEndpoint: "https://127.0.0.1:27448/v1",
		},
		Model:  devstack.ModelDocument{Name: "gpt-5", Provider: "llmproxy"},
		Policy: devstack.PolicyDocument{Version: "dev-v1", AllowedTools: []string{"list_environments", "shell", "read_file"}},
		Harness: devstack.HarnessDocument{
			MaxConcurrentAttempts: 2, MaxRunDuration: "30m", MaxApprovalTTL: "10s",
		},
		Identities: devstack.IdentitiesDocument{WorkerUID: 65531, WorkerGID: 65531, AppUID: 65532, AppGID: 65532},
	}
	loaded, err := devstack.ValidateConfig(document)
	if err != nil {
		return Fixture{}, err
	}
	output := filepath.Join(resolved, "prepared-stack")
	prepared, err := devstack.Prepare(loaded, output, rand.Reader, time.Date(2026, time.July, 31, 9, 0, 0, 0, time.UTC))
	if err != nil {
		return Fixture{}, fmt.Errorf("prepare cross-loader development stack: %w", err)
	}
	return Fixture{Root: resolved, Output: output, Config: document, Prepared: prepared}, nil
}

func runtimeManifest(codexDigest string, codexSize int64) runtimelock.Manifest {
	return runtimelock.Manifest{
		ManifestVersion: runtimelock.CurrentManifestVersion, CodexRelease: "0.146.0",
		CodexCommit: strings.Repeat("a", 40), AppServerSchemaSHA256: strings.Repeat("b", 64),
		AppServerSchemaDigestAlgorithm: runtimelock.AppServerSchemaDigestAlgorithmV1,
		ExecProtocolSourceSHA256:       strings.Repeat("c", 64),
		ExecServerBounds: runtimelock.ExecServerBounds{
			MaxStdioFrameBytes: 64 * 1024 * 1024, MaxJSONValues: 256 * 1024,
			ArgvEnvLimit:                  runtimelock.ArgvEnvLimitTransportAndPlatformOnly,
			RetainedOutputBytesPerProcess: 1024 * 1024, RetainedOutputChunksPerProcess: 50_000,
			RetainedStdinWriteIDsPerProcess: 4096, ExitedProcessRetentionMilliseconds: 30_000,
		},
		AgentxLimits: runtimelock.AgentxLimits{
			MaxFrameBytes: 8 * 1024 * 1024, MaxJSONValues: 64 * 1024,
			MaxArgvElements: 256, MaxArgvBytes: 16 * 1024, MaxEnvVariables: 256, MaxEnvBytes: 16 * 1024,
			MaxWriteIDBytes: 128, MaxOutputBufferBytesPerProcess: 8 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: 1, AgentxProtocolVersion: "2.0",
		Artifacts: map[string]runtimelock.PlatformArtifacts{
			runtimelock.CurrentPlatform(): {
				Codex: runtimelock.FileArtifact{
					Path: "bin/codex", SourceURL: "https://example.test/codex/0.146.0/" + runtime.GOOS,
					SHA256: codexDigest, SizeBytes: codexSize,
				},
				ExternalExecutables: map[string]runtimelock.FileArtifact{},
			},
		},
	}
}
