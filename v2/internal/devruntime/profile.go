// Package devruntime assembles an explicitly insecure development runtime
// bundle from already-downloaded, release-pinned stock Codex artifacts.
// It does not download artifacts and does not create a production runtime lock.
package devruntime

import "github.com/agentserver/agentserver/v2/internal/runtimelock"

const (
	PlatformLinuxARM64 = "linux-arm64"

	stockCodexRelease = "0.146.0"
	stockCodexCommit  = "e363b08c9175ac1cbe5893615dd2cb9ddf95043b"

	stockAppServerSchemaSHA256 = "834975f055f4dc0bf25231ab23f446f4bfef63fd3f7832bc9b0c5fe8a32363bb"
	stockExecProtocolSHA256    = "7917ed958875dc94258d04e088f349fc6d7fbab41ccf133226767af326d22a1f"

	linuxARM64CodexSHA256 = "cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6"
	linuxARM64CodexSize   = int64(269098800)
	linuxARM64CodexURL    = "https://github.com/openai/codex/releases/download/rust-v0.146.0/codex-aarch64-unknown-linux-musl.tar.gz"

	linuxARM64BwrapSHA256 = "c547cbdc762a70ed216789ffaa4c6c0e7d2beabe32245a498f8e365a9fc8dab4"
	linuxARM64BwrapSize   = int64(529168)
	linuxARM64BwrapURL    = "https://github.com/openai/codex/releases/download/rust-v0.146.0/bwrap-aarch64-unknown-linux-musl.tar.gz"
)

type protocolSourceFile struct {
	Path   string
	SHA256 string
}

// stockExecProtocolSources is the production Rust source surface of the
// exec-server-protocol crate at stockCodexCommit. Test modules and build
// metadata are deliberately excluded. The aggregate uses the same sorted
// "<sha256><two spaces><repo-relative path><LF>" tree record format as
// runtimelock.HashTree.
var stockExecProtocolSources = []protocolSourceFile{
	{Path: "codex-rs/exec-server-protocol/src/lib.rs", SHA256: "4a8beeb8da4f41633e0c132f334de466990be981ec7978ec3666ee795ef392e0"},
	{Path: "codex-rs/exec-server-protocol/src/network_policy.rs", SHA256: "833ebff744e6ddc74fe69f367ab96df34d9fa4bfa32b168b48bdf94922e6c1b9"},
	{Path: "codex-rs/exec-server-protocol/src/process_id.rs", SHA256: "e027b4e4ac3581a188727a1b1d479168ae9b73e4509d0f8fbf94a3dbb0d0203f"},
	{Path: "codex-rs/exec-server-protocol/src/protocol.rs", SHA256: "c21c5d4ec195e75fd50020a8231f75d01e47ac03eb05c72ff0b2c0614eb64483"},
	{Path: "codex-rs/exec-server-protocol/src/rpc.rs", SHA256: "67fdd002caa343def5d78d417c944851ef176add634d06410d945603307c3f7c"},
}

func stockLinuxARM64Manifest() runtimelock.Manifest {
	return runtimelock.Manifest{
		ManifestVersion: runtimelock.CurrentManifestVersion,
		CodexRelease:    stockCodexRelease, CodexCommit: stockCodexCommit,
		AppServerSchemaSHA256:          stockAppServerSchemaSHA256,
		AppServerSchemaDigestAlgorithm: runtimelock.AppServerSchemaDigestAlgorithmV1,
		ExecProtocolSourceSHA256:       stockExecProtocolSHA256,
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
			PlatformLinuxARM64: {
				Codex: runtimelock.FileArtifact{
					Path: "bin/codex", SourceURL: linuxARM64CodexURL,
					SHA256: linuxARM64CodexSHA256, SizeBytes: linuxARM64CodexSize,
				},
				ExternalExecutables: map[string]runtimelock.FileArtifact{
					"bwrap": {
						Path: "codex-resources/bwrap", SourceURL: linuxARM64BwrapURL,
						SHA256: linuxARM64BwrapSHA256, SizeBytes: linuxARM64BwrapSize,
					},
				},
			},
		},
	}
}
