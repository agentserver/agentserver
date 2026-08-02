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

	CodexRelease = "0.146.0"
	CodexCommit  = "e363b08c9175ac1cbe5893615dd2cb9ddf95043b"

	AppServerSchemaSHA256 = "834975f055f4dc0bf25231ab23f446f4bfef63fd3f7832bc9b0c5fe8a32363bb"
	ExecProtocolSHA256    = "7917ed958875dc94258d04e088f349fc6d7fbab41ccf133226767af326d22a1f"
	ManifestSHA256        = "5b7297d0a4e5eb294dc201ce2e82ae8f73f27b51c3360c8737e4338e46e3d128"
	ManifestSizeBytes     = int64(2429)

	CheckpointAllowlistVersion = 1
	AgentxProtocolVersion      = "2.0"

	LinuxARM64CodexSHA256 = "cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6"
	LinuxARM64CodexSize   = int64(269098800)
	LinuxARM64CodexURL    = "https://github.com/openai/codex/releases/download/rust-v0.146.0/codex-aarch64-unknown-linux-musl.tar.gz"

	LinuxARM64BwrapSHA256 = "c547cbdc762a70ed216789ffaa4c6c0e7d2beabe32245a498f8e365a9fc8dab4"
	LinuxARM64BwrapSize   = int64(529168)
	LinuxARM64BwrapURL    = "https://github.com/openai/codex/releases/download/rust-v0.146.0/bwrap-aarch64-unknown-linux-musl.tar.gz"

	LinuxAMD64CodexSHA256 = "2e863156ed35ecc5253b1e2f907a9143077b9f7cb51942070c61996471ff6e04"
	LinuxAMD64CodexSize   = int64(311001136)
	LinuxAMD64CodexURL    = "https://github.com/openai/codex/releases/download/rust-v0.146.0/codex-x86_64-unknown-linux-musl.tar.gz"

	LinuxAMD64BwrapSHA256 = "77360cb751ccedc5971391444ac86a8a33c15b04d6b4a6fe45f5d25496e62c4c"
	LinuxAMD64BwrapSize   = int64(529776)
	LinuxAMD64BwrapURL    = "https://github.com/openai/codex/releases/download/rust-v0.146.0/bwrap-x86_64-unknown-linux-musl.tar.gz"
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
	{Path: "codex-rs/exec-server-protocol/src/lib.rs", SHA256: "4a8beeb8da4f41633e0c132f334de466990be981ec7978ec3666ee795ef392e0"},
	{Path: "codex-rs/exec-server-protocol/src/network_policy.rs", SHA256: "833ebff744e6ddc74fe69f367ab96df34d9fa4bfa32b168b48bdf94922e6c1b9"},
	{Path: "codex-rs/exec-server-protocol/src/process_id.rs", SHA256: "e027b4e4ac3581a188727a1b1d479168ae9b73e4509d0f8fbf94a3dbb0d0203f"},
	{Path: "codex-rs/exec-server-protocol/src/protocol.rs", SHA256: "c21c5d4ec195e75fd50020a8231f75d01e47ac03eb05c72ff0b2c0614eb64483"},
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
