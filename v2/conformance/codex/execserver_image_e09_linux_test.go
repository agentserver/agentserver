//go:build linux

package codex_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	e09ImageGateEnvironment           = "AGENTSERVER_RUN_IMAGE_E09"
	e09ExpectedPlatformEnvironment    = "AGENTSERVER_EXPECTED_RUNTIME_PLATFORM"
	e09ExpectedReleaseEnvironment     = "AGENTSERVER_EXPECTED_CODEX_RELEASE"
	e09ExpectedCodexDigestEnvironment = "AGENTSERVER_EXPECTED_CODEX_SHA256"
	e09ExpectedCodexSizeEnvironment   = "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES"
	e09ExpectedBwrapDigestEnvironment = "AGENTSERVER_EXPECTED_BWRAP_SHA256"
	e09ExpectedBwrapSizeEnvironment   = "AGENTSERVER_EXPECTED_BWRAP_SIZE_BYTES"
	e09RuntimeCodexRelativePath       = "bin/codex"
	e09RuntimeBwrapRelativePath       = "codex-resources/bwrap"
	e09RuntimeNoPathRelativePath      = ".agentserver-no-path"
	e09RuntimePackageMetadata         = "codex-package.json"
	e09ImagePoisonBwrapPath           = "/opt/agentserver/poison/bwrap"
	e09ReadFixtureContents            = "e09-readable\n"
)

var e09ImageSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestExecServerE09BundledBwrapImageGate is intentionally run only through
// conformance/image/e09. It verifies the exact Codex and bwrap bytes again in
// the disposable image, builds the launch plan through PrepareExecServerLaunch,
// inherits that plan's controlled PATH into sandbox helpers, and runs real
// read-only and workspace-write process/start requests. The latter proves that
// writes work only under the declared workspace while a sibling path on the
// same writable tmpfs remains inaccessible.
func TestExecServerE09BundledBwrapImageGate(t *testing.T) {
	platform := requireE09DisposableImage(t)

	binary, paths := prepareLiveCodex(t)
	runtimeRoot := filepath.Dir(filepath.Dir(binary))
	if got, want := binary, filepath.Join(runtimeRoot, filepath.FromSlash(e09RuntimeCodexRelativePath)); got != want {
		t.Fatalf("E09 Codex path = %q, want minimal-bundle path %q", got, want)
	}
	bwrapPath := filepath.Join(runtimeRoot, filepath.FromSlash(e09RuntimeBwrapRelativePath))
	assertE09MinimalRuntimeLayout(t, runtimeRoot)
	manifest := e09ImageManifest(t, platform, binary, bwrapPath, paths)
	assertE09BwrapArgv0(t, bwrapPath)

	poisonInfo, err := os.Lstat(e09ImagePoisonBwrapPath)
	if err != nil || !poisonInfo.Mode().IsRegular() || poisonInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("inspect E09 ambient poison bwrap: info=%v error=%v", poisonInfo, err)
	}
	poisonDirectory := filepath.Dir(e09ImagePoisonBwrapPath)
	poisonMarker := filepath.Join(paths.cwd, "ambient-bwrap-executed")
	poisonedBase := replaceEnvironmentValue(paths.environment, "PATH", poisonDirectory)
	poisonedBase = append(poisonedBase, e09PoisonMarkerEnvironment+"="+poisonMarker)

	plan, err := manifest.PrepareExecServerLaunch(runtimeRoot, platform)
	if err != nil {
		t.Fatalf("prepare verified E09 exec-server launch: %v", err)
	}
	if got, want := plan.Program(), binary; got != want {
		t.Fatalf("E09 launch program = %q, want %q", got, want)
	}
	if got, want := plan.Arguments(), []string{"exec-server", "--listen", "stdio", "--strict-config"}; !equalStrings(got, want) {
		t.Fatalf("E09 launch arguments = %q, want %q", got, want)
	}
	controlledEnvironment, err := plan.Environment(poisonedBase)
	if err != nil {
		t.Fatalf("apply E09 controlled environment: %v", err)
	}
	controlledPath := filepath.Join(runtimeRoot, e09RuntimeNoPathRelativePath)
	if got := environmentValue(t, controlledEnvironment, "PATH"); got != controlledPath {
		t.Fatalf("E09 controlled PATH = %q, want %q", got, controlledPath)
	}
	if _, err := os.Lstat(controlledPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("E09 reserved PATH location exists or cannot be inspected: %v", err)
	}

	paths.environment = controlledEnvironment
	process := startPreparedLiveCodex(t, plan.Program(), paths, plan.Arguments()...)
	initializeExecServer(t, process)
	collector := newRPCCollector(process)
	assertE09LinuxSandboxAlias(t, paths.codexHome, binary)

	readFixture := filepath.Join(paths.cwd, "read-fixture")
	if err := os.WriteFile(readFixture, []byte(e09ReadFixtureContents), 0o600); err != nil {
		t.Fatalf("write E09 read fixture: %v", err)
	}
	readOnlyParams := execStartParams(t, paths, "e09-read-only", "e09-read-only", false, map[string]string{
		execChildE09ReadPathEnv:     readFixture,
		execChildE09ExpectedPathEnv: controlledPath,
	})
	readOnlyParams["envPolicy"] = e09InheritServerEnvironmentPolicy()
	readOnlyParams["sandbox"] = e09SandboxContext(t, paths.cwd, false)
	sendRPC(t, process, map[string]any{"id": 2, "method": "process/start", "params": readOnlyParams})
	mustDecodeResult(t, collector.response(t, "2"), &struct {
		ProcessID string `json:"processId"`
	}{})
	readOnlyEvents := collector.processEventsUntilClosed(t, "e09-read-only")
	logE09SandboxFailure(t, "read-only", readOnlyEvents)
	readOnlyOutput := assertCompletedProcessEvents(t, readOnlyEvents, 0)
	if !bytes.Equal(readOnlyOutput.stdout, []byte(execChildE09ReadOutput)) || len(readOnlyOutput.stderr) != 0 || len(readOnlyOutput.pty) != 0 {
		t.Fatalf("E09 read-only output = stdout %q stderr %q pty %q", readOnlyOutput.stdout, readOnlyOutput.stderr, readOnlyOutput.pty)
	}

	outsideDirectory := filepath.Join(paths.root, "outside-workspace")
	if err := os.Mkdir(outsideDirectory, 0o700); err != nil {
		t.Fatalf("create E09 outside-workspace directory: %v", err)
	}
	outsidePath := filepath.Join(outsideDirectory, "escape")
	if err := os.WriteFile(outsidePath, []byte("outer mount is writable\n"), 0o600); err != nil {
		t.Fatalf("E09 outside path is not writable before sandboxing: %v", err)
	}
	if err := os.Remove(outsidePath); err != nil {
		t.Fatalf("reset E09 outside path: %v", err)
	}
	workspacePath := filepath.Join(paths.cwd, "workspace-write")
	workspaceParams := execStartParams(t, paths, "e09-workspace-write", "e09-workspace-write", false, map[string]string{
		execChildE09WorkspacePathEnv: workspacePath,
		execChildE09OutsidePathEnv:   outsidePath,
		execChildE09ExpectedPathEnv:  controlledPath,
	})
	workspaceParams["envPolicy"] = e09InheritServerEnvironmentPolicy()
	workspaceParams["sandbox"] = e09SandboxContext(t, paths.cwd, true)
	sendRPC(t, process, map[string]any{"id": 3, "method": "process/start", "params": workspaceParams})
	mustDecodeResult(t, collector.response(t, "3"), &struct {
		ProcessID string `json:"processId"`
	}{})
	workspaceEvents := collector.processEventsUntilClosed(t, "e09-workspace-write")
	logE09SandboxFailure(t, "workspace-write", workspaceEvents)
	workspaceOutput := assertCompletedProcessEvents(t, workspaceEvents, 0)
	if !bytes.Equal(workspaceOutput.stdout, []byte(execChildE09WorkspaceOutput)) || len(workspaceOutput.stderr) != 0 || len(workspaceOutput.pty) != 0 {
		t.Fatalf("E09 workspace-write output = stdout %q stderr %q pty %q", workspaceOutput.stdout, workspaceOutput.stderr, workspaceOutput.pty)
	}
	if got, err := os.ReadFile(workspacePath); err != nil || string(got) != "workspace write allowed\n" {
		t.Fatalf("E09 workspace result = %q, error %v", got, err)
	}
	if _, err := os.Lstat(outsidePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("E09 outside-workspace path was created or cannot be inspected: %v", err)
	}
	if _, err := os.Lstat(poisonMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("E09 ambient bwrap was executed or marker cannot be inspected: %v", err)
	}

	closeAndWait(t, process)
	artifacts := manifest.Artifacts[platform]
	t.Logf(
		"E09 bundled bwrap selected: platform=%s release=%s codex_sha256=%s codex_size=%d bwrap_sha256=%s bwrap_size=%d PATH=%s",
		platform,
		manifest.CodexRelease,
		artifacts.Codex.SHA256,
		artifacts.Codex.SizeBytes,
		artifacts.ExternalExecutables["bwrap"].SHA256,
		artifacts.ExternalExecutables["bwrap"].SizeBytes,
		controlledPath,
	)
}

func requireE09DisposableImage(t *testing.T) string {
	t.Helper()
	if os.Getenv(e09ImageGateEnvironment) != "1" {
		t.Skip("run through conformance/image/e09/run.sh; E09 requires a disposable Linux image")
	}
	wantPlatform := os.Getenv(e09ExpectedPlatformEnvironment)
	if wantPlatform != "linux-amd64" && wantPlatform != "linux-arm64" {
		t.Fatalf("invalid %s %q", e09ExpectedPlatformEnvironment, wantPlatform)
	}
	if got := runtimelock.CurrentPlatform(); got != wantPlatform {
		t.Fatalf("E09 image platform = %q, want %q", got, wantPlatform)
	}
	if os.Geteuid() == 0 {
		t.Fatal("E09 image gate must run as an unprivileged runtime user")
	}
	rootMount := requireA04Mount(t, "/")
	if !rootMount.hasOption("ro") {
		t.Fatalf("E09 image root mount is not read-only: %s", rootMount.options)
	}
	return wantPlatform
}

func e09ImageManifest(t *testing.T, platform, binary, bwrapPath string, paths livePaths) runtimelock.Manifest {
	t.Helper()
	release := os.Getenv(e09ExpectedReleaseEnvironment)
	commit, characterized := e09CandidateCommits[release]
	if !characterized {
		t.Fatalf("E09 release %q has no characterized commit", release)
	}
	bounds, characterized := e10CandidateBounds[release]
	if !characterized {
		t.Fatalf("E09 release %q has no characterized exec-server bounds", release)
	}
	if gotRelease := candidateRelease(t, binary, paths); gotRelease != release {
		t.Fatalf("E09 image Codex release = %q, want %q", gotRelease, release)
	}
	artifactTarget := e09LinuxArtifactTarget(t, platform)

	codexArtifact := e09PinnedArtifact(
		t,
		"Codex",
		binary,
		e09RuntimeCodexRelativePath,
		fmt.Sprintf("https://github.com/openai/codex/releases/download/rust-v%s/codex-%s.tar.gz", release, artifactTarget),
		e09ExpectedCodexDigestEnvironment,
		e09ExpectedCodexSizeEnvironment,
	)
	bwrapArtifact := e09PinnedArtifact(
		t,
		"bwrap",
		bwrapPath,
		e09RuntimeBwrapRelativePath,
		fmt.Sprintf("https://github.com/openai/codex/releases/download/rust-v%s/bwrap-%s.tar.gz", release, artifactTarget),
		e09ExpectedBwrapDigestEnvironment,
		e09ExpectedBwrapSizeEnvironment,
	)
	return runtimelock.Manifest{
		ManifestVersion:                runtimelock.CurrentManifestVersion,
		CodexRelease:                   release,
		CodexCommit:                    commit,
		AppServerSchemaSHA256:          strings.Repeat("a", 64),
		AppServerSchemaDigestAlgorithm: runtimelock.AppServerSchemaDigestAlgorithmV1,
		ExecProtocolSourceSHA256:       strings.Repeat("b", 64),
		ExecServerBounds:               bounds,
		AgentxLimits:                   characterizedE10AgentxLimits(),
		CheckpointAllowlistVersion:     1,
		AgentxProtocolVersion:          "2.0",
		Artifacts: map[string]runtimelock.PlatformArtifacts{
			platform: {
				Codex: codexArtifact,
				ExternalExecutables: map[string]runtimelock.FileArtifact{
					"bwrap": bwrapArtifact,
				},
			},
		},
	}
}

func e09LinuxArtifactTarget(t *testing.T, platform string) string {
	t.Helper()
	switch platform {
	case "linux-amd64":
		return "x86_64-unknown-linux-musl"
	case "linux-arm64":
		return "aarch64-unknown-linux-musl"
	default:
		t.Fatalf("unsupported E09 platform %q", platform)
		return ""
	}
}

func e09PinnedArtifact(
	t *testing.T,
	label string,
	path string,
	relativePath string,
	sourceURL string,
	digestEnvironment string,
	sizeEnvironment string,
) runtimelock.FileArtifact {
	t.Helper()
	wantDigest := os.Getenv(digestEnvironment)
	wantSizeText := os.Getenv(sizeEnvironment)
	if !e09ImageSHA256Pattern.MatchString(wantDigest) || wantSizeText == "" {
		t.Fatalf("%s and %s must pin the E09 %s artifact", digestEnvironment, sizeEnvironment, label)
	}
	wantSize, err := strconv.ParseInt(wantSizeText, 10, 64)
	if err != nil || wantSize < 1 || strconv.FormatInt(wantSize, 10) != wantSizeText {
		t.Fatalf("invalid %s %q", sizeEnvironment, wantSizeText)
	}
	digest, size, err := runtimelock.HashFile(path)
	if err != nil {
		t.Fatalf("hash E09 %s: %v", label, err)
	}
	if digest != wantDigest || size != wantSize {
		t.Fatalf("E09 %s artifact = sha256 %s size %d, want %s/%d", label, digest, size, wantDigest, wantSize)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("inspect E09 %s: %v", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		t.Fatalf("E09 %s mode = %s, want immutable executable regular file", label, info.Mode())
	}
	return runtimelock.FileArtifact{
		Path:      relativePath,
		SourceURL: sourceURL,
		SHA256:    wantDigest,
		SizeBytes: wantSize,
	}
}

func e09InheritServerEnvironmentPolicy() map[string]any {
	return map[string]any{
		"inherit":               "all",
		"ignoreDefaultExcludes": true,
		"exclude":               []string{},
		"set":                   map[string]string{},
		"includeOnly":           []string{},
	}
}

func e09SandboxContext(t *testing.T, workspace string, workspaceWrite bool) map[string]any {
	t.Helper()
	workspaceURI := localFileURI(t, workspace)
	entries := []any{
		map[string]any{
			"path":   map[string]any{"type": "special", "value": map[string]any{"kind": "root"}},
			"access": "read",
		},
	}
	if workspaceWrite {
		entries = append(entries, map[string]any{
			"path":   map[string]any{"type": "path", "path": workspaceURI},
			"access": "write",
		})
	}
	return map[string]any{
		"permissions": map[string]any{
			"type": "managed",
			"file_system": map[string]any{
				"type":    "restricted",
				"entries": entries,
			},
			"network": "restricted",
		},
		"cwd":                          workspaceURI,
		"workspaceRoots":               []string{workspaceURI},
		"windowsSandboxLevel":          "disabled",
		"windowsSandboxPrivateDesktop": false,
		"useLegacyLandlock":            false,
	}
}

func environmentValue(t *testing.T, environment []string, name string) string {
	t.Helper()
	var value string
	found := false
	for _, entry := range environment {
		entryName, entryValue, valid := strings.Cut(entry, "=")
		if !valid || entryName != name {
			continue
		}
		if found {
			t.Fatalf("environment contains duplicate %s", name)
		}
		found = true
		value = entryValue
	}
	if !found {
		t.Fatalf("environment omits %s", name)
	}
	return value
}

func assertE09LinuxSandboxAlias(t *testing.T, codexHome, binary string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(codexHome, "tmp", "arg0", "codex-arg0*", "codex-linux-sandbox"))
	if err != nil {
		t.Fatalf("find E09 Linux sandbox alias: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("E09 Linux sandbox aliases = %q, want exactly one", matches)
	}
	resolved, err := filepath.EvalSymlinks(matches[0])
	if err != nil {
		t.Fatalf("resolve E09 Linux sandbox alias: %v", err)
	}
	if resolved != binary {
		t.Fatalf("E09 Linux sandbox alias resolves to %q, want %q", resolved, binary)
	}
}

func assertE09BwrapArgv0(t *testing.T, bwrapPath string) {
	t.Helper()
	command := exec.Command(
		bwrapPath,
		"--ro-bind", "/", "/",
		"--argv0", e09BwrapArgv0Probe,
		"--", os.Args[0],
	)
	command.Dir = "/"
	command.Env = []string{}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("E09 direct bundled bwrap argv0 probe: %v; output=%q", err, output)
	}
	if string(output) != e09BwrapArgv0ProbeOutput {
		t.Fatalf(
			"E09 bundled bwrap did not preserve argv0 (cross-architecture emulation is not valid gate evidence): output=%q, want %q",
			output,
			e09BwrapArgv0ProbeOutput,
		)
	}
}

func assertE09MinimalRuntimeLayout(t *testing.T, root string) {
	t.Helper()
	assertE09DirectoryEntries(t, root, []string{"bin", "codex-resources"})
	assertE09DirectoryEntries(t, filepath.Join(root, "bin"), []string{"codex"})
	assertE09DirectoryEntries(t, filepath.Join(root, "codex-resources"), []string{"bwrap"})
	if _, err := os.Lstat(filepath.Join(root, e09RuntimePackageMetadata)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("E09 minimal bundle contains package metadata or cannot be inspected: %v", err)
	}
}

func assertE09DirectoryEntries(t *testing.T, directory string, want []string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read E09 runtime directory %q: %v", directory, err)
	}
	got := make([]string, len(entries))
	for index, entry := range entries {
		got[index] = entry.Name()
	}
	if !equalStrings(got, want) {
		t.Fatalf("E09 runtime directory %q entries = %q, want %q", directory, got, want)
	}
}

func logE09SandboxFailure(t *testing.T, label string, events []observedProcessEvent) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	failed := false
	for _, event := range events {
		if event.method == "process/output" {
			switch event.stream {
			case "stdout":
				_, _ = stdout.Write(event.chunk)
			case "stderr":
				_, _ = stderr.Write(event.chunk)
			}
		}
		if event.method == "process/exited" && event.sandboxDenied != nil && *event.sandboxDenied {
			failed = true
		}
	}
	if failed {
		t.Logf("E09 %s sandbox failure output: stdout=%q stderr=%q", label, stdout.Bytes(), stderr.Bytes())
	}
}
