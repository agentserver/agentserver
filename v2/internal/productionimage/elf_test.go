package productionimage

import (
	"debug/buildinfo"
	"runtime/debug"
	"strings"
	"testing"
)

func TestProductionSandboxGatewayRequiresTAEProviderModuleIdentity(t *testing.T) {
	provider := &buildinfo.BuildInfo{
		GoVersion: GoToolchain,
		Path:      "github.com/agentserver/agentserver/v2/providers/tae/cmd/sandbox-gateway",
		Main:      debug.Module{Path: "github.com/agentserver/agentserver/v2/providers/tae"},
	}
	if err := validateGoExecutableIdentity("sandbox-gateway", "sandbox-gateway", provider); err != nil {
		t.Fatal(err)
	}

	rootFake := *provider
	rootFake.Path = "github.com/agentserver/agentserver/v2/cmd/sandbox-gateway"
	rootFake.Main.Path = "github.com/agentserver/agentserver/v2"
	if err := validateGoExecutableIdentity("sandbox-gateway", "sandbox-gateway", &rootFake); err == nil ||
		!strings.Contains(err.Error(), "unexpected toolchain or main package") {
		t.Fatalf("root fake sandbox-gateway identity error = %v", err)
	}
}

func TestProductionRootServiceIdentityRemainsRootModuleBound(t *testing.T) {
	information := &buildinfo.BuildInfo{
		GoVersion: GoToolchain,
		Path:      "github.com/agentserver/agentserver/v2/cmd/egress-authorizer",
		Main:      debug.Module{Path: "github.com/agentserver/agentserver/v2"},
	}
	if err := validateGoExecutableIdentity("egress-authorizer", "egress-authorizer", information); err != nil {
		t.Fatal(err)
	}
	information.Main.Path = "github.com/agentserver/agentserver/v2/providers/tae"
	if err := validateGoExecutableIdentity("egress-authorizer", "egress-authorizer", information); err == nil {
		t.Fatal("root service binary from the provider module was accepted")
	}
}
