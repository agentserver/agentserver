package main

import (
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/productiondeploytest"
)

func TestProductionRendererEgressEnvironmentPassesCommandLoader(t *testing.T) {
	environment, err := productiondeploytest.ExampleDeploymentEnvironment("egress-authorizer")
	if err != nil {
		if strings.Contains(err.Error(), "has no deployment egress-authorizer") {
			return
		}
		t.Fatal(err)
	}
	config, err := loadEgressAuthorizerConfig(environment.Get, egressAuthorizerServeProduction)
	if err != nil {
		t.Fatalf("egress-authorizer rejected rendered production environment: %v", err)
	}
	if config.allowedTAEPSM == "" || config.placeholderKeyring == "" || config.decisionTimeout != defaultEgressDecisionTimeout {
		t.Fatalf("rendered egress authority = %+v", config)
	}
}
