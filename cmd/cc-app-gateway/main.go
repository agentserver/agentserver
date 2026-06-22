package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agentserver/agentserver/internal/ccappgateway"
)

var (
	BuildVersion = "dev"
	BuildSHA     = "dev"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	subcommand := os.Args[1]

	switch subcommand {
	case "serve":
		serveCmd(os.Args[2:])
	case "env-mcp":
		envMcpCmd()
	case "version":
		versionCmd()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", subcommand)
		printUsage()
		os.Exit(2)
	}
}

func serveCmd(args []string) {
	flags, err := parseServeArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing serve args: %v\n", err)
		os.Exit(2)
	}

	cfg, err := ccappgateway.LoadServeConfigFromEnv(ccappgateway.ServeFlags{
		ListenAddr: flags.ListenAddr,
		ClaudeBin:  flags.ClaudeBin,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(2)
	}

	srv, err := ccappgateway.NewServer(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server init: %v\n", err)
		os.Exit(1)
	}

	// Listen for SIGTERM/SIGINT for graceful shutdown.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Wait for either signal or Start to fail.
	var startErr error
	select {
	case <-ctx.Done():
		// Shutdown signal received; wait for Start to return.
		startErr = <-errCh
	case startErr = <-errCh:
		// Start exited on its own (listener bind failed, etc.)
	}

	// Check for errors from Start (but ignore ErrServerClosed, which is expected).
	if startErr != nil && startErr != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server: %v\n", startErr)
		os.Exit(1)
	}

	// Only drain if shutdown was initiated by signal (ctx is done).
	if ctx.Err() != nil {
		shutdownCtx, scancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer scancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "shutdown: %v\n", err)
			os.Exit(1)
		}
	}
}

func envMcpCmd() {
	fmt.Fprintf(os.Stderr, "env-mcp not implemented in phase 1\n")
	os.Exit(2)
}

func versionCmd() {
	fmt.Printf("cc-app-gateway version %s (%s)\n", BuildVersion, BuildSHA)
	os.Exit(0)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "usage: cc-app-gateway <subcommand> [flags]\n")
	fmt.Fprintf(os.Stderr, "subcommands:\n")
	fmt.Fprintf(os.Stderr, "  serve     Start the cc-app-gateway server\n")
	fmt.Fprintf(os.Stderr, "  env-mcp   (reserved for phase 3)\n")
	fmt.Fprintf(os.Stderr, "  version   Print version\n")
}
