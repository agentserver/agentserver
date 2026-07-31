package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/agentserver/agentserver/v2/internal/devfixtures"
	"github.com/agentserver/agentserver/v2/internal/devruntime"
	"github.com/agentserver/agentserver/v2/internal/devstack"
)

type prepareFunc func(string, string) (devstack.Result, error)
type fixturesFunc func(context.Context, string, io.Writer) error
type runtimeFunc func(devruntime.PrepareConfig) (devruntime.Result, error)

type commandFunctions struct {
	prepare  prepareFunc
	fixtures fixturesFunc
	runtime  runtimeFunc
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, commandFunctions{
		prepare: devstack.PrepareFromFile, fixtures: devfixtures.ServeBundle, runtime: devruntime.Prepare,
	}))
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer, commands commandFunctions) int {
	if len(arguments) == 6 && arguments[0] == "runtime" && arguments[1] == "--insecure-dev" &&
		strings.HasPrefix(arguments[2], "--platform=") && strings.TrimPrefix(arguments[2], "--platform=") != "" &&
		strings.HasPrefix(arguments[3], "--codex=") && strings.TrimPrefix(arguments[3], "--codex=") != "" &&
		strings.HasPrefix(arguments[4], "--bwrap=") && strings.TrimPrefix(arguments[4], "--bwrap=") != "" &&
		strings.HasPrefix(arguments[5], "--output-dir=") && strings.TrimPrefix(arguments[5], "--output-dir=") != "" {
		if commands.runtime == nil {
			fmt.Fprintln(stderr, "agentserver-dev runtime: command is unavailable")
			return 1
		}
		result, err := commands.runtime(devruntime.PrepareConfig{
			Platform:        strings.TrimPrefix(arguments[2], "--platform="),
			CodexExecutable: strings.TrimPrefix(arguments[3], "--codex="),
			BwrapExecutable: strings.TrimPrefix(arguments[4], "--bwrap="),
			OutputDirectory: strings.TrimPrefix(arguments[5], "--output-dir="),
		})
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-dev runtime: %v\n", err)
			return 1
		}
		fmt.Fprintf(
			stdout,
			"agentserver-dev runtime: INSECURE DEV %s stock runtime created at %s; manifest %s; bundle %s\n",
			result.Platform, result.OutputDirectory, result.ManifestFile, result.BundleRoot,
		)
		return 0
	}
	if len(arguments) == 3 && arguments[0] == "fixtures" && arguments[1] == "--insecure-dev" &&
		strings.HasPrefix(arguments[2], "--bundle=") && strings.TrimPrefix(arguments[2], "--bundle=") != "" {
		if commands.fixtures == nil {
			fmt.Fprintln(stderr, "agentserver-dev fixtures: command is unavailable")
			return 1
		}
		if err := commands.fixtures(ctx, strings.TrimPrefix(arguments[2], "--bundle="), stdout); err != nil {
			fmt.Fprintf(stderr, "agentserver-dev fixtures: %v\n", err)
			return 1
		}
		return 0
	}
	if len(arguments) != 4 || arguments[0] != "prepare" || arguments[1] != "--insecure-dev" ||
		!strings.HasPrefix(arguments[2], "--config=") || strings.TrimPrefix(arguments[2], "--config=") == "" ||
		!strings.HasPrefix(arguments[3], "--output-dir=") || strings.TrimPrefix(arguments[3], "--output-dir=") == "" {
		writeUsage(stderr)
		return 2
	}
	if commands.prepare == nil {
		fmt.Fprintln(stderr, "agentserver-dev prepare: command is unavailable")
		return 1
	}
	configPath := strings.TrimPrefix(arguments[2], "--config=")
	outputDirectory := strings.TrimPrefix(arguments[3], "--output-dir=")
	result, err := commands.prepare(configPath, outputDirectory)
	if err != nil {
		fmt.Fprintf(stderr, "agentserver-dev prepare: %v\n", err)
		return 1
	}
	fmt.Fprintf(
		stdout,
		"agentserver-dev prepare: INSECURE DEV material created at %s; metadata %s; bootstrap %s; fixtures %s\n",
		result.OutputDirectory, result.MetadataFile, result.BootstrapConfigFile, result.FixturesConfigFile,
	)
	return 0
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: agentserver-dev prepare --insecure-dev --config=/absolute/path --output-dir=/absolute/new-directory")
	fmt.Fprintln(writer, "       agentserver-dev fixtures --insecure-dev --bundle=/absolute/prepared-directory")
	fmt.Fprintln(writer, "       agentserver-dev runtime --insecure-dev --platform=linux-arm64 --codex=/absolute/path --bwrap=/absolute/path --output-dir=/absolute/new-directory")
}
