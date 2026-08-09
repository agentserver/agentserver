package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

type sandboxGatewayServeMode uint8

const (
	sandboxGatewayServeProduction sandboxGatewayServeMode = iota + 1
	sandboxGatewayServeInsecureDevelopment
)

type sandboxGatewayServeFunc func(context.Context, func(string) string, io.Writer, io.Writer, sandboxGatewayServeMode) error

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, serveSandboxGateway))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, serve sandboxGatewayServeFunc) int {
	var mode sandboxGatewayServeMode
	switch {
	case len(args) == 1 && args[0] == "serve":
		mode = sandboxGatewayServeProduction
	case len(args) == 2 && args[0] == "serve" && args[1] == "--insecure-dev":
		mode = sandboxGatewayServeInsecureDevelopment
	default:
		writeUsage(stderr)
		return 2
	}
	if serve == nil {
		fmt.Fprintln(stderr, "sandbox-gateway serve: command is unavailable")
		return 1
	}
	if err := serve(ctx, getenv, stdout, stderr, mode); err != nil {
		fmt.Fprintf(stderr, "sandbox-gateway serve: %v\n", err)
		return 1
	}
	return 0
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: sandbox-gateway serve")
	fmt.Fprintln(writer, "       sandbox-gateway serve --insecure-dev")
}
