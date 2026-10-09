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
	flag.StringVar(&release.HarnessImage, "harness-image", "", "published harness image with the selected stock runtime bundle")
	flag.BoolVar(&release.UpgradeCodex, "upgrade-codex", false, "explicitly upgrade the previous runtime metadata to the current packaged Codex release")
	flag.BoolVar(&release.UpgradeManagedSkill, "upgrade-managed-skill", false, "select current managed instructions only when the harness was rebuilt with this source")
	flag.StringVar(&release.CNGatewayURL, "cn-gateway-url", "https://"+productiondeploy.ProductionCNSandboxGatewayHostname, "CN external sandbox gateway HTTPS origin")
	flag.StringVar(&release.CNGatewayServerName, "cn-gateway-server-name", productiondeploy.ProductionCNSandboxGatewayBackendHost, "CN sandbox gateway backend TLS server name")
	flag.StringVar(&release.CNEnvironmentID, "cn-environment-id", "73cd7602-c0be-4d9a-96a9-d0c09bf6f689", "CN Kubernetes managed environment UUID")
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
	base, err := productiondeploy.LoadKubernetesReleaseBase(input, release.UpgradeCodex)
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
