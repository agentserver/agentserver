package main

import (
	"encoding/json"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/productiondeploytest"
)

func TestProductionRendererWorkerDocumentPassesCommandValidator(t *testing.T) {
	raw, err := productiondeploytest.ExampleWorkerDeployment()
	if err != nil {
		t.Fatal(err)
	}
	var document workerDeploymentDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkerDeploymentDocument(document); err != nil {
		t.Fatalf("production renderer emitted a worker config rejected by harness-worker: %v", err)
	}
}
