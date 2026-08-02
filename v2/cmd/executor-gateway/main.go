package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

type gatewayServeMode uint8

const (
	gatewayServeProduction gatewayServeMode = iota + 1
	gatewayServeInsecureDevelopment
)

type serveFunc func(context.Context, func(string) string, io.Writer, gatewayServeMode) error

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, serveGateway))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, serve serveFunc) int {
	var mode gatewayServeMode
	switch {
	case len(args) == 1 && args[0] == "serve":
		mode = gatewayServeProduction
	case len(args) == 2 && args[0] == "serve" && args[1] == "--insecure-dev":
		mode = gatewayServeInsecureDevelopment
	default:
		fmt.Fprintln(stderr, "usage: executor-gateway serve")
		fmt.Fprintln(stderr, "       executor-gateway serve --insecure-dev")
		return 2
	}
	if serve == nil {
		fmt.Fprintln(stderr, "executor-gateway serve: command is unavailable")
		return 1
	}
	if err := serve(ctx, getenv, stdout, mode); err != nil {
		fmt.Fprintf(stderr, "executor-gateway serve: %v\n", err)
		return 1
	}
	return 0
}
