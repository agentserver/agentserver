package codexprocess

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

type CommandConfig struct {
	Binary      string
	Args        []string
	Dir         string
	Env         []string
	StdoutBytes int
	StderrBytes int
}

type CommandResult struct {
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

// RunCommand runs a non-stdio-server Codex subcommand with explicit environment
// and bounded output capture. It is used for version and schema fingerprinting.
func RunCommand(ctx context.Context, config CommandConfig) (CommandResult, error) {
	if ctx == nil {
		return CommandResult{}, errors.New("context is required")
	}
	if err := validateCommandFields(config.Binary, config.Dir, config.Env); err != nil {
		return CommandResult{}, err
	}
	if config.StdoutBytes < 0 || config.StderrBytes < 0 {
		return CommandResult{}, errors.New("command output bounds cannot be negative")
	}
	if config.StdoutBytes == 0 {
		config.StdoutBytes = defaultStderrBytes
	}
	if config.StderrBytes == 0 {
		config.StderrBytes = defaultStderrBytes
	}

	stdout := newBoundedCapture(config.StdoutBytes)
	stderr := newBoundedCapture(config.StderrBytes)
	command := exec.CommandContext(ctx, config.Binary, config.Args...)
	command.Dir = config.Dir
	command.Env = append([]string(nil), config.Env...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = 2 * time.Second
	err := command.Run()
	stdoutBytes, stdoutTruncated := stdout.Snapshot()
	stderrBytes, stderrTruncated := stderr.Snapshot()
	result := CommandResult{
		Stdout:          stdoutBytes,
		Stderr:          stderrBytes,
		StdoutTruncated: stdoutTruncated,
		StderrTruncated: stderrTruncated,
	}
	if err != nil {
		return result, fmt.Errorf("run stock Codex command: %w", err)
	}
	return result, nil
}
