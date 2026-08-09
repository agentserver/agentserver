package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

type egressAuthorizerServeMode uint8

const (
	egressAuthorizerServeProduction egressAuthorizerServeMode = iota + 1
	egressAuthorizerServePolicyBootstrap
	egressAuthorizerServeInsecureDevelopment
)

type egressAuthorizerServeFunc func(context.Context, func(string) string, io.Writer, io.Writer, egressAuthorizerServeMode) error

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, serveEgressAuthorizer))
}

func run(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	serve egressAuthorizerServeFunc,
) int {
	var mode egressAuthorizerServeMode
	switch {
	case len(args) == 1 && args[0] == "serve":
		mode = egressAuthorizerServeProduction
	case len(args) == 2 && args[0] == "serve" && args[1] == "--policy-bootstrap":
		mode = egressAuthorizerServePolicyBootstrap
	case len(args) == 2 && args[0] == "serve" && args[1] == "--insecure-dev":
		mode = egressAuthorizerServeInsecureDevelopment
	default:
		writeUsage(stderr)
		return 2
	}
	if serve == nil {
		fmt.Fprintln(stderr, "egress-authorizer serve: command is unavailable")
		return 1
	}
	if err := serve(ctx, getenv, stdout, stderr, mode); err != nil {
		fmt.Fprintf(stderr, "egress-authorizer serve: %v\n", err)
		return 1
	}
	return 0
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: egress-authorizer serve")
	fmt.Fprintln(writer, "       egress-authorizer serve --policy-bootstrap")
	fmt.Fprintln(writer, "       egress-authorizer serve --insecure-dev")
}
