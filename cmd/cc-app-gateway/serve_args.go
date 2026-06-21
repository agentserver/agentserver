package main

import (
	"flag"
	"fmt"
	"os"
)

type serveFlags struct {
	ListenAddr string
	ClaudeBin  string
}

// parseServeArgs parses command-line flags for the serve subcommand.
// It returns the flags and any parsing error.
func parseServeArgs(args []string) (serveFlags, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)

	// Get env defaults
	listenAddrDefault := os.Getenv("CCAPPGW_LISTEN_ADDR")
	if listenAddrDefault == "" {
		listenAddrDefault = ":8087"
	}

	claudeBinDefault := os.Getenv("CCAPPGW_CLAUDE_BIN")
	if claudeBinDefault == "" {
		claudeBinDefault = "/usr/local/bin/claude"
	}

	listenAddr := fs.String("listen-addr", listenAddrDefault, "HTTP listen address")
	claudeBin := fs.String("claude-bin", claudeBinDefault, "Path to claude binary")

	if err := fs.Parse(args); err != nil {
		return serveFlags{}, fmt.Errorf("failed to parse serve args: %w", err)
	}

	return serveFlags{
		ListenAddr: *listenAddr,
		ClaudeBin:  *claudeBin,
	}, nil
}
