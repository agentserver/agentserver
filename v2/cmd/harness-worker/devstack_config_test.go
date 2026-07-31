//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/devstacktest"
)

func TestGeneratedDevelopmentStackLoadsWorkerDeployment(t *testing.T) {
	fixture, err := devstacktest.Prepare(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	attemptRoot := filepath.Join(fixture.Root, "attempt-root")
	if err := os.Mkdir(attemptRoot, 0o701); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(attemptRoot, 0o701); err != nil {
		t.Fatal(err)
	}
	deployment, err := loadWorkerDeployment(fixture.Prepared.WorkerDeploymentFile, attemptRoot)
	if err != nil {
		t.Fatalf("load generated worker deployment: %v", err)
	}
	if deployment.keyring == nil || deployment.preparer == nil || deployment.controlClient == nil || deployment.executorClient == nil {
		t.Fatalf("generated worker deployment is incomplete: %+v", deployment)
	}
	deployment.controlClient.CloseIdleConnections()
	deployment.executorClient.CloseIdleConnections()
}
