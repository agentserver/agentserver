//go:build linux || darwin

package main

import (
	"encoding/base64"
	"path/filepath"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/devstack"
	"github.com/agentserver/agentserver/v2/internal/devstacktest"
)

func TestGeneratedDevelopmentStackLoadsCoreAuthorityAndTLS(t *testing.T) {
	fixture, err := devstacktest.Prepare(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	environment, err := devstack.ReadEnvironmentFile(fixture.Prepared.EnvironmentFiles["agentserver-core"])
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := loadDevelopmentBootstrap(fixture.Prepared.BootstrapConfigFile)
	if err != nil {
		t.Fatalf("load generated core bootstrap: %v", err)
	}
	if bootstrap.WorkspaceID != fixture.Config.Authority.WorkspaceID ||
		bootstrap.ExecutorID != fixture.Config.Authority.ExecutorID ||
		bootstrap.Environment.EnvironmentID != fixture.Config.Authority.EnvironmentID {
		t.Fatalf("generated bootstrap authority = %+v", bootstrap)
	}
	if _, err := coreTLSConfig(
		environment[coreTLSCertificateEnvironment], environment[coreTLSKeyEnvironment], environment[coreClientCAEnvironment],
	); err != nil {
		t.Fatalf("load generated core TLS configuration: %v", err)
	}
	cursorKey, err := base64.RawURLEncoding.DecodeString(environment[coreRunCursorKeyEnvironment])
	if err != nil {
		t.Fatal(err)
	}
	if len(cursorKey) != 32 {
		t.Fatalf("generated cursor key bytes = %d", len(cursorKey))
	}
	loginKey, err := base64.RawURLEncoding.DecodeString(environment[coreLoginTransactionKeyEnvironment])
	if err != nil || len(loginKey) != 32 {
		t.Fatalf("generated login transaction key = %d bytes, err %v", len(loginKey), err)
	}
	if environment[coreHydraAdminEnvironment] == "" || environment[coreHydraPublicOriginEnvironment] == "" ||
		environment[coreExternalOIDCIssuerEnvironment] == "" || environment[coreExternalOIDCSecretEnvironment] == "" {
		t.Fatal("generated Core environment omits login bridge configuration")
	}
	if environment[coreDevPromptObjectRootEnvironment] != filepath.Join(fixture.Output, "state", "objects") {
		t.Fatalf("generated object root = %q", environment[coreDevPromptObjectRootEnvironment])
	}
}
