//go:build linux

package codex_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/codexprocess"
	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
	"github.com/agentserver/agentserver/v2/internal/finalexec"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/agentserver/agentserver/v2/internal/networkguard"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sys/unix"
)

const (
	a12ImageGateEnvironment         = "AGENTSERVER_RUN_IMAGE_A12"
	a12ExpectedPlatformEnvironment  = "AGENTSERVER_EXPECTED_RUNTIME_PLATFORM"
	a12ExpectedReleaseEnvironment   = "AGENTSERVER_EXPECTED_CODEX_RELEASE"
	a12ExpectedDigestEnvironment    = "AGENTSERVER_EXPECTED_CODEX_SHA256"
	a12ExpectedSizeEnvironment      = "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES"
	a12HardenTmpfsEnvironment       = "AGENTSERVER_HARDEN_A12_TMPFS"
	a12SubprocessModeEnvironment    = "AGENTSERVER_A12_SUBPROCESS_MODE"
	a12FinalExecMode                = "final-exec"
	a12WorkerMode                   = "worker-supervisor"
	a12FinalProgramEnvironment      = "AGENTSERVER_A12_FINAL_PROGRAM"
	a12FinalDirectoryEnvironment    = "AGENTSERVER_A12_FINAL_DIRECTORY"
	a12TrapFDEnvironment            = "AGENTSERVER_A12_TRAP_FD"
	a12WorkerPIDEnvironment         = "AGENTSERVER_A12_WORKER_PID"
	a12WorkerFDEnvironment          = "AGENTSERVER_A12_WORKER_FD"
	a12WorkerCredentialEnvironment  = "AGENTSERVER_A12_WORKER_CREDENTIAL_PATH"
	a12WorkerStagingEnvironment     = "AGENTSERVER_A12_WORKER_STAGING_PATH"
	a12WorkerResultEnvironment      = "AGENTSERVER_A12_WORKER_RESULT_PATH"
	a12WorkerControlEnvironment     = "AGENTSERVER_A12_WORKER_CONTROL_PATH"
	a12WorkerHarnessEnvironment     = "AGENTSERVER_A12_WORKER_HARNESS_URL"
	a12ExecutorMCPEnvironment       = "AGENTSERVER_A12_EXECUTOR_MCP_URL"
	a12LLMProxyEnvironment          = "AGENTSERVER_A12_LLM_PROXY_URL"
	a12DirectProbeEnvironment       = "AGENTSERVER_A12_DIRECT_PROBE_URL"
	a12RedirectProbeEnvironment     = "AGENTSERVER_A12_REDIRECT_PROBE_URL"
	a12ScenarioEnvironment          = "AGENTSERVER_A12_SCENARIO"
	a12DNSProbeEnvironment          = "AGENTSERVER_A12_DNS_PROBE_ADDRESS"
	a12IPv6ProbeEnvironment         = "AGENTSERVER_A12_IPV6_PROBE_URL"
	a12WorkerSecretEnvironment      = "AGENTSERVER_A12_WORKER_SECRET"
	a12WorkerUID                    = uint32(65531)
	a12WorkerGID                    = uint32(65531)
	a12AppUID                       = uint32(65532)
	a12AppGID                       = uint32(65532)
	a12RuntimeDirectory             = "/run/agentserver"
	a12ExecutorCapability           = "a12-executor-capability-2f0865"
	a12ImageWorkerStagingSecret     = "a12-worker-staging-9334c1"
	a12ImageWorkerEnvironmentSecret = "a12-worker-environment-f7d83a"
	a12ControlRequest               = "a12-control-sensitivity\n"
	a12ControlResponse              = "a12-control-accepted\n"
	a12HarnessMarker                = "a12-worker-harness-only"
	a12AllowedAssistantText         = "a12 allowed model and worker MCP egress complete"
	a12AllowedToolCallID            = "call-a12-allowed-mcp"
	a12AllowedToolResultMarker      = "a12-worker-approved-mcp-egress"
	a12AllowedScenario              = "allowed"
	a12DirectScenario               = "direct-forbidden"
	a12RedirectScenario             = "redirect-forbidden"
	a12ScenarioCount                = 3
)

var a12SHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func runA12ImageSubprocess() (bool, int) {
	mode := os.Getenv(a12SubprocessModeEnvironment)
	if mode == "" {
		return false, 0
	}
	var err error
	switch mode {
	case a12FinalExecMode:
		err = runA12FinalExecSubprocess()
	case a12WorkerMode:
		err = runA12WorkerSubprocess()
	default:
		err = fmt.Errorf("unknown A12 subprocess mode %q", mode)
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "A12 subprocess: %v\n", err)
		return true, 97
	}
	return true, 0
}

func runA12FinalExecSubprocess() error {
	// Seal first: none of the filesystem, procfs, socket, or network probes below
	// may execute while the worker's narrowly retained set-ID capabilities are
	// still available to this nonzero-UID child.
	if err := finalexec.SealIdentity(a12AppUID, a12AppGID); err != nil {
		return fmt.Errorf("seal app-server launcher identity: %w", err)
	}
	if _, present := os.LookupEnv(a12WorkerSecretEnvironment); present {
		return errors.New("app-server launcher inherited the worker environment secret")
	}
	workerPID, err := parseA12PositiveInt(a12WorkerPIDEnvironment)
	if err != nil {
		return err
	}
	if parent := os.Getppid(); parent != workerPID {
		return fmt.Errorf("app-server parent pid = %d, want worker %d", parent, workerPID)
	}
	workerFD, err := parseA12PositiveInt(a12WorkerFDEnvironment)
	if err != nil {
		return err
	}
	trapFD, err := parseA12PositiveInt(a12TrapFDEnvironment)
	if err != nil {
		return err
	}
	if err := requireA12OnlyDeliberateInheritedFD(trapFD, []string{
		os.Getenv(a12WorkerCredentialEnvironment),
		os.Getenv(a12WorkerResultEnvironment),
		os.Getenv(a12WorkerStagingEnvironment),
	}); err != nil {
		return err
	}
	for label, path := range map[string]string{
		"worker credential": os.Getenv(a12WorkerCredentialEnvironment),
		"worker result":     os.Getenv(a12WorkerResultEnvironment),
		"worker staging":    os.Getenv(a12WorkerStagingEnvironment),
	} {
		if path == "" || !filepath.IsAbs(path) {
			return fmt.Errorf("%s path is not absolute", label)
		}
		if _, readErr := os.ReadFile(path); readErr == nil {
			return fmt.Errorf("app-server identity unexpectedly read %s", label)
		} else if !errors.Is(readErr, os.ErrPermission) {
			return fmt.Errorf("%s read failed without a permission boundary: %w", label, readErr)
		}
	}
	controlPath := os.Getenv(a12WorkerControlEnvironment)
	if controlPath == "" || !filepath.IsAbs(controlPath) {
		return errors.New("worker control socket path is not absolute")
	}
	controlConnection, controlErr := net.DialTimeout("unix", controlPath, 250*time.Millisecond)
	if controlErr == nil {
		_ = controlConnection.Close()
		return errors.New("app-server identity connected to the worker control socket")
	}
	for label, path := range map[string]string{
		"worker environment": fmt.Sprintf("/proc/%d/environ", workerPID),
		"worker descriptor":  fmt.Sprintf("/proc/%d/fd/%d", workerPID, workerFD),
	} {
		if _, readErr := os.ReadFile(path); readErr == nil {
			return fmt.Errorf("app-server identity unexpectedly read %s through procfs", label)
		}
	}
	if err := unix.Kill(workerPID, 0); !errors.Is(err, unix.EPERM) {
		return fmt.Errorf("app-server signal probe against worker = %v, want EPERM", err)
	}
	for _, path := range []string{
		"/workspace",
		"/workspaces",
		"/var/run/secrets/kubernetes.io/serviceaccount",
		"/run/secrets/kubernetes.io/serviceaccount",
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("forbidden app-server mount path %q exists or cannot be classified: %w", path, err)
		}
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		Timeout:   250 * time.Millisecond,
	}
	for label, target := range map[string]string{
		"direct forbidden sink": os.Getenv(a12DirectProbeEnvironment),
		"executor MCP":          os.Getenv(a12ExecutorMCPEnvironment),
		"forbidden IPv6 sink":   os.Getenv(a12IPv6ProbeEnvironment),
		"redirect sink":         os.Getenv(a12RedirectProbeEnvironment),
		"worker-only harness":   os.Getenv(a12WorkerHarnessEnvironment),
	} {
		if target == "" {
			return fmt.Errorf("%s URL is empty", label)
		}
		if response, err := client.Get(target); err == nil {
			_ = response.Body.Close()
			return fmt.Errorf("app-server identity reached %s with status %d", label, response.StatusCode)
		}
	}
	if address := os.Getenv(a12DNSProbeEnvironment); address != "" {
		connection, err := net.DialTimeout("udp4", address, time.Second)
		if err == nil {
			_, _ = connection.Write([]byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01})
			_ = connection.Close()
		}
	}

	program := os.Getenv(a12FinalProgramEnvironment)
	directory := os.Getenv(a12FinalDirectoryEnvironment)
	targetEnvironment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found || strings.HasPrefix(name, "AGENTSERVER_A12_") || name == a12ImageGateEnvironment {
			continue
		}
		targetEnvironment = append(targetEnvironment, entry)
	}
	targetEnvironmentBytes := []byte(strings.Join(targetEnvironment, "\x00"))
	for label, forbidden := range map[string]string{
		"executor MCP endpoint":  os.Getenv(a12ExecutorMCPEnvironment),
		"worker credential path": os.Getenv(a12WorkerCredentialEnvironment),
		"worker result path":     os.Getenv(a12WorkerResultEnvironment),
	} {
		if forbidden == "" {
			return fmt.Errorf("%s is empty before final exec", label)
		}
		if bytes.Contains(targetEnvironmentBytes, []byte(forbidden)) {
			return fmt.Errorf("stock app-server environment retained %s", label)
		}
	}
	return finalexec.Execute(finalexec.Config{
		Program:         program,
		Arguments:       append([]string(nil), os.Args[1:]...),
		Directory:       directory,
		Environment:     targetEnvironment,
		ExpectedUID:     a12AppUID,
		ExpectedGID:     a12AppGID,
		RequiredOpenFDs: []int{trapFD},
	})
}

func runA12WorkerSubprocess() error {
	realUID, effectiveUID, savedUID := unix.Getresuid()
	if uint32(realUID) != a12WorkerUID || uint32(effectiveUID) != a12WorkerUID || uint32(savedUID) != a12WorkerUID {
		return fmt.Errorf("worker uid = real %d effective %d saved %d, want %d", realUID, effectiveUID, savedUID, a12WorkerUID)
	}
	realGID, effectiveGID, savedGID := unix.Getresgid()
	if uint32(realGID) != a12WorkerGID || uint32(effectiveGID) != a12WorkerGID || uint32(savedGID) != a12WorkerGID {
		return fmt.Errorf("worker gid = real %d effective %d saved %d, want %d", realGID, effectiveGID, savedGID, a12WorkerGID)
	}
	groups, err := os.Getgroups()
	if err != nil || len(groups) != 0 {
		return fmt.Errorf("worker supplementary groups = %v, error %v", groups, err)
	}
	if err := requireA12WorkerCapabilities(); err != nil {
		return err
	}
	if os.Getenv(a12WorkerSecretEnvironment) != a12ImageWorkerEnvironmentSecret {
		return errors.New("worker environment sensitivity sentinel is missing")
	}
	scenario, err := a12WorkerScenarioFromEnvironment()
	if err != nil {
		return err
	}
	credential, err := os.Open(os.Getenv(a12WorkerCredentialEnvironment))
	if err != nil {
		return fmt.Errorf("worker read credential: %w", err)
	}
	defer credential.Close()
	contents, err := io.ReadAll(io.LimitReader(credential, 1024))
	if err != nil {
		return fmt.Errorf("read worker credential contents: %w", err)
	}
	if string(contents) != a12ExecutorCapability {
		return errors.New("worker executor credential contents do not match the sensitivity sentinel")
	}
	executorCapability := string(contents)
	credentialFD := int(credential.Fd())
	if err := requireA12CloseOnExec(credentialFD); err != nil {
		return fmt.Errorf("worker credential descriptor: %w", err)
	}
	stagingContents, err := os.ReadFile(os.Getenv(a12WorkerStagingEnvironment))
	if err != nil {
		return fmt.Errorf("read worker staging contents: %w", err)
	}
	if string(stagingContents) != a12ImageWorkerStagingSecret {
		return errors.New("worker staging contents do not match the sensitivity sentinel")
	}
	control, err := net.DialTimeout("unix", os.Getenv(a12WorkerControlEnvironment), time.Second)
	if err != nil {
		return fmt.Errorf("worker connect control socket: %w", err)
	}
	defer control.Close()
	if err := requireA12NetworkFDIsCloseOnExec(control); err != nil {
		return fmt.Errorf("worker control descriptor: %w", err)
	}
	_ = control.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(control, a12ControlRequest); err != nil {
		return fmt.Errorf("worker control write: %w", err)
	}
	controlResponse, err := bufio.NewReader(control).ReadString('\n')
	if err != nil || controlResponse != a12ControlResponse {
		return fmt.Errorf("worker control response = %q, error %v", controlResponse, err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		Timeout:   2 * time.Second,
	}
	response, err := client.Get(os.Getenv(a12WorkerHarnessEnvironment))
	if err != nil {
		return fmt.Errorf("worker harness request: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Header.Get("X-Agentserver-A12") != a12HarnessMarker {
		return fmt.Errorf("worker harness response = %d/%q", response.StatusCode, response.Header.Get("X-Agentserver-A12"))
	}

	catalog, err := buildA12DynamicCatalog()
	if err != nil {
		return err
	}
	runContext, cancelRun := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancelRun()
	mcpClient, err := harnessworker.ConnectMCP(runContext, harnessworker.MCPClientConfig{
		Endpoint:              os.Getenv(a12ExecutorMCPEnvironment),
		BearerToken:           executorCapability,
		AllowInsecureLoopback: true,
		Namespace:             catalog.Namespace(),
		NamespaceDescription:  catalog.NamespaceDescription(),
		ExpectedCatalogDigest: catalog.Digest(),
		ExpectedCatalog:       catalog.CanonicalBytes(),
		Limits:                harnessworker.DefaultLimits(),
		CloseGrace:            2 * time.Second,
		ElicitationHandler: func(context.Context, harnessworker.ElicitationRequest) (harnessworker.ElicitationDecision, error) {
			return harnessworker.ElicitationDecision{Action: harnessworker.ApprovalCancel}, nil
		},
	})
	if err != nil {
		return fmt.Errorf("connect worker executor MCP: %w", err)
	}
	mcpClosed := false
	defer func() {
		if !mcpClosed {
			_ = mcpClient.Close()
		}
	}()
	bridge, err := harnessworker.NewDynamicBridge(mcpClient, 8, harnessworker.DefaultLimits().MaxArgumentBytes)
	if err != nil {
		return err
	}

	for label, target := range map[string]string{
		"app-only llmproxy": os.Getenv(a12LLMProxyEnvironment),
		"direct sink":       os.Getenv(a12DirectProbeEnvironment),
		"IPv6 sink":         os.Getenv(a12IPv6ProbeEnvironment),
		"redirect sink":     os.Getenv(a12RedirectProbeEnvironment),
	} {
		if err := requireA12WorkerHTTPDenied(client, label, target); err != nil {
			return err
		}
	}
	if address := os.Getenv(a12DNSProbeEnvironment); address != "" {
		connection, dialErr := net.DialTimeout("udp4", address, time.Second)
		if dialErr == nil {
			_, _ = connection.Write([]byte{0x12, 0x34, 0x01, 0x00, 0x00, 0x01})
			_ = connection.Close()
		}
	}

	trapReader, trapWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create worker final-exec trap: %w", err)
	}
	defer trapReader.Close()
	defer trapWriter.Close()
	trapFlags, err := unix.FcntlInt(trapWriter.Fd(), unix.F_GETFD, 0)
	if err != nil {
		trapWriter.Close()
		return fmt.Errorf("read worker trap descriptor flags: %w", err)
	}
	if _, err := unix.FcntlInt(trapWriter.Fd(), unix.F_SETFD, trapFlags&^unix.FD_CLOEXEC); err != nil {
		trapWriter.Close()
		return fmt.Errorf("clear worker trap close-on-exec: %w", err)
	}

	childEnvironment := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		switch name {
		case a12SubprocessModeEnvironment,
			a12TrapFDEnvironment,
			a12WorkerPIDEnvironment,
			a12WorkerFDEnvironment,
			a12WorkerSecretEnvironment:
			continue
		}
		childEnvironment = append(childEnvironment, entry)
	}
	// ExtraFiles maps the sole explicit inheritance trap to descriptor 3 in the
	// launcher. Credential/control descriptors are deliberately omitted and
	// have already been proven O_CLOEXEC above.
	const childTrapFD = 3
	childEnvironment = append(childEnvironment,
		a12SubprocessModeEnvironment+"="+a12FinalExecMode,
		a12TrapFDEnvironment+"="+strconv.Itoa(childTrapFD),
		a12WorkerPIDEnvironment+"="+strconv.Itoa(os.Getpid()),
		a12WorkerFDEnvironment+"="+strconv.Itoa(credentialFD),
	)
	childEnvironmentBytes := []byte(strings.Join(childEnvironment, "\x00"))
	for label, forbidden := range map[string]string{
		"executor capability": executorCapability,
		"worker environment":  a12ImageWorkerEnvironmentSecret,
	} {
		if bytes.Contains(childEnvironmentBytes, []byte(forbidden)) {
			return fmt.Errorf("app-server launcher environment contains %s", label)
		}
	}
	child, err := codexprocess.Start(runContext, codexprocess.Config{
		Binary:     os.Args[0],
		Args:       append([]string(nil), os.Args[1:]...),
		Dir:        "/",
		Env:        childEnvironment,
		Identity:   &codexprocess.Identity{UID: a12AppUID, GID: a12AppGID},
		ExtraFiles: []*os.File{trapWriter},
	})
	if err != nil {
		return fmt.Errorf("start app-server child through final exec: %w", err)
	}
	// One worker supervises exactly one app-server child. Once fork/exec has
	// copied the transition capabilities into that child, the worker no longer
	// needs them for stdio supervision, checkpointing, or wait. Error returns
	// intentionally do not try to signal the different-UID child: worker exit
	// triggers the child's Pdeathsig instead.
	if err := finalexec.SealIdentity(a12WorkerUID, a12WorkerGID); err != nil {
		return fmt.Errorf("seal worker after app-server launch: %w", err)
	}
	if err := trapWriter.Close(); err != nil {
		return fmt.Errorf("close worker trap writer: %w", err)
	}
	poll := []unix.PollFd{{Fd: int32(trapReader.Fd()), Events: unix.POLLIN | unix.POLLHUP}}
	ready, err := unix.Poll(poll, 5_000)
	if err != nil || ready != 1 {
		return fmt.Errorf("wait for final-exec close-all: ready=%d error=%v", ready, err)
	}
	buffer := make([]byte, 1)
	bytesRead, err := trapReader.Read(buffer)
	if !errors.Is(err, io.EOF) || bytesRead != 0 {
		return fmt.Errorf("final-exec trap remained open or emitted data: bytes=%d data=%q error=%v", bytesRead, buffer[:bytesRead], err)
	}
	runner, err := harnessworker.NewAppServerRunner(child.Peer, bridge, harnessworker.DefaultAppServerRunnerOptions())
	if err != nil {
		return err
	}
	eventsDone := make(chan struct{})
	go func() {
		_ = runner.ConsumeEvents(func(codexwire.Message) {})
		close(eventsDone)
	}()
	runResult, runErr := runner.Run(runContext, harnessworker.AppServerRunRequest{
		RunID:                "run-a12-image-" + scenario.name,
		RunAttemptGeneration: scenario.generation,
		ClientInfo: harnessworker.AppServerClientInfo{
			Name:    "agentserver_v2_a12_image",
			Title:   "agentserver v2 A12 image gate",
			Version: "0.0.0",
		},
		Catalog: catalog,
		Start: &harnessworker.AppServerThreadStart{
			Model:                 conformanceModelName,
			CWD:                   os.Getenv(a12FinalDirectoryEnvironment),
			BaseInstructions:      "Return only the scripted model result.",
			DeveloperInstructions: "Use only the frozen worker-owned executor callback.",
		},
		UserText: scenario.prompt,
	})
	<-eventsDone
	closeStdinErr := child.CloseStdin()
	waitErr := child.Wait(runContext)
	stderr, stderrTruncated := child.Stderr()
	closeMCPErr := mcpClient.Close()
	mcpClosed = true
	if stderrTruncated {
		return errors.New("app-server stderr exceeded the A12 bound")
	}
	for label, forbidden := range map[string]string{
		"executor capability":   executorCapability,
		"executor MCP endpoint": os.Getenv(a12ExecutorMCPEnvironment),
	} {
		if bytes.Contains(stderr, []byte(forbidden)) {
			return fmt.Errorf("app-server stderr contains %s", label)
		}
	}
	if runErr != nil {
		return fmt.Errorf("run app-server scenario %q: %w (child_stderr=%q)", scenario.name, runErr, stderr)
	}
	if closeStdinErr != nil {
		return fmt.Errorf("close app-server stdin: %w", closeStdinErr)
	}
	if waitErr != nil {
		return fmt.Errorf("wait for app-server child: %w (child_stderr=%q)", waitErr, stderr)
	}
	if closeMCPErr != nil {
		return fmt.Errorf("close worker executor MCP: %w", closeMCPErr)
	}
	if runResult.Terminal.Turn.Status != scenario.terminalStatus {
		return fmt.Errorf("scenario %q terminal = %q, want %q", scenario.name, runResult.Terminal.Turn.Status, scenario.terminalStatus)
	}
	if bridge.Outstanding() != 0 {
		return fmt.Errorf("scenario %q retained %d dynamic callbacks", scenario.name, bridge.Outstanding())
	}
	resultPath := os.Getenv(a12WorkerResultEnvironment)
	if resultPath == "" || !filepath.IsAbs(resultPath) {
		return errors.New("worker result path must be absolute")
	}
	encoded, err := json.Marshal(a12WorkerRunResult{
		Scenario:       scenario.name,
		TerminalStatus: runResult.Terminal.Turn.Status,
		ThreadID:       runResult.Thread.Thread.ID,
		RolloutPath:    runResult.Thread.Thread.Path,
	})
	if err != nil {
		return fmt.Errorf("encode worker result: %w", err)
	}
	if err := os.WriteFile(resultPath, encoded, 0o600); err != nil {
		return fmt.Errorf("write worker result: %w", err)
	}
	return nil
}

type a12WorkerScenario struct {
	name           string
	prompt         string
	terminalStatus string
	generation     int64
}

func a12WorkerScenarioFromEnvironment() (a12WorkerScenario, error) {
	switch value := os.Getenv(a12ScenarioEnvironment); value {
	case a12AllowedScenario:
		return a12WorkerScenario{name: value, prompt: "verify A12 allowed egress", terminalStatus: "completed", generation: 1}, nil
	case a12DirectScenario:
		return a12WorkerScenario{name: value, prompt: "attempt direct forbidden egress", terminalStatus: "failed", generation: 2}, nil
	case a12RedirectScenario:
		return a12WorkerScenario{name: value, prompt: "attempt cross-origin redirect egress", terminalStatus: "failed", generation: 3}, nil
	default:
		return a12WorkerScenario{}, fmt.Errorf("unknown A12 worker scenario %q", value)
	}
}

type a12WorkerRunResult struct {
	Scenario       string `json:"scenario"`
	TerminalStatus string `json:"terminalStatus"`
	ThreadID       string `json:"threadId"`
	RolloutPath    string `json:"rolloutPath"`
}

func buildA12DynamicCatalog() (*harnessworker.Catalog, error) {
	return harnessworker.BuildCatalog(
		executorDynamicNamespace,
		"Policy-approved deterministic executor tools.",
		[]harnessworker.ToolDescriptor{{
			Name:        approvedMCPToolName,
			Description: "Execute one approved deterministic instruction.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
				"required":             []string{"message"},
				"additionalProperties": false,
			},
		}},
		harnessworker.DefaultLimits(),
	)
}

func requireA12WorkerHTTPDenied(client *http.Client, label, target string) error {
	if target == "" {
		return fmt.Errorf("worker %s URL is empty", label)
	}
	response, err := client.Get(target)
	if err != nil {
		return nil
	}
	_ = response.Body.Close()
	return fmt.Errorf("worker reached forbidden %s with status %d", label, response.StatusCode)
}

func requireA12WorkerCapabilities() error {
	contents, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return fmt.Errorf("read worker process status: %w", err)
	}
	if len(contents) > 128*1024 {
		return errors.New("worker process status exceeds 128 KiB")
	}
	expected := uint64(1<<unix.CAP_SETGID | 1<<unix.CAP_SETUID)
	wanted := map[string]bool{
		"CapInh": false,
		"CapPrm": false,
		"CapEff": false,
		"CapAmb": false,
	}
	for _, line := range strings.Split(string(contents), "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if _, tracked := wanted[name]; !tracked {
			continue
		}
		parsed, parseErr := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if parseErr != nil {
			return fmt.Errorf("parse worker %s: %w", name, parseErr)
		}
		if parsed != expected {
			return fmt.Errorf("worker %s = %x, want only SETGID/SETUID (%x)", name, parsed, expected)
		}
		wanted[name] = true
	}
	for name, found := range wanted {
		if !found {
			return fmt.Errorf("worker process status omits %s", name)
		}
	}
	return nil
}

// TestAppServerA12ProductionIsolationImageGate exercises the combined final
// launch and egress boundary. The container entrypoint is a privileged init
// fixture only long enough to install nftables and create worker-owned state;
// every stock app-server process runs as the fixed app UID with no capabilities
// and reaches Codex only through finalexec close_range + exec.
func TestAppServerA12ProductionIsolationImageGate(t *testing.T) {
	platform := requireA12DisposableImage(t)
	binary, artifactPaths := prepareLiveCodex(t)
	assertA12CandidateArtifact(t, platform, binary, artifactPaths)
	catalog, err := buildA12DynamicCatalog()
	if err != nil {
		t.Fatal(err)
	}

	allowedToolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a12-allowed-tool",
		a12AllowedToolCallID,
		executorDynamicNamespace,
		approvedMCPToolName,
		`{"message":"verify approved MCP egress"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	allowedFinal, err := scriptedmodel.AssistantMessage(
		"response-a12-allowed-final",
		"message-a12-allowed-final",
		a12AllowedAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	directForbiddenResponse, err := scriptedmodel.AssistantMessage(
		"response-a12-direct-forbidden",
		"message-a12-direct-forbidden",
		"A12 direct forbidden sink was reached",
	)
	if err != nil {
		t.Fatal(err)
	}
	directForbidden, err := scriptedmodel.Start(scriptedmodel.Config{Responses: []scriptedmodel.Response{directForbiddenResponse}})
	if err != nil {
		t.Fatalf("start A12 direct forbidden sink: %v", err)
	}
	t.Cleanup(directForbidden.Close)
	redirectForbiddenResponse, err := scriptedmodel.AssistantMessage(
		"response-a12-redirect-forbidden",
		"message-a12-redirect-forbidden",
		"A12 redirected forbidden sink was reached",
	)
	if err != nil {
		t.Fatal(err)
	}
	redirectForbidden, err := scriptedmodel.Start(scriptedmodel.Config{Responses: []scriptedmodel.Response{redirectForbiddenResponse}})
	if err != nil {
		t.Fatalf("start A12 redirect forbidden sink: %v", err)
	}
	t.Cleanup(redirectForbidden.Close)
	llmproxyResponses := []scriptedmodel.Response{allowedToolCall, allowedFinal}
	for range 8 {
		llmproxyResponses = append(llmproxyResponses, scriptedmodel.Response{
			StatusCode:  http.StatusTemporaryRedirect,
			RedirectURL: redirectForbidden.URL() + "/v1/responses",
		})
	}
	llmproxy, err := scriptedmodel.Start(scriptedmodel.Config{Responses: llmproxyResponses})
	if err != nil {
		t.Fatalf("start A12 exact llmproxy fixture: %v", err)
	}
	t.Cleanup(llmproxy.Close)

	executorGateway := startWorkerMCPGateway(t, workerMCPGatewayConfig{
		BearerToken: a12ExecutorCapability,
		Catalog:     catalog,
		CallTool: func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if err := validateA12WorkerMCPCall(request, catalog); err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "approved worker MCP reached"}},
				StructuredContent: map[string]any{
					"marker": a12AllowedToolResultMarker,
				},
			}, nil
		},
	})

	harness := startA12WorkerHarness(t)
	dnsProbe := startA12DNSProbe(t)
	ipv6Probe := startA12IPv6Probe(t)
	workerState := createA12WorkerState(t)
	policies := []networkguard.UIDPolicy{
		{
			UID: a12WorkerUID,
			AllowedEndpoints: []networkguard.Endpoint{
				endpointFromURL(t, harness.URL()),
				endpointFromURL(t, executorGateway.Endpoint()),
			},
		},
		{
			UID: a12AppUID,
			AllowedEndpoints: []networkguard.Endpoint{
				endpointFromURL(t, llmproxy.URL()),
			},
		},
	}
	if err := networkguard.Install("agentserver_a12", policies); err != nil {
		t.Fatalf("install A12 per-UID nftables policy: %v", err)
	}
	if err := networkguard.Install("agentserver_a12", policies); err != nil {
		t.Fatalf("retry exact A12 per-UID nftables policy: %v", err)
	}
	for _, scenario := range []struct {
		name     string
		modelURL string
	}{
		{name: a12AllowedScenario, modelURL: llmproxy.URL()},
		{name: a12DirectScenario, modelURL: directForbidden.URL()},
		{name: a12RedirectScenario, modelURL: llmproxy.URL()},
	} {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			paths := prepareA12AppPaths(t)
			writeScriptedModelConfigWithOptions(t, paths.codexHome, scenario.modelURL, scriptedModelConfigOptions{disableUpdatePlan: true})
			assertA12AppCredentialBoundary(t, paths, executorGateway.Endpoint())
			chownA12AppTree(t, paths.root)
			result := runA12WorkerScenario(
				t,
				binary,
				paths,
				workerState,
				scenario.name,
				harness.URL(),
				executorGateway.Endpoint(),
				llmproxy.URL(),
				directForbidden.URL(),
				redirectForbidden.URL(),
				dnsProbe.Address(),
				ipv6Probe.URL(),
			)
			rollout := readA12Rollout(t, paths.codexHome, result.RolloutPath)
			if bytes.Contains(rollout, []byte(a12ExecutorCapability)) || bytes.Contains(rollout, []byte(executorGateway.Endpoint())) {
				t.Fatalf("A12 %s rollout contains worker executor credential material", scenario.name)
			}
			if scenario.name == a12AllowedScenario {
				assertCompleteRolloutJSONL(
					t,
					result.RolloutPath,
					result.ThreadID,
					"verify A12 allowed egress",
					a12AllowedAssistantText,
					a12AllowedToolCallID,
					a12AllowedToolResultMarker,
				)
			}
		})
	}

	workerState.assertControlCount(t, a12ScenarioCount)
	if got := harness.RequestCount(); got != a12ScenarioCount {
		t.Fatalf("A12 worker-only harness requests = %d, want one sensitivity request from each real worker", got)
	}
	dnsProbe.stop(t)
	if got := dnsProbe.RequestCount(); got != 0 {
		t.Fatalf("A12 forbidden DNS-shaped sink received %d app packets", got)
	}
	if got := ipv6Probe.RequestCount(); got != 0 {
		t.Fatalf("A12 forbidden IPv6 sink received %d app requests", got)
	}
	if failures := llmproxy.Failures(); len(failures) != 0 {
		t.Fatalf("A12 exact llmproxy failures: %v", failures)
	}
	modelRequests := llmproxy.Requests()
	if got := len(modelRequests); got < 3 || got > len(llmproxyResponses) {
		t.Fatalf("A12 exact llmproxy requests = %d, want two allowed responses plus at least one bounded redirect source request", got)
	}
	for index, request := range modelRequests {
		headers, err := json.Marshal(request.Header)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(headers, []byte(a12ExecutorCapability)) || bytes.Contains(request.Body, []byte(a12ExecutorCapability)) {
			t.Fatalf("A12 model request %d contains the worker executor capability", index)
		}
	}
	initialRequest := decodeCapturedModelRequest(t, modelRequests[0])
	if got, want := modelToolNames(t, initialRequest.Tools), []string{executorDynamicNamespace + "." + approvedMCPToolName}; !reflect.DeepEqual(got, want) {
		t.Fatalf("A12 model tool surface = %v, want %v", got, want)
	}
	followupRequest := decodeCapturedModelRequest(t, modelRequests[1])
	if !modelInputContainsFunctionOutput(followupRequest.Input, a12AllowedToolCallID, a12AllowedToolResultMarker) {
		t.Fatalf("A12 model followup omitted worker MCP result: input=%s", encodeModelInput(t, followupRequest.Input))
	}
	executorGateway.AssertAuthenticated(t)
	if calls := executorGateway.ToolCalls(); calls != 1 {
		t.Fatalf("A12 worker executor MCP calls = %d, want one", calls)
	}
	assertA12ForbiddenModelSinkUntouched(t, "direct", directForbidden)
	assertA12ForbiddenModelSinkUntouched(t, "redirect", redirectForbidden)
	t.Logf("A12 worker-owned MCP image isolation passed: platform=%s release=%s app_uid=%d worker_uid=%d", platform, os.Getenv(a12ExpectedReleaseEnvironment), a12AppUID, a12WorkerUID)
}

func requireA12DisposableImage(t *testing.T) string {
	t.Helper()
	if os.Getenv(a12ImageGateEnvironment) != "1" {
		t.Skip("run through conformance/image/a12/run.sh; A12 requires a disposable Linux image")
	}
	if os.Geteuid() != 0 {
		t.Fatal("A12 image init fixture must start as container root")
	}
	requireA12InitCapability(t, unix.CAP_DAC_READ_SEARCH)
	platform := os.Getenv(a12ExpectedPlatformEnvironment)
	if platform != "linux-amd64" && platform != "linux-arm64" {
		t.Fatalf("invalid A12 expected platform %q", platform)
	}
	if got := runtimelock.CurrentPlatform(); got != platform {
		t.Fatalf("A12 image platform = %q, want %q", got, platform)
	}
	rootMount := requireA04Mount(t, "/")
	if !rootMount.hasOption("ro") {
		t.Fatalf("A12 image root mount is not read-only: %s", rootMount.options)
	}
	for _, mountPoint := range []string{"/tmp", a12RuntimeDirectory} {
		if os.Getenv(a12HardenTmpfsEnvironment) == "1" {
			before := requireA04Mount(t, mountPoint)
			if before.filesystem != "tmpfs" {
				t.Fatalf("refusing to remount A12 %s filesystem %q", mountPoint, before.filesystem)
			}
			flags := uintptr(syscall.MS_REMOUNT | syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC)
			if err := syscall.Mount("", mountPoint, "", flags, ""); err != nil {
				t.Fatalf("harden A12 tmpfs %s: %v", mountPoint, err)
			}
		}
		mount := requireA04Mount(t, mountPoint)
		if mount.filesystem != "tmpfs" || !mount.hasOption("rw") || !mount.hasOption("nosuid") || !mount.hasOption("nodev") || !mount.hasOption("noexec") {
			t.Fatalf("A12 mount %s = filesystem %q options %q, want rw,nosuid,nodev,noexec tmpfs", mountPoint, mount.filesystem, mount.options)
		}
	}
	for _, forbidden := range []string{"/workspace", "/workspaces", "/var/run/secrets/kubernetes.io/serviceaccount"} {
		if _, err := os.Lstat(forbidden); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("A12 image contains forbidden mount path %q: %v", forbidden, err)
		}
	}
	return platform
}

func requireA12InitCapability(t *testing.T, capability int) {
	t.Helper()
	contents, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatalf("read A12 init process status: %v", err)
	}
	for _, line := range strings.Split(string(contents), "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found || name != "CapEff" {
			continue
		}
		parsed, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil {
			t.Fatalf("parse A12 init CapEff: %v", err)
		}
		if parsed&(uint64(1)<<uint(capability)) == 0 {
			t.Fatalf("A12 init CapEff = %x, missing capability %d required for post-exit read-only rollout inspection", parsed, capability)
		}
		return
	}
	t.Fatal("A12 init process status omits CapEff")
}

func assertA12CandidateArtifact(t *testing.T, platform, binary string, paths livePaths) {
	t.Helper()
	release := os.Getenv(a12ExpectedReleaseEnvironment)
	digest := os.Getenv(a12ExpectedDigestEnvironment)
	sizeText := os.Getenv(a12ExpectedSizeEnvironment)
	if _, characterized := characterizedA03Releases[release]; !characterized {
		t.Fatalf("A12 release %q lacks app-server characterization", release)
	}
	if !a12SHA256Pattern.MatchString(digest) {
		t.Fatalf("invalid A12 Codex digest %q", digest)
	}
	size, err := strconv.ParseInt(sizeText, 10, 64)
	if err != nil || size < 1 || strconv.FormatInt(size, 10) != sizeText {
		t.Fatalf("invalid A12 Codex size %q", sizeText)
	}
	if got := candidateRelease(t, binary, paths); got != release {
		t.Fatalf("A12 Codex release = %q, want %q", got, release)
	}
	gotDigest, gotSize, err := runtimelock.HashFile(binary)
	if err != nil {
		t.Fatalf("hash A12 Codex artifact: %v", err)
	}
	if gotDigest != digest || gotSize != size {
		t.Fatalf("A12 Codex artifact = %s/%d, want %s/%d", gotDigest, gotSize, digest, size)
	}
	t.Logf("A12 candidate artifact: platform=%s release=%s sha256=%s size=%d", platform, release, digest, size)
}

type a12WorkerState struct {
	root             string
	credentialPath   string
	resultPath       string
	stagingPath      string
	controlPath      string
	listener         *net.UnixListener
	controlCount     atomic.Int64
	controlError     chan error
	controlCloseOnce sync.Once
}

func createA12WorkerState(t *testing.T) *a12WorkerState {
	t.Helper()
	root := filepath.Join(a12RuntimeDirectory, "worker")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("create A12 worker state: %v", err)
	}
	credentialPath := filepath.Join(root, "credential")
	if err := os.WriteFile(credentialPath, []byte(a12ExecutorCapability), 0o600); err != nil {
		t.Fatalf("write A12 worker credential: %v", err)
	}
	resultPath := filepath.Join(root, "result.json")
	if err := os.WriteFile(resultPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write A12 worker result placeholder: %v", err)
	}
	stagingDirectory := filepath.Join(root, "staging")
	if err := os.Mkdir(stagingDirectory, 0o700); err != nil {
		t.Fatalf("create A12 worker staging: %v", err)
	}
	stagingPath := filepath.Join(stagingDirectory, "checkpoint")
	if err := os.WriteFile(stagingPath, []byte(a12ImageWorkerStagingSecret), 0o600); err != nil {
		t.Fatalf("write A12 worker staging sentinel: %v", err)
	}
	for _, path := range []string{credentialPath, resultPath, stagingPath, stagingDirectory} {
		if err := os.Chown(path, int(a12WorkerUID), int(a12WorkerGID)); err != nil {
			t.Fatalf("own A12 worker path %s: %v", path, err)
		}
	}
	controlPath := filepath.Join(root, "control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: controlPath, Net: "unix"})
	if err != nil {
		t.Fatalf("listen on A12 worker control socket: %v", err)
	}
	if err := os.Chmod(controlPath, 0o600); err != nil {
		listener.Close()
		t.Fatalf("mode A12 worker control socket: %v", err)
	}
	if err := os.Chown(controlPath, int(a12WorkerUID), int(a12WorkerGID)); err != nil {
		listener.Close()
		t.Fatalf("own A12 worker control socket: %v", err)
	}
	if err := os.Chown(root, int(a12WorkerUID), int(a12WorkerGID)); err != nil {
		listener.Close()
		t.Fatalf("own A12 worker state: %v", err)
	}
	state := &a12WorkerState{
		root:           root,
		credentialPath: credentialPath,
		resultPath:     resultPath,
		stagingPath:    stagingPath,
		controlPath:    controlPath,
		listener:       listener,
		controlError:   make(chan error, 1),
	}
	t.Cleanup(func() { state.closeControl() })
	go state.serveControl()
	return state
}

func (state *a12WorkerState) serveControl() {
	for index := 0; index < a12ScenarioCount; index++ {
		connection, err := state.listener.AcceptUnix()
		if err != nil {
			state.controlError <- err
			return
		}
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		request, err := bufio.NewReader(connection).ReadString('\n')
		if err == nil && request != a12ControlRequest {
			err = fmt.Errorf("A12 control request %d = %q", index+1, request)
		}
		if err == nil {
			_, err = io.WriteString(connection, a12ControlResponse)
		}
		_ = connection.Close()
		if err != nil {
			state.controlError <- err
			return
		}
		state.controlCount.Add(1)
	}
	state.controlError <- nil
}

func (state *a12WorkerState) closeControl() {
	state.controlCloseOnce.Do(func() { _ = state.listener.Close() })
}

func (state *a12WorkerState) assertControlCount(t *testing.T, want int64) {
	t.Helper()
	select {
	case err := <-state.controlError:
		if err != nil {
			t.Fatalf("A12 worker control fixture: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("A12 worker control fixture did not finish")
	}
	if got := state.controlCount.Load(); got != want {
		t.Fatalf("A12 worker control requests = %d, want %d", got, want)
	}
}

func runA12WorkerScenario(
	t *testing.T,
	binary string,
	paths livePaths,
	state *a12WorkerState,
	scenario string,
	harnessURL string,
	executorMCPURL string,
	llmproxyURL string,
	directProbeURL string,
	redirectProbeURL string,
	dnsAddress string,
	ipv6URL string,
) a12WorkerRunResult {
	t.Helper()
	launcherEnvironment := append([]string(nil), paths.environment...)
	launcherEnvironment = append(launcherEnvironment,
		a12SubprocessModeEnvironment+"="+a12WorkerMode,
		a12FinalProgramEnvironment+"="+binary,
		a12FinalDirectoryEnvironment+"="+paths.cwd,
		a12WorkerCredentialEnvironment+"="+state.credentialPath,
		a12WorkerResultEnvironment+"="+state.resultPath,
		a12WorkerStagingEnvironment+"="+state.stagingPath,
		a12WorkerControlEnvironment+"="+state.controlPath,
		a12WorkerHarnessEnvironment+"="+harnessURL,
		a12ExecutorMCPEnvironment+"="+executorMCPURL,
		a12LLMProxyEnvironment+"="+llmproxyURL,
		a12DirectProbeEnvironment+"="+directProbeURL,
		a12RedirectProbeEnvironment+"="+redirectProbeURL,
		a12ScenarioEnvironment+"="+scenario,
		a12DNSProbeEnvironment+"="+dnsAddress,
		a12IPv6ProbeEnvironment+"="+ipv6URL,
		a12WorkerSecretEnvironment+"="+a12ImageWorkerEnvironmentSecret,
	)
	processContext, cancelProcess := context.WithTimeout(context.Background(), 45*time.Second)
	process, err := codexprocess.Start(processContext, codexprocess.Config{
		Binary:   os.Args[0],
		Args:     []string{"app-server", "--listen", "stdio://", "--strict-config"},
		Dir:      "/",
		Env:      launcherEnvironment,
		Identity: &codexprocess.Identity{UID: a12WorkerUID, GID: a12WorkerGID, AllowSetID: true},
	})
	if err != nil {
		cancelProcess()
		t.Fatalf("start A12 worker-supervised app-server: %v", err)
	}
	defer cancelProcess()
	if err := process.CloseStdin(); err != nil {
		_ = process.Kill()
		t.Fatalf("close A12 worker stdin: %v", err)
	}
	waitErr := process.Wait(processContext)
	stderr, truncated := process.Stderr()
	if truncated {
		t.Fatal("A12 worker stderr exceeded the probe bound")
	}
	if bytes.Contains(stderr, []byte(a12ExecutorCapability)) {
		t.Fatal("A12 worker stderr contains the executor capability")
	}
	if waitErr != nil {
		t.Fatalf("A12 worker scenario %q failed: %v\nstderr: %s", scenario, waitErr, stderr)
	}
	contents, err := os.ReadFile(state.resultPath)
	if err != nil {
		t.Fatalf("read A12 worker scenario result: %v", err)
	}
	if len(contents) == 0 || len(contents) > 64*1024 {
		t.Fatalf("A12 worker result size = %d", len(contents))
	}
	var result a12WorkerRunResult
	if err := json.Unmarshal(contents, &result); err != nil {
		t.Fatalf("decode A12 worker result: %v", err)
	}
	if result.Scenario != scenario || result.TerminalStatus == "" || result.ThreadID == "" || !filepath.IsAbs(result.RolloutPath) {
		t.Fatalf("A12 worker result = %+v", result)
	}
	wantStatus := "failed"
	if scenario == a12AllowedScenario {
		wantStatus = "completed"
	}
	if result.TerminalStatus != wantStatus {
		t.Fatalf("A12 worker scenario %q terminal = %q, want %q", scenario, result.TerminalStatus, wantStatus)
	}
	return result
}

func validateA12WorkerMCPCall(request *mcp.CallToolRequest, catalog *harnessworker.Catalog) error {
	if request == nil || request.Params == nil {
		return errors.New("A12 worker MCP call has no params")
	}
	if request.Params.Name != approvedMCPToolName {
		return fmt.Errorf("A12 worker MCP tool = %q", request.Params.Name)
	}
	var arguments struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
		return fmt.Errorf("decode A12 worker MCP arguments: %w", err)
	}
	if arguments.Message != "verify approved MCP egress" {
		return fmt.Errorf("A12 worker MCP message = %q", arguments.Message)
	}
	metadataBytes, err := json.Marshal(request.Params.Meta)
	if err != nil {
		return fmt.Errorf("encode A12 worker MCP metadata: %w", err)
	}
	var metadata struct {
		RunID                string `json:"io.agentserver/runId"`
		ThreadID             string `json:"io.agentserver/threadId"`
		TurnID               string `json:"io.agentserver/turnId"`
		CallID               string `json:"io.agentserver/callId"`
		RunAttemptGeneration int64  `json:"io.agentserver/runAttemptGeneration"`
		ToolCatalogDigest    string `json:"io.agentserver/toolCatalogDigest"`
	}
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return fmt.Errorf("decode A12 worker MCP metadata: %w", err)
	}
	if metadata.RunID != "run-a12-image-"+a12AllowedScenario || metadata.ThreadID == "" || metadata.TurnID == "" ||
		metadata.CallID != a12AllowedToolCallID || metadata.RunAttemptGeneration != 1 ||
		metadata.ToolCatalogDigest != catalog.Digest() {
		return fmt.Errorf("A12 worker MCP metadata = %+v", metadata)
	}
	return nil
}

func assertA12AppCredentialBoundary(t *testing.T, paths livePaths, executorEndpoint string) {
	t.Helper()
	environment := []byte(strings.Join(paths.environment, "\x00"))
	for label, forbidden := range map[string]string{
		"executor capability":   a12ExecutorCapability,
		"executor MCP endpoint": executorEndpoint,
	} {
		if bytes.Contains(environment, []byte(forbidden)) {
			t.Fatalf("A12 stock child environment contains %s", label)
		}
	}
	config, err := os.ReadFile(filepath.Join(paths.codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("read A12 stock app-server config: %v", err)
	}
	for label, forbidden := range map[string]string{
		"executor capability":   a12ExecutorCapability,
		"executor MCP endpoint": executorEndpoint,
		"Codex MCP server":      "[mcp_servers.",
	} {
		if bytes.Contains(config, []byte(forbidden)) {
			t.Fatalf("A12 stock app-server config contains %s", label)
		}
	}
}

func readA12Rollout(t *testing.T, codexHome, path string) []byte {
	t.Helper()
	relative := stateRelativePath(t, codexHome, path)
	if !strings.HasSuffix(relative, ".jsonl") {
		t.Fatalf("A12 rollout path = %q", relative)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat A12 rollout: %v", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxA08StateFileBytes {
		t.Fatalf("A12 rollout mode/size = %s/%d", info.Mode(), info.Size())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read A12 rollout: %v", err)
	}
	return contents
}

func chownA12AppTree(t *testing.T, root string) {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("A12 app tree contains symlink %q", path)
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("inspect A12 app tree before ownership transfer: %v", err)
	}
	for index := len(paths) - 1; index >= 0; index-- {
		if err := os.Chown(paths[index], int(a12AppUID), int(a12AppGID)); err != nil {
			t.Fatalf("own A12 app path %s: %v", paths[index], err)
		}
	}
}

func prepareA12AppPaths(t *testing.T) livePaths {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "agentserver-a12-app-")
	if err != nil {
		t.Fatalf("create A12 app runtime root: %v", err)
	}
	// This is a disposable image. Deliberately do not register testing.TempDir
	// cleanup: after ownership transfer the capability-bounded init fixture
	// cannot traverse the app's 0700 tree, and the container/tmpfs teardown is
	// the authoritative cleanup boundary.
	paths := livePaths{
		root:      root,
		home:      filepath.Join(root, "home"),
		codexHome: filepath.Join(root, "codex-home"),
		temporary: filepath.Join(root, "tmp"),
		cwd:       filepath.Join(root, "cwd"),
	}
	for _, directory := range []string{paths.home, paths.codexHome, paths.temporary, paths.cwd} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("create A12 app runtime directory: %v", err)
		}
	}
	environment, err := codexprocess.Environment(paths.home, paths.codexHome, paths.temporary, nil)
	if err != nil {
		t.Fatal(err)
	}
	paths.environment = environment
	return paths
}

func endpointFromURL(t *testing.T, rawURL string) networkguard.Endpoint {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse A12 endpoint %q: %v", rawURL, err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split A12 endpoint %q: %v", rawURL, err)
	}
	address, err := netip.ParseAddr(host)
	if err != nil || !address.Is4() {
		t.Fatalf("A12 endpoint address = %q, error %v", host, err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		t.Fatalf("A12 endpoint port = %q, error %v", portText, err)
	}
	return networkguard.Endpoint{Address: address, Port: uint16(port)}
}

type a12HTTPFixture struct {
	server    *http.Server
	listener  net.Listener
	url       string
	requests  atomic.Int64
	closeOnce sync.Once
	done      chan struct{}
}

func startA12WorkerHarness(t *testing.T) *a12HTTPFixture {
	t.Helper()
	return startA12HTTPFixture(t, "tcp4", "127.0.0.1:0", "/control")
}

func startA12IPv6Probe(t *testing.T) *a12HTTPFixture {
	t.Helper()
	fixture := startA12HTTPFixture(t, "tcp6", "[::1]:0", "/forbidden")
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		Timeout:   time.Second,
	}
	response, err := client.Get(fixture.URL())
	if err != nil {
		t.Fatalf("reach A12 IPv6 sensitivity sink as init fixture: %v", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent || fixture.RequestCount() != 1 {
		t.Fatalf("A12 IPv6 sensitivity response = status %d requests %d", response.StatusCode, fixture.RequestCount())
	}
	fixture.requests.Store(0)
	return fixture
}

func startA12HTTPFixture(t *testing.T, network, address, path string) *a12HTTPFixture {
	t.Helper()
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Fatalf("listen for A12 %s fixture at %s: %v", network, address, err)
	}
	fixture := &a12HTTPFixture{listener: listener, url: "http://" + listener.Addr().String() + path, done: make(chan struct{})}
	fixture.server = &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			fixture.requests.Add(1)
			writer.Header().Set("X-Agentserver-A12", a12HarnessMarker)
			writer.WriteHeader(http.StatusNoContent)
		}),
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
		WriteTimeout:      time.Second,
		IdleTimeout:       time.Second,
	}
	go func() {
		defer close(fixture.done)
		_ = fixture.server.Serve(listener)
	}()
	t.Cleanup(fixture.Close)
	return fixture
}

func (fixture *a12HTTPFixture) URL() string { return fixture.url }

func (fixture *a12HTTPFixture) RequestCount() int64 { return fixture.requests.Load() }

func (fixture *a12HTTPFixture) Close() {
	fixture.closeOnce.Do(func() {
		_ = fixture.server.Close()
		<-fixture.done
	})
}

type a12DNSFixture struct {
	connection *net.UDPConn
	address    string
	requests   atomic.Int64
	probe      chan struct{}
	done       chan struct{}
	closeOnce  sync.Once
}

func startA12DNSProbe(t *testing.T) *a12DNSFixture {
	t.Helper()
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen for A12 DNS-shaped sink: %v", err)
	}
	fixture := &a12DNSFixture{
		connection: connection,
		address:    connection.LocalAddr().String(),
		probe:      make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
	go func() {
		defer close(fixture.done)
		buffer := make([]byte, 512)
		for {
			if _, _, err := connection.ReadFromUDP(buffer); err != nil {
				return
			}
			fixture.requests.Add(1)
			select {
			case fixture.probe <- struct{}{}:
			default:
			}
		}
	}()
	probe, err := net.Dial("udp4", fixture.address)
	if err != nil {
		connection.Close()
		t.Fatalf("dial A12 DNS sensitivity fixture: %v", err)
	}
	_, err = probe.Write([]byte("root-sensitivity"))
	_ = probe.Close()
	if err != nil {
		connection.Close()
		t.Fatalf("write A12 DNS sensitivity fixture: %v", err)
	}
	select {
	case <-fixture.probe:
		fixture.requests.Store(0)
	case <-time.After(time.Second):
		connection.Close()
		t.Fatal("A12 DNS sensitivity packet was not observed")
	}
	t.Cleanup(func() { fixture.close() })
	return fixture
}

func (fixture *a12DNSFixture) Address() string { return fixture.address }

func (fixture *a12DNSFixture) RequestCount() int64 { return fixture.requests.Load() }

func (fixture *a12DNSFixture) close() {
	fixture.closeOnce.Do(func() {
		_ = fixture.connection.Close()
		<-fixture.done
	})
}

func (fixture *a12DNSFixture) stop(t *testing.T) {
	t.Helper()
	fixture.close()
}

func assertA12ForbiddenModelSinkUntouched(t *testing.T, label string, server *scriptedmodel.Server) {
	t.Helper()
	if failures := server.Failures(); len(failures) != 0 {
		t.Fatalf("A12 %s forbidden sink failures: %v", label, failures)
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Fatalf("A12 %s forbidden sink received %d requests", label, len(requests))
	}
}

func parseA12PositiveInt(name string) (int, error) {
	text := os.Getenv(name)
	value, err := strconv.Atoi(text)
	if err != nil || value < 1 || strconv.Itoa(value) != text {
		return 0, fmt.Errorf("invalid %s %q", name, text)
	}
	return value, nil
}

func requireA12CloseOnExec(descriptor int) error {
	flags, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)
	if err != nil {
		return err
	}
	if flags&unix.FD_CLOEXEC == 0 {
		return errors.New("descriptor omits FD_CLOEXEC")
	}
	return nil
}

func requireA12OnlyDeliberateInheritedFD(trapFD int, forbiddenPaths []string) error {
	trapFlags, err := unix.FcntlInt(uintptr(trapFD), unix.F_GETFD, 0)
	if err != nil {
		return fmt.Errorf("inspect inherited trap descriptor: %w", err)
	}
	if trapFlags&unix.FD_CLOEXEC != 0 {
		return errors.New("inherited trap unexpectedly has FD_CLOEXEC")
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return fmt.Errorf("enumerate final-exec descriptors: %w", err)
	}
	if len(entries) > 256 {
		return fmt.Errorf("final-exec descriptor count = %d, exceeds 256", len(entries))
	}
	for _, entry := range entries {
		descriptor, err := strconv.Atoi(entry.Name())
		if err != nil || descriptor <= 2 || descriptor == trapFD {
			continue
		}
		flags, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)
		if errors.Is(err, unix.EBADF) {
			// os.ReadDir's own descriptor may be present in the snapshot but is
			// closed before the returned entries are inspected.
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect final-exec descriptor %d: %w", descriptor, err)
		}
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil {
			return fmt.Errorf("resolve final-exec descriptor %d: %w", descriptor, err)
		}
		for _, forbidden := range forbiddenPaths {
			if forbidden != "" && (target == forbidden || target == forbidden+" (deleted)") {
				return fmt.Errorf("worker-only path reached final-exec descriptor %d", descriptor)
			}
		}
		if flags&unix.FD_CLOEXEC == 0 {
			matchesStdio, err := a12DescriptorMatchesAny(descriptor, []int{0, 1, 2, trapFD})
			if err != nil {
				return err
			}
			if strings.HasPrefix(target, "pipe:[") && matchesStdio {
				// os/exec may retain a duplicate of a mapped stdio/trap pipe in
				// the intermediate Go trampoline. It carries no additional
				// object and is still removed by the mandatory close_range.
				continue
			}
			return fmt.Errorf("unexpected non-CLOEXEC descriptor %d reached final-exec: target=%q", descriptor, target)
		}
	}
	return nil
}

func a12DescriptorMatchesAny(descriptor int, allowed []int) (bool, error) {
	var candidate unix.Stat_t
	if err := unix.Fstat(descriptor, &candidate); err != nil {
		return false, fmt.Errorf("stat final-exec descriptor %d: %w", descriptor, err)
	}
	for _, allowedDescriptor := range allowed {
		var expected unix.Stat_t
		if err := unix.Fstat(allowedDescriptor, &expected); err != nil {
			return false, fmt.Errorf("stat allowed final-exec descriptor %d: %w", allowedDescriptor, err)
		}
		if candidate.Dev == expected.Dev && candidate.Ino == expected.Ino {
			return true, nil
		}
	}
	return false, nil
}

func requireA12NetworkFDIsCloseOnExec(connection net.Conn) error {
	syscallConnection, ok := connection.(syscall.Conn)
	if !ok {
		return errors.New("network connection has no syscall descriptor")
	}
	raw, err := syscallConnection.SyscallConn()
	if err != nil {
		return err
	}
	var checkErr error
	if err := raw.Control(func(descriptor uintptr) {
		checkErr = requireA12CloseOnExec(int(descriptor))
	}); err != nil {
		return err
	}
	return checkErr
}
