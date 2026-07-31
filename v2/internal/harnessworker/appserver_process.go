package harnessworker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/finalexec"
)

const (
	AppServerModelCapabilityEnvironment = "AGENTSERVER_LLM_CAPABILITY"

	defaultAppServerIncomingFrames = 64
	defaultAppServerStderrBytes    = 1024 * 1024
	maximumAppServerIncomingFrames = 4 * 1024
	maximumAppServerStderrBytes    = 64 * 1024 * 1024
	maximumModelCapabilityBytes    = 16 * 1024
)

// AppServerRuntimeEnvironment is the complete environment authority exposed
// to stock app-server. In particular, executor MCP and worker control
// credentials have no representation in this type.
type AppServerRuntimeEnvironment struct {
	Home            string
	CodexHome       string
	Temporary       string
	ModelCapability string
}

// AppServerProcessConfig selects only deployment-owned, pinned process facts.
// FinalExecExecutable is the agentserver close-all trampoline, not stock Codex.
type AppServerProcessConfig struct {
	FinalExecExecutable string
	CodexExecutable     string
	Directory           string
	Environment         AppServerRuntimeEnvironment
	WorkerUID           uint32
	WorkerGID           uint32
	AppUID              uint32
	AppGID              uint32
	MaxFrameBytes       int
	IncomingFrames      int
	MaxStderrBytes      int
}

// AppServerProcess is the worker's single stdio owner for one stock
// app-server child. It implements AppServerTransport and deliberately exposes
// no generic command execution surface.
type AppServerProcess struct {
	peer    *codexwire.Peer
	command *exec.Cmd
	stdin   io.WriteCloser
	stderr  *boundedAppServerCapture

	writeMu     sync.Mutex
	stdinClosed bool
	stdinErr    error
	waitDone    chan struct{}
	stderrDone  chan struct{}
	waitErr     error
}

type appServerProcessBounds struct {
	maxFrameBytes  int
	incomingFrames int
	maxStderrBytes int
}

// StartAppServerProcess forks the final-exec trampoline as the fixed app UID,
// starts bounded stdio drains, and then permanently seals the worker's own
// identity. A worker is one-shot and must never call this function twice.
func StartAppServerProcess(ctx context.Context, config AppServerProcessConfig) (*AppServerProcess, error) {
	if ctx == nil {
		return nil, errors.New("app-server process context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalized, environment, err := validateAppServerProcessConfig(config)
	if err != nil {
		return nil, err
	}
	command := exec.Command(
		normalized.FinalExecExecutable,
		finalexec.AppServerArguments(
			normalized.CodexExecutable,
			normalized.Directory,
			normalized.AppUID,
			normalized.AppGID,
		)...,
	)
	command.Dir = normalized.Directory
	command.Env = environment
	if err := configureAppServerFinalExecCommand(command, normalized.AppUID, normalized.AppGID); err != nil {
		return nil, err
	}
	return startAppServerCommand(ctx, command, appServerProcessBounds{
		maxFrameBytes: normalized.MaxFrameBytes, incomingFrames: normalized.IncomingFrames,
		maxStderrBytes: normalized.MaxStderrBytes,
	}, func() error {
		return finalexec.SealIdentity(normalized.WorkerUID, normalized.WorkerGID)
	})
}

func startAppServerCommand(
	ctx context.Context,
	command *exec.Cmd,
	bounds appServerProcessBounds,
	afterStart func() error,
) (*AppServerProcess, error) {
	if ctx == nil {
		return nil, errors.New("app-server command context is required")
	}
	if command == nil {
		return nil, errors.New("app-server command is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateAppServerProcessBounds(bounds); err != nil {
		return nil, err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open app-server stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("open app-server stderr: %w", err)
	}
	peer, err := codexwire.NewPeer(stdout, stdin, bounds.maxFrameBytes, bounds.incomingFrames)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("create app-server stdio peer: %w", err)
	}
	process := &AppServerProcess{
		peer: peer, command: command, stdin: stdin,
		stderr:   newBoundedAppServerCapture(bounds.maxStderrBytes),
		waitDone: make(chan struct{}), stderrDone: make(chan struct{}),
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start app-server final exec: %w", err)
	}
	go func() {
		_, _ = io.Copy(process.stderr, stderr)
		close(process.stderrDone)
	}()
	go func() {
		process.waitErr = command.Wait()
		close(process.waitDone)
	}()
	if afterStart != nil {
		if err := afterStart(); err != nil {
			closeErr := process.CloseStdin()
			killErr := command.Process.Kill()
			return nil, errors.Join(fmt.Errorf("seal worker after app-server launch: %w", err), closeErr, killErr)
		}
	}
	return process, nil
}

func (process *AppServerProcess) Send(value any) error {
	if process == nil || process.peer == nil {
		return errors.New("app-server process is required")
	}
	process.writeMu.Lock()
	defer process.writeMu.Unlock()
	if process.stdinClosed {
		return io.ErrClosedPipe
	}
	return process.peer.Send(value)
}

func (process *AppServerProcess) Receive(ctx context.Context) (codexwire.Message, error) {
	if process == nil || process.peer == nil {
		return codexwire.Message{}, errors.New("app-server process is required")
	}
	return process.peer.Receive(ctx)
}

// CloseStdin is the graceful app-server shutdown boundary after turn terminal
// cleanup. It is idempotent and serialized with every protocol write.
func (process *AppServerProcess) CloseStdin() error {
	if process == nil || process.stdin == nil {
		return errors.New("app-server process is required")
	}
	process.writeMu.Lock()
	defer process.writeMu.Unlock()
	if !process.stdinClosed {
		process.stdinErr = process.stdin.Close()
		process.stdinClosed = true
	}
	return process.stdinErr
}

// Wait returns only after both the child and its bounded stderr drain stop.
func (process *AppServerProcess) Wait(ctx context.Context) error {
	if process == nil || process.command == nil {
		return errors.New("app-server process is required")
	}
	if ctx == nil {
		return errors.New("app-server wait context is required")
	}
	select {
	case <-process.waitDone:
	case <-ctx.Done():
		return contextFailure(ctx)
	}
	select {
	case <-process.stderrDone:
		return process.waitErr
	case <-ctx.Done():
		return contextFailure(ctx)
	}
}

func (process *AppServerProcess) Stderr() (contents []byte, truncated bool) {
	if process == nil || process.stderr == nil {
		return nil, false
	}
	return process.stderr.snapshot()
}

func (process *AppServerProcess) PID() int {
	if process == nil || process.command == nil || process.command.Process == nil {
		return 0
	}
	return process.command.Process.Pid
}

func validateAppServerProcessConfig(config AppServerProcessConfig) (AppServerProcessConfig, []string, error) {
	for label, path := range map[string]string{
		"app-server final-exec executable": config.FinalExecExecutable,
		"stock Codex executable":           config.CodexExecutable,
	} {
		if err := validateAppServerExecutable(label, path); err != nil {
			return AppServerProcessConfig{}, nil, err
		}
	}
	if err := validateEmptyAppServerDirectory(config.Directory); err != nil {
		return AppServerProcessConfig{}, nil, err
	}
	for label, path := range map[string]string{
		"app-server home":                config.Environment.Home,
		"app-server Codex home":          config.Environment.CodexHome,
		"app-server temporary directory": config.Environment.Temporary,
	} {
		if err := validateAppServerDirectory(label, path); err != nil {
			return AppServerProcessConfig{}, nil, err
		}
	}
	if config.Environment.ModelCapability == "" || len(config.Environment.ModelCapability) > maximumModelCapabilityBytes ||
		strings.ContainsAny(config.Environment.ModelCapability, "\x00\r\n") {
		return AppServerProcessConfig{}, nil, errors.New("app-server model capability is invalid")
	}
	if err := validateAppServerIdentity("worker", config.WorkerUID, config.WorkerGID); err != nil {
		return AppServerProcessConfig{}, nil, err
	}
	if err := validateAppServerIdentity("app", config.AppUID, config.AppGID); err != nil {
		return AppServerProcessConfig{}, nil, err
	}
	if config.WorkerUID == config.AppUID || config.WorkerGID == config.AppGID {
		return AppServerProcessConfig{}, nil, errors.New("worker and app-server must use distinct uid and gid identities")
	}
	if config.MaxFrameBytes == 0 {
		config.MaxFrameBytes = codexwire.DefaultMaxFrameBytes
	}
	if config.IncomingFrames == 0 {
		config.IncomingFrames = defaultAppServerIncomingFrames
	}
	if config.MaxStderrBytes == 0 {
		config.MaxStderrBytes = defaultAppServerStderrBytes
	}
	if err := validateAppServerProcessBounds(appServerProcessBounds{
		maxFrameBytes: config.MaxFrameBytes, incomingFrames: config.IncomingFrames,
		maxStderrBytes: config.MaxStderrBytes,
	}); err != nil {
		return AppServerProcessConfig{}, nil, err
	}
	environment := map[string]string{
		AppServerModelCapabilityEnvironment: config.Environment.ModelCapability,
		"CODEX_HOME":                        config.Environment.CodexHome,
		"HOME":                              config.Environment.Home,
		"LANG":                              "C",
		"LC_ALL":                            "C",
		"NO_COLOR":                          "1",
		"PATH":                              "/usr/bin:/bin",
		"SHELL":                             "/bin/sh",
		"TMPDIR":                            config.Environment.Temporary,
	}
	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	explicit := make([]string, 0, len(names))
	for _, name := range names {
		explicit = append(explicit, name+"="+environment[name])
	}
	return config, explicit, nil
}

func validateAppServerExecutable(label, path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an absolute path", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s is not a directly executable regular file: mode=%s", label, info.Mode())
	}
	return nil
}

func validateAppServerDirectory(label, path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("%s must be an absolute path", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a real directory: mode=%s", label, info.Mode())
	}
	return nil
}

func validateEmptyAppServerDirectory(path string) error {
	if err := validateAppServerDirectory("app-server working directory", path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o222 != 0 {
		return errors.New("app-server working directory must be read-only")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read app-server working directory: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("app-server working directory must be empty")
	}
	return nil
}

func validateAppServerIdentity(label string, uid, gid uint32) error {
	if uid == 0 || gid == 0 || uid == ^uint32(0) || gid == ^uint32(0) {
		return fmt.Errorf("app-server %s identity must be valid and unprivileged: uid=%d gid=%d", label, uid, gid)
	}
	return nil
}

func validateAppServerProcessBounds(bounds appServerProcessBounds) error {
	if bounds.maxFrameBytes < 1 || bounds.maxFrameBytes > codexwire.DefaultMaxFrameBytes {
		return fmt.Errorf("app-server max frame bytes must be between 1 and %d", codexwire.DefaultMaxFrameBytes)
	}
	if bounds.incomingFrames < 1 || bounds.incomingFrames > maximumAppServerIncomingFrames {
		return fmt.Errorf("app-server incoming frame buffer must be between 1 and %d", maximumAppServerIncomingFrames)
	}
	if bounds.maxStderrBytes < 1 || bounds.maxStderrBytes > maximumAppServerStderrBytes {
		return fmt.Errorf("app-server stderr bytes must be between 1 and %d", maximumAppServerStderrBytes)
	}
	return nil
}

type boundedAppServerCapture struct {
	mu        sync.Mutex
	contents  bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedAppServerCapture(limit int) *boundedAppServerCapture {
	return &boundedAppServerCapture{limit: limit}
}

func (capture *boundedAppServerCapture) Write(data []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	original := len(data)
	remaining := capture.limit - capture.contents.Len()
	if remaining < len(data) {
		capture.truncated = true
		data = data[:max(remaining, 0)]
	}
	_, _ = capture.contents.Write(data)
	return original, nil
}

func (capture *boundedAppServerCapture) snapshot() ([]byte, bool) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return bytes.Clone(capture.contents.Bytes()), capture.truncated
}
