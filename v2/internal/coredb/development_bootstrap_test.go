package coredb

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
)

func TestValidateInsecureDevelopmentBootstrap(t *testing.T) {
	bootstrap := validInsecureDevelopmentBootstrap()
	if err := validateInsecureDevelopmentBootstrap(bootstrap); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*InsecureDevelopmentBootstrap){
		"workspace identity": func(value *InsecureDevelopmentBootstrap) { value.WorkspaceID = "not-a-uuid" },
		"runtime digest":     func(value *InsecureDevelopmentBootstrap) { value.RuntimeManifestSHA256 = [sha256.Size]byte{} },
		"agentx version":     func(value *InsecureDevelopmentBootstrap) { value.AgentxVersion = "" },
		"OIDC issuer":        func(value *InsecureDevelopmentBootstrap) { value.ExternalOIDCIssuer = "https://idp.example" },
		"OIDC subject":       func(value *InsecureDevelopmentBootstrap) { value.ExternalOIDCSubject = "" },
		"environment identity": func(value *InsecureDevelopmentBootstrap) {
			value.Environment.EnvironmentID = "not-a-uuid"
		},
		"production environment": func(value *InsecureDevelopmentBootstrap) { value.Environment.InsecureDev = false },
		"incomplete profile": func(value *InsecureDevelopmentBootstrap) {
			value.Environment.OuterProfileVersion = execprofile.Version
		},
		"root descriptor": func(value *InsecureDevelopmentBootstrap) {
			value.Environment.RootDescriptor = json.RawMessage(`[]`)
		},
		"owner policy": func(value *InsecureDevelopmentBootstrap) {
			value.Environment.OwnerPolicySHA256 = [sha256.Size]byte{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validInsecureDevelopmentBootstrap()
			mutate(&candidate)
			if err := validateInsecureDevelopmentBootstrap(candidate); err == nil {
				t.Fatal("invalid development bootstrap was accepted")
			}
		})
	}
}

func TestBootstrapInsecureDevelopmentRejectsInvalidURLWithoutLeakingCredential(t *testing.T) {
	credential := "development-bootstrap-secret"
	databaseURL := "postgres://user:" + credential + "@%zz/database"
	_, err := BootstrapInsecureDevelopment(t.Context(), databaseURL, validInsecureDevelopmentBootstrap())
	if err == nil || strings.Contains(err.Error(), credential) || !strings.Contains(err.Error(), "invalid PostgreSQL") {
		t.Fatalf("BootstrapInsecureDevelopment() error = %v", err)
	}
}

func TestDevelopmentBootstrapConflictSentinel(t *testing.T) {
	err := developmentBootstrapConflict("executor", "20000000-0000-4000-8000-000000000002")
	if !errors.Is(err, ErrInsecureDevelopmentBootstrapConflict) {
		t.Fatalf("development conflict = %v", err)
	}
}

func validInsecureDevelopmentBootstrap() InsecureDevelopmentBootstrap {
	digest := func(value string) [sha256.Size]byte { return sha256.Sum256([]byte(value)) }
	return InsecureDevelopmentBootstrap{
		WorkspaceID:         "40000000-0000-4000-8000-000000000004",
		SessionID:           "50000000-0000-4000-8000-000000000005",
		ActorID:             "10000000-0000-4000-8000-000000000001",
		ExternalOIDCIssuer:  "http://127.0.0.1:17447/idp",
		ExternalOIDCSubject: "agentserver-dev-user",
		ExecutorID:          "20000000-0000-4000-8000-000000000002",
		MachineKeySHA256:    digest("development-machine-key"), AgentxVersion: "0.1.0-dev",
		RuntimeManifestSHA256:    digest("development-runtime-manifest"),
		ExecProtocolSourceSHA256: digest("development-exec-protocol"),
		Environment: InsecureDevelopmentEnvironment{
			EnvironmentID:     "60000000-0000-4000-8000-000000000006",
			RootDescriptor:    json.RawMessage(`{"kind":"local","root":"/workspace"}`),
			OwnerPolicySHA256: digest("development-owner-policy"),
			Platform:          "linux-arm64", CodexRelease: "0.146.0", CodexCommit: strings.Repeat("a", 40),
			CodexSHA256: digest("development-codex"), OuterProfileVersion: execprofile.FilesystemReadVersion,
			ProcessMethods: append([]string(nil), execprofile.ProcessMethods()...), InsecureDev: true,
		},
	}
}
