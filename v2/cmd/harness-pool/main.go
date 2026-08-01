package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

type harnessPoolServeMode uint8

const (
	harnessPoolServeProduction harnessPoolServeMode = iota + 1
	harnessPoolServeInsecureDevelopment
)

type serveFunc func(context.Context, func(string) string, io.Writer, io.Writer, harnessPoolServeMode) error

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, serveHarnessPool))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, serve serveFunc) int {
	var mode harnessPoolServeMode
	switch {
	case len(args) == 1 && args[0] == "serve":
		mode = harnessPoolServeProduction
	case len(args) == 2 && args[0] == "serve" && args[1] == "--insecure-dev":
		mode = harnessPoolServeInsecureDevelopment
	default:
		fmt.Fprintln(stderr, "usage: harness-pool serve")
		fmt.Fprintln(stderr, "       harness-pool serve --insecure-dev")
		return 2
	}
	if serve == nil {
		fmt.Fprintln(stderr, "harness-pool serve: command is unavailable")
		return 1
	}
	if err := serve(ctx, getenv, stdout, stderr, mode); err != nil {
		fmt.Fprintf(stderr, "harness-pool serve: %v\n", err)
		return 1
	}
	return 0
}
