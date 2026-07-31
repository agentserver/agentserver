package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/devstack"
)

type prepareFunc func(string, string) (devstack.Result, error)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, devstack.PrepareFromFile))
}

func run(arguments []string, stdout, stderr io.Writer, prepare prepareFunc) int {
	if len(arguments) != 4 || arguments[0] != "prepare" || arguments[1] != "--insecure-dev" ||
		!strings.HasPrefix(arguments[2], "--config=") || strings.TrimPrefix(arguments[2], "--config=") == "" ||
		!strings.HasPrefix(arguments[3], "--output-dir=") || strings.TrimPrefix(arguments[3], "--output-dir=") == "" {
		writeUsage(stderr)
		return 2
	}
	if prepare == nil {
		fmt.Fprintln(stderr, "agentserver-dev prepare: command is unavailable")
		return 1
	}
	configPath := strings.TrimPrefix(arguments[2], "--config=")
	outputDirectory := strings.TrimPrefix(arguments[3], "--output-dir=")
	result, err := prepare(configPath, outputDirectory)
	if err != nil {
		fmt.Fprintf(stderr, "agentserver-dev prepare: %v\n", err)
		return 1
	}
	fmt.Fprintf(
		stdout,
		"agentserver-dev prepare: INSECURE DEV material created at %s; metadata %s; bootstrap %s\n",
		result.OutputDirectory, result.MetadataFile, result.BootstrapConfigFile,
	)
	return 0
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: agentserver-dev prepare --insecure-dev --config=/absolute/path --output-dir=/absolute/new-directory")
}
