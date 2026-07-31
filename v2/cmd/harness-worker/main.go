package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const (
	workerBootstrapDescriptor  = 3
	workerPromptDescriptor     = 4
	workerCheckpointDescriptor = 5
)

type executeWorkerFunc func(context.Context, string, *os.File, *os.File, *os.File) error
type inheritedWorkerFileFunc func(uintptr, string) *os.File

type workerCommandFunctions struct {
	execute       executeWorkerFunc
	inheritedFile inheritedWorkerFileFunc
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stderr, workerCommandFunctions{
		execute: executeWorker,
		inheritedFile: func(descriptor uintptr, name string) *os.File {
			return os.NewFile(descriptor, name)
		},
	}))
}

func run(ctx context.Context, arguments []string, stderr io.Writer, commands workerCommandFunctions) int {
	configPath, checkpoint, ok := parseWorkerArguments(arguments)
	if !ok {
		fmt.Fprintln(stderr, "usage: harness-worker run --config=/absolute/path --bootstrap-fd=3 --prompt-fd=4 [--checkpoint-fd=5]")
		return 2
	}
	if commands.execute == nil || commands.inheritedFile == nil {
		fmt.Fprintln(stderr, "harness-worker: command is unavailable")
		return 1
	}
	bootstrapFile := commands.inheritedFile(workerBootstrapDescriptor, "harness-bootstrap")
	promptFile := commands.inheritedFile(workerPromptDescriptor, "harness-prompt")
	if bootstrapFile == nil || promptFile == nil {
		if bootstrapFile != nil {
			_ = bootstrapFile.Close()
		}
		if promptFile != nil {
			_ = promptFile.Close()
		}
		fmt.Fprintln(stderr, "harness-worker: required inherited input descriptor is unavailable")
		return 1
	}
	var checkpointFile *os.File
	if checkpoint {
		checkpointFile = commands.inheritedFile(workerCheckpointDescriptor, "harness-checkpoint")
		if checkpointFile == nil {
			_ = bootstrapFile.Close()
			_ = promptFile.Close()
			fmt.Fprintln(stderr, "harness-worker: inherited checkpoint descriptor is unavailable")
			return 1
		}
	}
	if err := commands.execute(ctx, configPath, bootstrapFile, promptFile, checkpointFile); err != nil {
		fmt.Fprintf(stderr, "harness-worker: %v\n", err)
		return 1
	}
	return 0
}
