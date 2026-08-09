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
type coreServeMode uint8

const (
	coreServeProduction coreServeMode = iota + 1
	coreServeInsecureDevelopment
)

type serveFunc func(context.Context, func(string) string, io.Writer, io.Writer, coreServeMode) error
type bootstrapFunc func(context.Context, string, string) (developmentBootstrapResult, error)
type productionBootstrapFunc func(context.Context, string, string) (productionBootstrapCommandResult, error)
type managedEnvironmentBootstrapFunc func(context.Context, string, string) (managedEnvironmentProfileCommandResult, error)
type commandFunctions struct {
	migrate                     migrateFunc
	serve                       serveFunc
	bootstrap                   bootstrapFunc
	bootstrapProduction         productionBootstrapFunc
	bootstrapManagedEnvironment managedEnvironmentBootstrapFunc
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, commandFunctions{
		migrate: coredb.Migrate, serve: serveCore, bootstrap: bootstrapDevelopment,
		bootstrapProduction: bootstrapProduction, bootstrapManagedEnvironment: bootstrapManagedEnvironmentProfile,
	}))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, commands commandFunctions) int {
	if len(args) == 0 {
		writeCoreUsage(stderr)
		return 2
	}
	if args[0] == "serve" {
		mode := coreServeProduction
		if len(args) == 2 && args[1] == "--insecure-dev" {
			mode = coreServeInsecureDevelopment
		} else if len(args) != 1 {
			writeCoreUsage(stderr)
			return 2
		}
		if commands.serve == nil {
			fmt.Fprintln(stderr, "agentserver-core serve: command is unavailable")
			return 1
		}
		if err := commands.serve(ctx, getenv, stdout, stderr, mode); err != nil {
			fmt.Fprintf(stderr, "agentserver-core serve: %v\n", err)
			return 1
		}
		return 0
	}
	if args[0] == "bootstrap" {
		production := len(args) == 2 && strings.HasPrefix(args[1], "--config=") && strings.TrimPrefix(args[1], "--config=") != ""
		development := len(args) == 3 && args[1] == "--insecure-dev" && strings.HasPrefix(args[2], "--config=") && strings.TrimPrefix(args[2], "--config=") != ""
		if !production && !development {
			writeCoreUsage(stderr)
			return 2
		}
		databaseURL := getenv(databaseURLEnvironment)
		if strings.TrimSpace(databaseURL) == "" {
			fmt.Fprintf(stderr, "agentserver-core bootstrap: %s is required\n", databaseURLEnvironment)
			return 2
		}
		if production {
			if commands.bootstrapProduction == nil {
				fmt.Fprintln(stderr, "agentserver-core bootstrap: command is unavailable")
				return 1
			}
			result, err := commands.bootstrapProduction(ctx, databaseURL, strings.TrimPrefix(args[1], "--config="))
			if err != nil {
				fmt.Fprintf(stderr, "agentserver-core bootstrap: %v\n", err)
				return 1
			}
			fmt.Fprintf(
				stdout,
				"agentserver-core bootstrap: production workspace %s session %s user %s executor %s; schema %04d; created %d row(s)\n",
				result.WorkspaceID, result.SessionID, result.UserID, result.ExecutorID,
				result.Bootstrap.SchemaVersion, result.Bootstrap.CreatedRows,
			)
		} else {
			if commands.bootstrap == nil {
				fmt.Fprintln(stderr, "agentserver-core bootstrap: command is unavailable")
				return 1
			}
			result, err := commands.bootstrap(ctx, databaseURL, strings.TrimPrefix(args[2], "--config="))
			if err != nil {
				fmt.Fprintf(stderr, "agentserver-core bootstrap: %v\n", err)
				return 1
			}
			fmt.Fprintf(
				stdout,
				"agentserver-core bootstrap: INSECURE DEV workspace %s session %s actor %s executor %s environment %s; schema %04d; created %d row(s)\n",
				result.WorkspaceID, result.SessionID, result.ActorID, result.ExecutorID, result.EnvironmentID,
				result.Migration.CurrentVersion, result.Bootstrap.CreatedRows,
			)
		}
		return 0
	}
	if args[0] == "bootstrap-managed-environment" {
		valid := len(args) == 2 && strings.HasPrefix(args[1], "--config=") && strings.TrimPrefix(args[1], "--config=") != ""
		if !valid {
			writeCoreUsage(stderr)
			return 2
		}
		databaseURL := getenv(databaseURLEnvironment)
		if strings.TrimSpace(databaseURL) == "" {
			fmt.Fprintf(stderr, "agentserver-core bootstrap-managed-environment: %s is required\n", databaseURLEnvironment)
			return 2
		}
		if commands.bootstrapManagedEnvironment == nil {
			fmt.Fprintln(stderr, "agentserver-core bootstrap-managed-environment: command is unavailable")
			return 1
		}
		result, err := commands.bootstrapManagedEnvironment(ctx, databaseURL, strings.TrimPrefix(args[1], "--config="))
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-core bootstrap-managed-environment: %v\n", err)
			return 1
		}
		action := "already present"
		if result.Bootstrap.Created {
			action = "created"
		}
		fmt.Fprintf(
			stdout,
			"agentserver-core bootstrap-managed-environment: workspace %s executor %s environment %s; schema %04d; %s\n",
			result.WorkspaceID, result.ExecutorID, result.EnvironmentID, result.Bootstrap.SchemaVersion, action,
		)
		return 0
	}
	if args[0] != "migrate" || len(args) != 1 {
		writeCoreUsage(stderr)
		return 2
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

func writeCoreUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: agentserver-core migrate")
	fmt.Fprintln(writer, "       agentserver-core serve")
	fmt.Fprintln(writer, "       agentserver-core serve --insecure-dev")
	fmt.Fprintln(writer, "       agentserver-core bootstrap --config=/absolute/path")
	fmt.Fprintln(writer, "       agentserver-core bootstrap --insecure-dev --config=/absolute/path")
	fmt.Fprintln(writer, "       agentserver-core bootstrap-managed-environment --config=/absolute/path")
}
