// This command prepares artifacts only; Pulumi remains the deployment writer.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/agentserver/agentserver/v2/internal/productiondeploy"
)

func main() {
	config := flag.String("config", "", "previous deployed production JSON (no secret values)")
	output := flag.String("output", "", "new production JSON")
	var release productiondeploy.KubernetesRelease
	flag.StringVar(&release.ServiceImage, "service-image", "", "published service image")
	flag.StringVar(&release.HarnessImage, "harness-image", "", "published harness image retaining the base runtime bundle")
	flag.StringVar(&release.RuntimeImage, "runtime-image", "", "published Kubernetes runtime image")
	flag.StringVar(&release.GatewayImage, "gateway-image", "", "published Kubernetes gateway image")
	flag.StringVar(&release.EnvironmentID, "environment-id", "", "Kubernetes-only environment UUID")
	flag.StringVar(&release.APICIDR, "api-cidr", "", "in-cluster Kubernetes API endpoint /32")
	flag.BoolVar(&release.AllWorkspaces, "all-workspaces", false, "cut over all workspace defaults, retaining per-session authorization")
	flag.Parse()
	if err := run(*config, *output, release); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(input, output string, release productiondeploy.KubernetesRelease) error {
	base, err := productiondeploy.LoadConfig(input)
	if err != nil {
		return err
	}
	config, err := productiondeploy.PrepareKubernetesRelease(base, release)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(config.Document, "", "  ")
	if err != nil {
		return err
	}
	return productiondeploy.WriteReleaseConfig(append(raw, '\n'), output)
}
