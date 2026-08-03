package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

type serveFunc func(context.Context, func(string) string, io.Writer) error

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, servePlatformGateway))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, serve serveFunc) int {
	if len(args) != 1 || args[0] != "serve" {
		fmt.Fprintln(stderr, "usage: platform-gateway serve")
		return 2
	}
	if serve == nil {
		fmt.Fprintln(stderr, "platform-gateway serve: command is unavailable")
		return 1
	}
	if err := serve(ctx, getenv, stdout); err != nil {
		fmt.Fprintf(stderr, "platform-gateway serve: %v\n", err)
		return 1
	}
	return 0
}
