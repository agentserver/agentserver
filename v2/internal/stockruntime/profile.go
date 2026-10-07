// Package stockruntime defines the exact stock Codex runtime release accepted
// by the agentserver v2 production profile. Development packaging, harness
// images, and the independently built agentx distribution must all consume
// this package or the byte-for-byte checked-in manifest derived from it.
package stockruntime

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	PlatformLinuxAMD64 = "linux-amd64"
	PlatformLinuxARM64 = "linux-arm64"

	CodexRelease = "0.160.1"
	CodexCommit  = "d27764b82f7118f674371e6d6e76271d9d606edb"

	AppServerSchemaSHA256 = "c43501a592607e11b183211dc587e7c4b28083edcc08e8e7248ea7219a2467ae"
	ExecProtocolSHA256    = "c25b5a4b610a9f3549344b2807697ca7fb65e87e0f68f5b89485652143da565b"
	ManifestSHA256        = "e06ad26948731ece3e225729d3793c211aa127419e693ace5431abb0a4b70862"
	ManifestSizeBytes     = int64(2429)

	CheckpointAllowlistVersion = 1
	AgentxProtocolVersion      = "2.0"

	LinuxARM64CodexSHA256 = "fbbaec80443919f86dd63648a0b62759cf6f1d0e09310602fde96885e0bceb3e"
	LinuxARM64CodexSize   = int64(248966648)
	LinuxARM64CodexURL    = "https://github.com/openai/codex/releases/download/rust-v0.160.1/codex-aarch64-unknown-linux-musl.tar.gz"

	LinuxARM64BwrapSHA256 = "c547cbdc762a70ed216789ffaa4c6c0e7d2beabe32245a498f8e365a9fc8dab4"
	LinuxARM64BwrapSize   = int64(529168)
	LinuxARM64BwrapURL    = "https://github.com/openai/codex/releases/download/rust-v0.160.1/bwrap-aarch64-unknown-linux-musl.tar.gz"

	LinuxAMD64CodexSHA256 = "f34a4d2301892ae96c90097786bfe5dc269f187b6f69faf42a7b357b8c081e35"
	LinuxAMD64CodexSize   = int64(289166920)
	LinuxAMD64CodexURL    = "https://github.com/openai/codex/releases/download/rust-v0.160.1/codex-x86_64-unknown-linux-musl.tar.gz"

	LinuxAMD64BwrapSHA256 = "77360cb751ccedc5971391444ac86a8a33c15b04d6b4a6fe45f5d25496e62c4c"
	LinuxAMD64BwrapSize   = int64(529776)
	LinuxAMD64BwrapURL    = "https://github.com/openai/codex/releases/download/rust-v0.160.1/bwrap-x86_64-unknown-linux-musl.tar.gz"
)

type ProtocolSourceFile struct {
	Path   string
	SHA256 string
}

// protocolSources is the reviewed production Rust source surface of the
// exec-server-protocol crate at CodexCommit. Test modules and build metadata
// are deliberately excluded. ExecProtocolSHA256 uses the same sorted
// "<sha256><two spaces><repo-relative path><LF>" record format as
// runtimelock.HashTree.
var protocolSources = []ProtocolSourceFile{
	{Path: "codex-rs/exec-server-protocol/src/capabilities.rs", SHA256: "f014ae5db1b4bebcde44eabe93760550305a837bc346c71407a5553b5bfc41d1"},
	{Path: "codex-rs/exec-server-protocol/src/environment_config.rs", SHA256: "c463584db6a07c20dc8bce25ca3886f159e7191371dda18a7636fdf6d1856c4e"},
	{Path: "codex-rs/exec-server-protocol/src/lib.rs", SHA256: "8e7d176b249846ad2a273e000087665b37483ade9dfcc67e94ae9e12224b8ef4"},
	{Path: "codex-rs/exec-server-protocol/src/network_policy.rs", SHA256: "95d3ed5ba880476e689a8fd383b1b16de7bfaa7575fb65779db0241d9758a4ef"},
	{Path: "codex-rs/exec-server-protocol/src/process_id.rs", SHA256: "e027b4e4ac3581a188727a1b1d479168ae9b73e4509d0f8fbf94a3dbb0d0203f"},
	{Path: "codex-rs/exec-server-protocol/src/protocol.rs", SHA256: "6ef5940f3fd348b63b331299c80426b3aedcfa503e01e956d3301139733ed9fe"},
	{Path: "codex-rs/exec-server-protocol/src/rpc.rs", SHA256: "67fdd002caa343def5d78d417c944851ef176add634d06410d945603307c3f7c"},
}

// ProtocolSources returns a defensive copy of the exact upstream source
// allowlist used to derive ExecProtocolSHA256.
func ProtocolSources() []ProtocolSourceFile {
	return append([]ProtocolSourceFile(nil), protocolSources...)
}

// ProductionManifest returns a fresh closed-world manifest for every packaged
// production architecture. Callers may mutate the returned maps without
// changing the release profile used by later calls.
func ProductionManifest() runtimelock.Manifest {
	return runtimelock.Manifest{
		ManifestVersion:                runtimelock.CurrentManifestVersion,
		CodexRelease:                   CodexRelease,
		CodexCommit:                    CodexCommit,
		AppServerSchemaSHA256:          AppServerSchemaSHA256,
		AppServerSchemaDigestAlgorithm: runtimelock.AppServerSchemaDigestAlgorithmV1,
		ExecProtocolSourceSHA256:       ExecProtocolSHA256,
		ExecServerBounds: runtimelock.ExecServerBounds{
			MaxStdioFrameBytes:                 64 * 1024 * 1024,
			MaxJSONValues:                      256 * 1024,
			ArgvEnvLimit:                       runtimelock.ArgvEnvLimitTransportAndPlatformOnly,
			RetainedOutputBytesPerProcess:      1024 * 1024,
			RetainedOutputChunksPerProcess:     50_000,
			RetainedStdinWriteIDsPerProcess:    4096,
			ExitedProcessRetentionMilliseconds: 30_000,
		},
		AgentxLimits: runtimelock.AgentxLimits{
			MaxFrameBytes:                  8 * 1024 * 1024,
			MaxJSONValues:                  64 * 1024,
			MaxArgvElements:                256,
			MaxArgvBytes:                   16 * 1024,
			MaxEnvVariables:                256,
			MaxEnvBytes:                    16 * 1024,
			MaxWriteIDBytes:                128,
			MaxOutputBufferBytesPerProcess: 8 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: CheckpointAllowlistVersion,
		AgentxProtocolVersion:      AgentxProtocolVersion,
		Artifacts: map[string]runtimelock.PlatformArtifacts{
			PlatformLinuxAMD64: {
				Codex: runtimelock.FileArtifact{
					Path: "bin/codex", SourceURL: LinuxAMD64CodexURL,
					SHA256: LinuxAMD64CodexSHA256, SizeBytes: LinuxAMD64CodexSize,
				},
				ExternalExecutables: map[string]runtimelock.FileArtifact{
					"bwrap": {
						Path: "codex-resources/bwrap", SourceURL: LinuxAMD64BwrapURL,
						SHA256: LinuxAMD64BwrapSHA256, SizeBytes: LinuxAMD64BwrapSize,
					},
				},
			},
			PlatformLinuxARM64: {
				Codex: runtimelock.FileArtifact{
					Path: "bin/codex", SourceURL: LinuxARM64CodexURL,
					SHA256: LinuxARM64CodexSHA256, SizeBytes: LinuxARM64CodexSize,
				},
				ExternalExecutables: map[string]runtimelock.FileArtifact{
					"bwrap": {
						Path: "codex-resources/bwrap", SourceURL: LinuxARM64BwrapURL,
						SHA256: LinuxARM64BwrapSHA256, SizeBytes: LinuxARM64BwrapSize,
					},
				},
			},
		},
	}
}

// LinuxARM64Manifest retains the single-platform development bundle profile.
func LinuxARM64Manifest() runtimelock.Manifest {
	manifest := ProductionManifest()
	delete(manifest.Artifacts, PlatformLinuxAMD64)
	return manifest
}

// LinuxAMD64Manifest returns the single-platform amd64 bundle profile.
func LinuxAMD64Manifest() runtimelock.Manifest {
	manifest := ProductionManifest()
	delete(manifest.Artifacts, PlatformLinuxARM64)
	return manifest
}

// ManifestBytes is the single deterministic textual representation checked
// into packaging/stockruntime/runtime-manifest.json and copied into release
// artifacts. A trailing LF is part of the signed bytes.
func ManifestBytes() ([]byte, error) {
	manifest := ProductionManifest()
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate stock runtime profile: %w", err)
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		return nil, fmt.Errorf("encode stock runtime manifest: %w", err)
	}
	return output.Bytes(), nil
}
