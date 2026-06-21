package main

import (
	"fmt"
	"os"

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

	// Task 1 scaffold: just print config and exit
	fmt.Printf("phase1 scaffold OK; listen=%s claudeBin=%s\n", cfg.ListenAddr, cfg.ClaudeBin)
	os.Exit(0)
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
