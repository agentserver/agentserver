// Package codexprocess starts a stock Codex binary with explicit stdio and
// environment boundaries for conformance probes.
package codexprocess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

const (
	defaultIncomingBuffer = 64
	defaultStderrBytes    = 1024 * 1024
)

type Config struct {
	Binary         string
	Args           []string
	Dir            string
	Env            []string
	MaxFrameBytes  int
	IncomingBuffer int
	StderrBytes    int
}

type Process struct {
	Peer *codexwire.Peer

	command *exec.Cmd
	stdin   io.WriteCloser

	stdinMu  sync.Mutex
	stdinErr error
	stdinEOF bool

	waitDone chan struct{}
	waitErr  error

	stderr     *boundedCapture
	stderrDone chan struct{}
}

func Start(ctx context.Context, config Config) (*Process, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if config.MaxFrameBytes == 0 {
		config.MaxFrameBytes = codexwire.DefaultMaxFrameBytes
	}
	if config.IncomingBuffer == 0 {
		config.IncomingBuffer = defaultIncomingBuffer
	}
	if config.StderrBytes == 0 {
		config.StderrBytes = defaultStderrBytes
	}

	command := exec.CommandContext(ctx, config.Binary, config.Args...)
	command.Dir = config.Dir
	command.Env = append([]string(nil), config.Env...)
	command.WaitDelay = 2 * time.Second

	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex stderr: %w", err)
	}

	peer, err := codexwire.NewPeer(stdout, stdin, config.MaxFrameBytes, config.IncomingBuffer)
	if err != nil {
		return nil, fmt.Errorf("create Codex wire peer: %w", err)
	}
	capture := newBoundedCapture(config.StderrBytes)
	process := &Process{
		Peer:       peer,
		command:    command,
		stdin:      stdin,
		waitDone:   make(chan struct{}),
		stderr:     capture,
		stderrDone: make(chan struct{}),
	}

	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start stock Codex: %w", err)
	}
	go func() {
		_, _ = io.Copy(capture, stderr)
		close(process.stderrDone)
	}()
	go func() {
		process.waitErr = command.Wait()
		close(process.waitDone)
	}()
	return process, nil
}

func (p *Process) CloseStdin() error {
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if !p.stdinEOF {
		p.stdinErr = p.stdin.Close()
		p.stdinEOF = true
	}
	return p.stdinErr
}

func (p *Process) Kill() error {
	if p.command.Process == nil {
		return errors.New("Codex process was not started")
	}
	err := p.command.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (p *Process) Wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.waitDone:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.stderrDone:
	}
	return p.waitErr
}

func (p *Process) Stderr() (contents []byte, truncated bool) {
	return p.stderr.Snapshot()
}

func validateConfig(config Config) error {
	if config.Binary == "" {
		return errors.New("Codex binary path is required")
	}
	if !filepath.IsAbs(config.Binary) {
		return errors.New("Codex binary path must be absolute")
	}
	info, err := os.Stat(config.Binary)
	if err != nil {
		return fmt.Errorf("stat Codex binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("Codex binary must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return errors.New("Codex binary is not executable")
	}
	if config.Dir == "" || !filepath.IsAbs(config.Dir) {
		return errors.New("Codex working directory must be absolute")
	}
	if info, err := os.Stat(config.Dir); err != nil || !info.IsDir() {
		return errors.New("Codex working directory must exist and be a directory")
	}
	if config.Env == nil {
		return errors.New("Codex environment must be explicit; nil would inherit the parent environment")
	}
	if config.MaxFrameBytes < 0 || config.IncomingBuffer < 0 || config.StderrBytes < 0 {
		return errors.New("Codex process bounds cannot be negative")
	}

	seen := make(map[string]struct{}, len(config.Env))
	for _, entry := range config.Env {
		name, value, found := strings.Cut(entry, "=")
		if !found || name == "" || strings.ContainsRune(name, '\x00') || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("invalid environment entry %q", entry)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate environment variable %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// Environment returns a deterministic baseline environment for local Codex
// conformance probes. It never reads the parent environment. Callers must
// create the referenced directories and own the safety of explicit extras.
func Environment(home, codexHome, temporary string, extra map[string]string) ([]string, error) {
	for label, path := range map[string]string{
		"HOME":       home,
		"CODEX_HOME": codexHome,
		"TMPDIR":     temporary,
	} {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("%s path must be absolute", label)
		}
	}

	values := map[string]string{
		"CODEX_HOME": codexHome,
		"HOME":       home,
		"LANG":       "C",
		"LC_ALL":     "C",
		"NO_COLOR":   "1",
		"PATH":       "/usr/bin:/bin",
		"SHELL":      "/bin/sh",
		"TMPDIR":     temporary,
	}
	for name, value := range extra {
		if name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("invalid extra environment variable %q", name)
		}
		if _, reserved := values[name]; reserved {
			return nil, fmt.Errorf("extra environment cannot override reserved variable %q", name)
		}
		values[name] = value
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	environment := make([]string, 0, len(names))
	for _, name := range names {
		environment = append(environment, name+"="+values[name])
	}
	return environment, nil
}

type boundedCapture struct {
	mu        sync.Mutex
	contents  bytes.Buffer
	limit     int
	truncated bool
}

func newBoundedCapture(limit int) *boundedCapture {
	return &boundedCapture{limit: limit}
}

func (b *boundedCapture) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	originalLength := len(data)
	remaining := b.limit - b.contents.Len()
	if remaining < len(data) {
		b.truncated = true
		data = data[:max(remaining, 0)]
	}
	_, _ = b.contents.Write(data)
	return originalLength, nil
}

func (b *boundedCapture) Snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.contents.Bytes()), b.truncated
}
