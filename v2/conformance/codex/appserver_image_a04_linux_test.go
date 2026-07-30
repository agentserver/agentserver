//go:build linux

package codex_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmcp"
	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	a04ImageGateEnvironment       = "AGENTSERVER_RUN_IMAGE_A04"
	a04ExpectedReleaseEnvironment = "AGENTSERVER_EXPECTED_CODEX_RELEASE"
	a04ExpectedDigestEnvironment  = "AGENTSERVER_EXPECTED_CODEX_SHA256"
	a04ExpectedSizeEnvironment    = "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES"
	a04HardenTmpfsEnvironment     = "AGENTSERVER_HARDEN_A04_TMPFS"
	a04SystemDirectory            = "/etc/codex"
	a04SystemRequirementsPath     = "/etc/codex/requirements.toml"
	a04UserExtraName              = "user_extra"
	a04ProjectExtraName           = "project_extra"
)

var a04SHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestAppServerA04SystemRequirementsDenyAllMCP is intentionally gated
// behind the disposable-image runner. It writes the real Linux system path and
// therefore refuses to run unless /etc/codex is a separate hardened tmpfs and
// the container root is read-only. An explicit empty managed MCP allowlist
// must block the production-shaped direct executor config, a user injection,
// and an enabled trusted-project injection before bootstrap, while the client-
// supplied dynamic executor tool remains the exact model-visible surface.
// mcpServerStatus/list is deliberately not used as an allowlist oracle: stock
// 0.146.0 reports configured-but-disabled names there.
func TestAppServerA04SystemRequirementsDenyAllMCP(t *testing.T) {
	requireA04DisposableImage(t)

	directExecutorServer, directExecutorCA := startExecutorTLSMCPServer(t, nil)
	userExtraServer, userExtraCA := startExecutorTLSMCPServer(t, nil)
	projectExtraServer, projectExtraCA := startExecutorTLSMCPServer(t, nil)
	caBundle := bytes.Join([][]byte{directExecutorCA, userExtraCA, projectExtraCA}, nil)
	caPath := filepath.Join(t.TempDir(), "a04-loopback-cas.pem")
	if err := os.WriteFile(caPath, caBundle, 0o600); err != nil {
		t.Fatalf("write A04 loopback CA bundle: %v", err)
	}

	installA04SystemRequirements(t)
	binary, paths := prepareLiveCodex(t)
	assertA04CandidateArtifact(t, binary, paths)
	paths.environment = append(paths.environment, "CODEX_CA_CERTIFICATE="+caPath)
	modelServer := writeA04ScenarioConfig(
		t,
		paths,
		directExecutorServer.URL(),
		userExtraServer.URL(),
		projectExtraServer.URL(),
	)
	surface := runA04Turn(t, binary, paths, modelServer, "a04 managed deny-all MCP")
	assertA04DynamicExecutorToolSurface(t, surface)

	assertA04EndpointUntouched(t, "direct executor config", directExecutorServer)
	assertA04EndpointUntouched(t, "user injection", userExtraServer)
	assertA04EndpointUntouched(t, "trusted-project injection", projectExtraServer)
}

func requireA04DisposableImage(t *testing.T) {
	t.Helper()
	if os.Getenv(a04ImageGateEnvironment) != "1" {
		t.Skip("run through conformance/image/a04/run.sh; the A04 probe needs an isolated Linux /etc")
	}
	if os.Geteuid() != 0 {
		t.Fatal("A04 image gate must start as container root to install the system requirements fixture")
	}
	rootMount := requireA04Mount(t, "/")
	if !rootMount.hasOption("ro") {
		t.Fatalf("A04 image root mount is not read-only: %s", rootMount.options)
	}
	if os.Getenv(a04HardenTmpfsEnvironment) == "1" {
		unhardened := requireA04Mount(t, a04SystemDirectory)
		if unhardened.filesystem != "tmpfs" {
			t.Fatalf("refusing to remount %s filesystem %q", a04SystemDirectory, unhardened.filesystem)
		}
		flags := uintptr(syscall.MS_REMOUNT | syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC)
		if err := syscall.Mount("", a04SystemDirectory, "", flags, ""); err != nil {
			t.Fatalf("harden pre-existing A04 tmpfs: %v", err)
		}
	}
	requirementsMount := requireA04Mount(t, a04SystemDirectory)
	if requirementsMount.filesystem != "tmpfs" {
		t.Fatalf("%s filesystem = %q, want tmpfs", a04SystemDirectory, requirementsMount.filesystem)
	}
	for _, option := range []string{"rw", "nodev", "nosuid", "noexec"} {
		if !requirementsMount.hasOption(option) {
			t.Fatalf("%s mount options %q omit %q", a04SystemDirectory, requirementsMount.options, option)
		}
	}
	info, err := os.Lstat(a04SystemDirectory)
	if err != nil {
		t.Fatalf("inspect %s: %v", a04SystemDirectory, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is not a real directory: mode=%s", a04SystemDirectory, info.Mode())
	}
	entries, err := os.ReadDir(a04SystemDirectory)
	if err != nil {
		t.Fatalf("read %s: %v", a04SystemDirectory, err)
	}
	if len(entries) != 0 {
		t.Fatalf("refusing to replace non-empty %s: entries=%v", a04SystemDirectory, directoryEntryNames(entries))
	}
}

type a04Mount struct {
	filesystem string
	options    string
}

func (m a04Mount) hasOption(want string) bool {
	for _, option := range strings.Split(m.options, ",") {
		if option == want {
			return true
		}
	}
	return false
}

func requireA04Mount(t *testing.T, mountPoint string) a04Mount {
	t.Helper()
	contents, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatalf("read Linux mountinfo: %v", err)
	}
	if len(contents) > 4*1024*1024 {
		t.Fatalf("Linux mountinfo exceeds 4 MiB")
	}
	var matches []a04Mount
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || fields[4] != mountPoint {
			continue
		}
		separator := -1
		for index := 6; index < len(fields); index++ {
			if fields[index] == "-" {
				separator = index
				break
			}
		}
		if separator < 0 || separator+3 >= len(fields) {
			t.Fatalf("malformed mountinfo entry for %s: %q", mountPoint, line)
		}
		matches = append(matches, a04Mount{
			filesystem: fields[separator+1],
			options:    fields[5] + "," + fields[separator+3],
		})
	}
	if len(matches) != 1 {
		t.Fatalf("mountinfo entries for %s = %d, want exactly one", mountPoint, len(matches))
	}
	return matches[0]
}

func directoryEntryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}

func installA04SystemRequirements(t *testing.T) {
	t.Helper()
	contents := "check_for_update_on_startup = false\nmcp_servers = {}\n"
	if err := writeExclusiveFile(a04SystemRequirementsPath, []byte(contents), 0o444); err != nil {
		t.Fatalf("install A04 system requirements: %v", err)
	}
	if err := os.Chmod(a04SystemDirectory, 0o555); err != nil {
		t.Fatalf("seal A04 system requirements directory: %v", err)
	}
	got, err := os.ReadFile(a04SystemRequirementsPath)
	if err != nil {
		t.Fatalf("read installed A04 system requirements: %v", err)
	}
	if !bytes.Equal(got, []byte(contents)) {
		t.Fatalf("installed A04 requirements changed: %q", got)
	}
}

func writeExclusiveFile(path string, contents []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	written, err := file.Write(contents)
	if err != nil {
		_ = file.Close()
		return err
	}
	if written != len(contents) {
		_ = file.Close()
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func assertA04CandidateArtifact(t *testing.T, binary string, paths livePaths) {
	t.Helper()
	wantRelease := os.Getenv(a04ExpectedReleaseEnvironment)
	wantDigest := os.Getenv(a04ExpectedDigestEnvironment)
	wantSizeText := os.Getenv(a04ExpectedSizeEnvironment)
	if wantRelease == "" || !a04SHA256Pattern.MatchString(wantDigest) || wantSizeText == "" {
		t.Fatalf("%s, %s, and %s must pin the image candidate", a04ExpectedReleaseEnvironment, a04ExpectedDigestEnvironment, a04ExpectedSizeEnvironment)
	}
	if _, characterized := characterizedA03Releases[wantRelease]; !characterized {
		t.Fatalf("A04 image candidate %q lacks release-bound app-server characterization", wantRelease)
	}
	wantSize, err := strconv.ParseInt(wantSizeText, 10, 64)
	if err != nil || wantSize < 1 {
		t.Fatalf("invalid %s %q", a04ExpectedSizeEnvironment, wantSizeText)
	}
	if gotRelease := candidateRelease(t, binary, paths); gotRelease != wantRelease {
		t.Fatalf("A04 image Codex release = %q, want %q", gotRelease, wantRelease)
	}
	digest, size, err := runtimelock.HashFile(binary)
	if err != nil {
		t.Fatalf("hash A04 image Codex: %v", err)
	}
	if digest != wantDigest || size != wantSize {
		t.Fatalf("A04 image Codex artifact = sha256 %s size %d, want %s/%d", digest, size, wantDigest, wantSize)
	}
	t.Logf("A04 candidate artifact: release=%s sha256=%s size=%d", wantRelease, digest, size)
}

func writeA04ScenarioConfig(
	t *testing.T,
	paths livePaths,
	executorURL string,
	userExtraURL string,
	projectExtraURL string,
) *scriptedmodel.Server {
	t.Helper()
	modelResponse, err := scriptedmodel.AssistantMessage(
		"response-a04-image",
		"message-a04-image",
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{modelResponse},
	})
	if err != nil {
		t.Fatalf("start A04 image scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)
	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      executorURL,
		mcpEnabledTools:   []string{approvedMCPToolName},
	})

	userConfig := fmt.Sprintf("\n[projects.%s]\ntrust_level = \"trusted\"\n", strconv.Quote(paths.cwd))
	userConfig += a04MCPConfig(a04UserExtraName, userExtraURL)
	if err := appendFile(filepath.Join(paths.codexHome, "config.toml"), []byte(userConfig)); err != nil {
		t.Fatalf("append A04 user injections: %v", err)
	}

	projectDirectory := filepath.Join(paths.cwd, ".codex")
	if err := os.Mkdir(projectDirectory, 0o700); err != nil {
		t.Fatalf("create A04 trusted project config directory: %v", err)
	}
	projectConfig := []byte(a04MCPConfig(a04ProjectExtraName, projectExtraURL))
	if err := writeExclusiveFile(filepath.Join(projectDirectory, "config.toml"), projectConfig, 0o600); err != nil {
		t.Fatalf("write A04 trusted project injection: %v", err)
	}
	return modelServer
}

func a04MCPConfig(name, serverURL string) string {
	return fmt.Sprintf(`
[mcp_servers.%s]
url = %s
required = true
startup_timeout_sec = 5.0
tool_timeout_sec = 5.0
default_tools_approval_mode = "approve"
enabled_tools = ["%s"]
`, name, strconv.Quote(serverURL), approvedMCPToolName)
}

func appendFile(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	written, err := file.Write(contents)
	if err != nil {
		_ = file.Close()
		return err
	}
	if written != len(contents) {
		_ = file.Close()
		return io.ErrShortWrite
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func runA04Turn(
	t *testing.T,
	binary string,
	paths livePaths,
	modelServer *scriptedmodel.Server,
	prompt string,
) []string {
	t.Helper()
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	assertA04SystemRequirementsLoaded(t, collector)
	assertA04TrustedProjectLayer(t, collector, paths.cwd)
	thread, turn := startMinimalAppServerTurnWithDynamicTools(
		t,
		collector,
		paths.cwd,
		prompt,
		"never",
		approvedDynamicExecutorTools(),
	)
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("A04 scripted model failures: %v", failures)
	}
	modelRequests := modelServer.Requests()
	if len(modelRequests) != 1 {
		t.Fatalf("A04 scripted model received %d requests, want one", len(modelRequests))
	}
	return modelToolNames(t, decodeCapturedModelRequest(t, modelRequests[0]).Tools)
}

func assertA04SystemRequirementsLoaded(t *testing.T, collector *rpcCollector) {
	t.Helper()
	sendRPC(t, collector.process, map[string]any{
		"id":     19,
		"method": "configRequirements/read",
		"params": map[string]any{},
	})
	var result struct {
		Requirements *struct {
			CheckForUpdateOnStartup *bool `json:"checkForUpdateOnStartup"`
		} `json:"requirements"`
	}
	mustDecodeResult(t, collector.response(t, "19"), &result)
	if result.Requirements == nil {
		t.Fatal("A04 configRequirements/read returned null; system requirements were not loaded")
	}
	if result.Requirements.CheckForUpdateOnStartup == nil || *result.Requirements.CheckForUpdateOnStartup {
		t.Fatalf(
			"A04 managed sentinel checkForUpdateOnStartup = %v, want false",
			result.Requirements.CheckForUpdateOnStartup,
		)
	}
}

func assertA04TrustedProjectLayer(t *testing.T, collector *rpcCollector, cwd string) {
	t.Helper()
	sendRPC(t, collector.process, map[string]any{
		"id":     20,
		"method": "config/read",
		"params": map[string]any{"includeLayers": true, "cwd": cwd},
	})
	var result struct {
		Layers []struct {
			Name           json.RawMessage `json:"name"`
			Config         json.RawMessage `json:"config"`
			DisabledReason *string         `json:"disabledReason"`
		} `json:"layers"`
	}
	mustDecodeResult(t, collector.response(t, "20"), &result)
	for _, layer := range result.Layers {
		var name struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(layer.Name, &name) != nil {
			continue
		}
		if name.Type != "project" {
			continue
		}
		var config struct {
			MCPServers map[string]struct {
				URL string `json:"url"`
			} `json:"mcp_servers"`
		}
		if json.Unmarshal(layer.Config, &config) != nil {
			continue
		}
		if server, exists := config.MCPServers[a04ProjectExtraName]; exists {
			if layer.DisabledReason != nil {
				t.Fatalf("A04 project layer was not trusted: %s", *layer.DisabledReason)
			}
			if !strings.HasPrefix(server.URL, "https://127.0.0.1:") {
				t.Fatalf("A04 project layer endpoint = %q", server.URL)
			}
			return
		}
	}
	encodedLayers, err := json.Marshal(result.Layers)
	if err != nil {
		t.Fatalf("encode A04 config layers after missing project injection: %v", err)
	}
	t.Fatalf("A04 config/read did not expose an enabled project layer containing %q: layers=%s", a04ProjectExtraName, encodedLayers)
}

func assertA04DynamicExecutorToolSurface(t *testing.T, surface []string) {
	t.Helper()
	want := []string{executorDynamicNamespace + "." + approvedMCPToolName}
	if !equalStrings(surface, want) {
		t.Fatalf("A04 model tool surface = %v, want %v", surface, want)
	}
}

func assertA04EndpointUntouched(t *testing.T, label string, server *scriptedmcp.Server) {
	t.Helper()
	if failures := server.Failures(); len(failures) != 0 {
		t.Fatalf("A04 %s endpoint failures: %v", label, failures)
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Fatalf("A04 %s endpoint received methods %v", label, mcpRequestMethods(requests))
	}
}
