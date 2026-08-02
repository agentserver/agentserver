package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/productiondeploy"
)

type deployCommands struct {
	load       func(string) (productiondeploy.LoadedConfig, error)
	render     func(productiondeploy.LoadedConfig) (productiondeploy.Bundle, error)
	write      func(productiondeploy.Bundle, string) error
	chart      func(productiondeploy.LoadedConfig) (productiondeploy.HelmChart, error)
	writeChart func(productiondeploy.HelmChart, string) error
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, deployCommands{
		load: productiondeploy.LoadConfig, render: productiondeploy.Render, write: productiondeploy.WriteBundle,
		chart: productiondeploy.RenderHelmChart, writeChart: productiondeploy.WriteHelmChart,
	}))
}

func run(arguments []string, stdout, stderr io.Writer, commands deployCommands) int {
	if len(arguments) == 0 {
		writeUsage(stderr)
		return 2
	}
	switch arguments[0] {
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
	fmt.Fprintln(writer, "usage: agentserver-deploy validate --config=/absolute/path")
	fmt.Fprintln(writer, "       agentserver-deploy render --config=/absolute/path --output=/absolute/directory")
	fmt.Fprintln(writer, "       agentserver-deploy chart --config=/absolute/path --output=/absolute/new-chart")
}
