package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/productiondeploy"
)

type deployCommands struct {
	load                    func(string) (productiondeploy.LoadedConfig, error)
	render                  func(productiondeploy.LoadedConfig) (productiondeploy.Bundle, error)
	write                   func(productiondeploy.Bundle, string) error
	chart                   func(productiondeploy.LoadedConfig) (productiondeploy.HelmChart, error)
	writeChart              func(productiondeploy.HelmChart, string) error
	lock                    func(productiondeploy.LoadedConfig, productiondeploy.ReleaseLock) ([]byte, error)
	lockDeveloperService    func(productiondeploy.LoadedConfig, string) ([]byte, error)
	writeLock               func([]byte, string) error
	preparePolicyBootstrap  func(string, string) error
	pinManagedTerminal      func(string, string, string, string) error
	retargetManagedTerminal func(string, string, string, string, string, string, string) error
	retargetDirectTerminal  func(string, string, string, string, string, string, string) error
	activateManagedExecutor func(string, string, string, string, string, string) error
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, deployCommands{
		load: productiondeploy.LoadConfig, render: productiondeploy.Render, write: productiondeploy.WriteBundle,
		chart: productiondeploy.RenderHelmChart, writeChart: productiondeploy.WriteHelmChart,
		lock: productiondeploy.LockRelease, lockDeveloperService: productiondeploy.LockDeveloperServiceRelease,
		writeLock:               productiondeploy.WriteReleaseConfig,
		preparePolicyBootstrap:  productiondeploy.PreparePolicyBootstrapFile,
		pinManagedTerminal:      productiondeploy.PinManagedTerminalRevisionFile,
		retargetManagedTerminal: productiondeploy.RetargetManagedTerminalFile,
		retargetDirectTerminal:  productiondeploy.RetargetDirectManagedTerminalFile,
		activateManagedExecutor: productiondeploy.ActivateManagedExecutorFile,
	}))
}

func run(arguments []string, stdout, stderr io.Writer, commands deployCommands) int {
	if len(arguments) == 0 {
		writeUsage(stderr)
		return 2
	}
	switch arguments[0] {
	case "activate-managed-executor":
		values, ok := exactArguments(arguments[1:], "config", "output", "network-report", "policy-revision", "policy-evidence-ref", "network-evidence-ref")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		if commands.activateManagedExecutor == nil {
			fmt.Fprintln(stderr, "agentserver-deploy activate-managed-executor: command is unavailable")
			return 1
		}
		if err := commands.activateManagedExecutor(
			values["config"], values["output"], values["network-report"], values["policy-revision"],
			values["policy-evidence-ref"], values["network-evidence-ref"],
		); err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy activate-managed-executor: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentserver-deploy activate-managed-executor: wrote evidence-bound active config to %s\n", values["output"])
		return 0
	case "prepare-policy-bootstrap":
		values, ok := exactArguments(arguments[1:], "config", "output")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		if commands.preparePolicyBootstrap == nil {
			fmt.Fprintln(stderr, "agentserver-deploy prepare-policy-bootstrap: command is unavailable")
			return 1
		}
		if err := commands.preparePolicyBootstrap(values["config"], values["output"]); err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy prepare-policy-bootstrap: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentserver-deploy prepare-policy-bootstrap: wrote fail-closed bootstrap config to %s\n", values["output"])
		return 0
	case "pin-terminal-revision":
		values, ok := exactArguments(arguments[1:], "config", "output", "sandbox-id", "revision-id")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		if commands.pinManagedTerminal == nil {
			fmt.Fprintln(stderr, "agentserver-deploy pin-terminal-revision: command is unavailable")
			return 1
		}
		if err := commands.pinManagedTerminal(
			values["config"], values["output"], values["sandbox-id"], values["revision-id"],
		); err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy pin-terminal-revision: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentserver-deploy pin-terminal-revision: wrote fail-closed Terminal revision config to %s\n", values["output"])
		return 0
	case "retarget-terminal-sandbox":
		values, ok := exactArguments(
			arguments[1:], "config", "output", "expected-sandbox-id", "sandbox-id", "revision-id", "environment-id", "managed-sandbox-image",
		)
		if !ok {
			writeUsage(stderr)
			return 2
		}
		if commands.retargetManagedTerminal == nil {
			fmt.Fprintln(stderr, "agentserver-deploy retarget-terminal-sandbox: command is unavailable")
			return 1
		}
		if err := commands.retargetManagedTerminal(
			values["config"], values["output"], values["expected-sandbox-id"], values["sandbox-id"],
			values["revision-id"], values["environment-id"], values["managed-sandbox-image"],
		); err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy retarget-terminal-sandbox: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentserver-deploy retarget-terminal-sandbox: wrote fail-closed Terminal Sandbox config to %s\n", values["output"])
		return 0
	case "retarget-direct-terminal-sandbox":
		values, ok := exactArguments(
			arguments[1:], "config", "output", "expected-sandbox-id", "sandbox-id", "revision-id", "environment-id", "managed-sandbox-image",
		)
		if !ok {
			writeUsage(stderr)
			return 2
		}
		if commands.retargetDirectTerminal == nil {
			fmt.Fprintln(stderr, "agentserver-deploy retarget-direct-terminal-sandbox: command is unavailable")
			return 1
		}
		if err := commands.retargetDirectTerminal(
			values["config"], values["output"], values["expected-sandbox-id"], values["sandbox-id"],
			values["revision-id"], values["environment-id"], values["managed-sandbox-image"],
		); err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy retarget-direct-terminal-sandbox: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentserver-deploy retarget-direct-terminal-sandbox: wrote fail-closed direct Terminal Sandbox config to %s\n", values["output"])
		return 0
	case "lock-release":
		values, ok := exactArguments(arguments[1:], "config", "output", "service-image", "harness-image", "hydra-image", "managed-sandbox-image", "lark-cli-sha256", "lark-skill-sha256")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		if commands.load == nil || commands.lock == nil || commands.writeLock == nil {
			fmt.Fprintln(stderr, "agentserver-deploy lock-release: command is unavailable")
			return 1
		}
		config, err := commands.load(values["config"])
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy lock-release: %v\n", err)
			return 1
		}
		raw, err := commands.lock(config, productiondeploy.ReleaseLock{
			ServiceImage: values["service-image"], HarnessImage: values["harness-image"],
			HydraImage: values["hydra-image"], ManagedSandboxImage: values["managed-sandbox-image"],
			LarkCLISHA256: values["lark-cli-sha256"], LarkSkillSHA256: values["lark-skill-sha256"],
		})
		if err == nil {
			err = commands.writeLock(raw, values["output"])
		}
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy lock-release: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentserver-deploy lock-release: wrote locked production config to %s\n", values["output"])
		return 0
	case "lock-developer-service":
		values, ok := exactArguments(arguments[1:], "config", "output", "service-image")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		if commands.load == nil || commands.lockDeveloperService == nil || commands.writeLock == nil {
			fmt.Fprintln(stderr, "agentserver-deploy lock-developer-service: command is unavailable")
			return 1
		}
		config, err := commands.load(values["config"])
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy lock-developer-service: %v\n", err)
			return 1
		}
		raw, err := commands.lockDeveloperService(config, values["service-image"])
		if err == nil {
			err = commands.writeLock(raw, values["output"])
		}
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy lock-developer-service: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentserver-deploy lock-developer-service: wrote service-only development config to %s\n", values["output"])
		return 0
	case "validate":
		values, ok := exactArguments(arguments[1:], "config")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		if commands.load == nil {
			fmt.Fprintln(stderr, "agentserver-deploy validate: command is unavailable")
			return 1
		}
		if _, err := commands.load(values["config"]); err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy validate: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "agentserver-deploy validate: production config is valid")
		return 0
	case "render":
		values, ok := exactArguments(arguments[1:], "config", "output")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		if commands.load == nil || commands.render == nil || commands.write == nil {
			fmt.Fprintln(stderr, "agentserver-deploy render: command is unavailable")
			return 1
		}
		config, err := commands.load(values["config"])
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy render: %v\n", err)
			return 1
		}
		bundle, err := commands.render(config)
		if err == nil {
			err = commands.write(bundle, values["output"])
		}
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy render: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentserver-deploy render: wrote %d immutable files to %s\n", len(bundle.Files), values["output"])
		for _, file := range bundle.Files {
			fmt.Fprintf(stdout, "%s  %s\n", file.SHA256, file.Name)
		}
		return 0
	case "chart":
		values, ok := exactArguments(arguments[1:], "config", "output")
		if !ok {
			writeUsage(stderr)
			return 2
		}
		if commands.load == nil || commands.chart == nil || commands.writeChart == nil {
			fmt.Fprintln(stderr, "agentserver-deploy chart: command is unavailable")
			return 1
		}
		config, err := commands.load(values["config"])
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy chart: %v\n", err)
			return 1
		}
		chart, err := commands.chart(config)
		if err == nil {
			err = commands.writeChart(chart, values["output"])
		}
		if err != nil {
			fmt.Fprintf(stderr, "agentserver-deploy chart: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "agentserver-deploy chart: wrote %d immutable files to %s\n", len(chart.Files), values["output"])
		for _, file := range chart.Files {
			fmt.Fprintf(stdout, "%s  %s\n", file.SHA256, file.Name)
		}
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
		name, value, found := strings.Cut(strings.TrimPrefix(argument, "--"), "=")
		if !found || value == "" {
			return nil, false
		}
		if _, found := allowed[name]; !found {
			return nil, false
		}
		if _, duplicate := values[name]; duplicate {
			return nil, false
		}
		values[name] = value
	}
	return values, len(values) == len(names)
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: agentserver-deploy activate-managed-executor --config=/absolute/bootstrap.json --output=/absolute/new-active.json --network-report=/absolute/canonical-report.json --policy-revision=<published-revision> --policy-evidence-ref=<immutable-ticket> --network-evidence-ref=<immutable-report-reference>")
	fmt.Fprintln(writer, "usage: agentserver-deploy prepare-policy-bootstrap --config=/absolute/active-template.json --output=/absolute/new-bootstrap.json")
	fmt.Fprintln(writer, "usage: agentserver-deploy pin-terminal-revision --config=/absolute/bootstrap.json --output=/absolute/new-bootstrap.json --sandbox-id=<expected-sandbox-id> --revision-id=<published-terminal-revision-id>")
	fmt.Fprintln(writer, "usage: agentserver-deploy retarget-terminal-sandbox --config=/absolute/bootstrap.json --output=/absolute/new-bootstrap.json --expected-sandbox-id=<current-sandbox-id> --sandbox-id=<new-sandbox-id> --revision-id=<published-terminal-revision-id> --environment-id=<fresh-managed-environment-uuid> --managed-sandbox-image=registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-managed-sandbox@sha256:<digest>")
	fmt.Fprintln(writer, "usage: agentserver-deploy retarget-direct-terminal-sandbox --config=/absolute/bootstrap.json --output=/absolute/new-bootstrap.json --expected-sandbox-id=<current-sandbox-id> --sandbox-id=<new-sandbox-id> --revision-id=<published-terminal-revision-id> --environment-id=<fresh-managed-environment-uuid> --managed-sandbox-image=registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-managed-sandbox@sha256:<digest>")
	fmt.Fprintln(writer, "usage: agentserver-deploy lock-release --config=/absolute/template.json --output=/absolute/new-production.json --service-image=IMAGE@sha256:DIGEST --harness-image=IMAGE@sha256:DIGEST --hydra-image=IMAGE@sha256:DIGEST --managed-sandbox-image=IMAGE@sha256:DIGEST --lark-cli-sha256=DIGEST --lark-skill-sha256=DIGEST")
	fmt.Fprintln(writer, "       agentserver-deploy lock-developer-service --config=/absolute/active.json --output=/absolute/new-production.json --service-image=registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-service@sha256:DIGEST")
	fmt.Fprintln(writer, "       agentserver-deploy validate --config=/absolute/path")
	fmt.Fprintln(writer, "       agentserver-deploy render --config=/absolute/path --output=/absolute/directory")
	fmt.Fprintln(writer, "       agentserver-deploy chart --config=/absolute/path --output=/absolute/new-chart")
}
