package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentserver/agentserver/internal/browsergateway"
)

const usage = `browser-gateway — AG-UI endpoint for the codex harness

Subcommands:
  serve   Run the AG-UI HTTP/SSE server
`

const serveHelp = `Usage: browser-gateway serve [flags]

Run the browser-gateway HTTP/SSE server: exposes POST /agui (AG-UI over SSE),
translating each run into a codex turn via codex-app-gateway /codex-app/ws.

Flags:
  --listen-addr <addr>   HTTP listen address (default :8088, env BRG_LISTEN_ADDR)

Required env:
  BRG_CODEX_APP_GATEWAY_WS_URL   base ws URL of codex-app-gateway (e.g. ws://codex-app-gateway:8086)
Optional env:
  BRG_ALLOWED_ORIGINS            CORS allowlist, comma-separated (default *)
  BRG_LOG_LEVEL                  debug|info|warn|error (default info)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stderr, usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}

func runServe(rawArgs []string) {
	args, err := parseServeArgs(rawArgs)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(os.Stderr, serveHelp)
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "browser-gateway serve:", err)
		os.Exit(2)
	}
	cfg, err := browsergateway.LoadServeConfigFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "browser-gateway serve: config:", err)
		os.Exit(2)
	}
	if args.ListenAddr != "" {
		cfg.ListenAddr = args.ListenAddr
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	srv := browsergateway.NewServer(cfg, logger)
	logger.Info("browser-gateway starting", "addr", cfg.ListenAddr, "cxg", cfg.CodexAppGatewayWSURL)
	if err := srv.Run(ctx, cfg.ListenAddr); err != nil {
		logger.Error("server exited with error", "err", err)
		os.Exit(1)
	}
	logger.Info("server clean exit")
}
