package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/harnessinit"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		writeUsage(stderr)
		return 2
	}
	switch arguments[0] {
	case "materialize":
		values, ok := exactArguments(arguments[1:], "profile", "source", "destination", "uid", "gid")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		uid, uidErr := parseIdentity(values["uid"])
		gid, gidErr := parseIdentity(values["gid"])
		if uidErr != nil || gidErr != nil {
			fmt.Fprintln(stderr, "agentserver-init materialize: uid and gid must be unprivileged base-10 identities")
			return 2
		}
		if err := harnessinit.MaterializeFiles(values["profile"], values["source"], values["destination"], uid, gid); err != nil {
			fmt.Fprintf(stderr, "agentserver-init materialize: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "agentserver-init materialize: ready")
		return 0
	case "install-network-guard":
		values, ok := exactArguments(arguments[1:], "config")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		config, err := harnessinit.LoadNetworkGuardConfig(values["config"])
		if err == nil {
			err = harnessinit.InstallNetworkGuard(config)
		}
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-init install-network-guard: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "agentserver-init install-network-guard: installed")
		return 0
	case "prepare-harness-directories":
		values, ok := exactArguments(arguments[1:], "runtime", "checkpoint", "scratch", "uid", "gid")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		uid, uidErr := parseIdentity(values["uid"])
		gid, gidErr := parseIdentity(values["gid"])
		if uidErr != nil || gidErr != nil {
			fmt.Fprintln(stderr, "agentserver-init prepare-harness-directories: uid and gid must be unprivileged base-10 identities")
			return 2
		}
		if err := harnessinit.PrepareHarnessDirectories(
			values["runtime"], values["checkpoint"], values["scratch"], uid, gid,
		); err != nil {
			fmt.Fprintf(stderr, "agentserver-init prepare-harness-directories: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "agentserver-init prepare-harness-directories: ready")
		return 0
	default:
		writeUsage(stderr)
		return 2
	}
}

func exactArguments(arguments []string, names ...string) (map[string]string, bool) {
	if len(arguments) != len(names) {
		return nil, false
	}
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed[name] = struct{}{}
	}
	values := make(map[string]string, len(names))
	for _, argument := range arguments {
		if !strings.HasPrefix(argument, "--") {
			return nil, false
		}
		name, value, found := strings.Cut(argument, "=")
		name = strings.TrimPrefix(name, "--")
		if !found || value == "" {
			return nil, false
		}
		if _, exists := allowed[name]; !exists {
			return nil, false
		}
		if _, duplicate := values[name]; duplicate {
			return nil, false
		}
		values[name] = value
	}
	return values, len(values) == len(names)
}

func parseIdentity(value string) (uint32, error) {
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || parsed == 0 || parsed > 1<<31-1 || strconv.FormatUint(parsed, 10) != value {
		return 0, fmt.Errorf("invalid identity")
	}
	return uint32(parsed), nil
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: agentserver-init materialize --profile=NAME --source=/absolute/path --destination=/absolute/path --uid=N --gid=N")
	fmt.Fprintln(writer, "       agentserver-init install-network-guard --config=/absolute/path")
	fmt.Fprintln(writer, "       agentserver-init prepare-harness-directories --runtime=/absolute/path --checkpoint=/absolute/path --scratch=/absolute/path --uid=N --gid=N")
}
