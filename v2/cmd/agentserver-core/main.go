package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const databaseURLEnvironment = "AGENTSERVER_V2_DATABASE_URL"

type migrateFunc func(context.Context, string) (coredb.MigrationResult, error)
type serveFunc func(context.Context, func(string) string, io.Writer) error

type commandFunctions struct {
	migrate migrateFunc
	serve   serveFunc
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, commandFunctions{
		migrate: coredb.Migrate,
		serve:   serveCore,
	}))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, commands commandFunctions) int {
	if len(args) != 1 || (args[0] != "migrate" && args[0] != "serve") {
		fmt.Fprintln(stderr, "usage: agentserver-core <migrate|serve>")
		return 2
	}
	if args[0] == "serve" {
		if commands.serve == nil {
			fmt.Fprintln(stderr, "agentserver-core serve: command is unavailable")
			return 1
		}
		if err := commands.serve(ctx, getenv, stdout); err != nil {
			fmt.Fprintf(stderr, "agentserver-core serve: %v\n", err)
			return 1
		}
		return 0
	}
	databaseURL := getenv(databaseURLEnvironment)
	if strings.TrimSpace(databaseURL) == "" {
		fmt.Fprintf(stderr, "agentserver-core migrate: %s is required\n", databaseURLEnvironment)
		return 2
	}
	if commands.migrate == nil {
		fmt.Fprintln(stderr, "agentserver-core migrate: command is unavailable")
		return 1
	}

	result, err := commands.migrate(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "agentserver-core migrate: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "agentserver-core migrate: schema %s is at version %04d; applied %d migration(s)\n", coredb.SchemaName, result.CurrentVersion, result.Applied)
	return 0
}
