package coredb

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestValidateManagedEnvironmentProfileIsClosedAndManagedOnly(t *testing.T) {
	valid := validManagedEnvironmentProfile()
	if err := validateManagedEnvironmentProfile(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ManagedEnvironmentProfile){
		"workspace":    func(profile *ManagedEnvironmentProfile) { profile.WorkspaceID = "not-a-uuid" },
		"owner digest": func(profile *ManagedEnvironmentProfile) { profile.OwnerPolicySHA256 = [sha256.Size]byte{} },
		"codex commit": func(profile *ManagedEnvironmentProfile) { profile.CodexCommit = strings.Repeat("z", 40) },
		"local kind": func(profile *ManagedEnvironmentProfile) {
			profile.RootDescriptor = json.RawMessage(`{"kind":"local","root":"/workspace"}`)
		},
		"relative root": func(profile *ManagedEnvironmentProfile) {
			profile.RootDescriptor = json.RawMessage(`{"kind":"managed","root":"workspace"}`)
		},
		"escaping cwd": func(profile *ManagedEnvironmentProfile) {
			profile.RootDescriptor = json.RawMessage(`{"kind":"managed","root":"/workspace","defaultCwd":"../escape"}`)
		},
		"unknown descriptor field": func(profile *ManagedEnvironmentProfile) {
			profile.RootDescriptor = json.RawMessage(`{"kind":"managed","root":"/workspace","future":true}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			profile := valid
			mutate(&profile)
			if err := validateManagedEnvironmentProfile(profile); err == nil {
				t.Fatal("invalid managed environment profile was accepted")
			}
		})
	}
}

func TestPostgreSQLManagedEnvironmentProfileBootstrapIsExactAndIdempotent(t *testing.T) {
	connectionConfig := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, connectionConfig)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrateConfig(t.Context(), connectionConfig, runnerConfig{
		schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog,
	}); err != nil {
		t.Fatal(err)
	}
	profile := validManagedEnvironmentProfile()
	connection := openPostgresTestConnection(t, connectionConfig)
	quotedSchema := quoteIdentifier(schema)
	if _, err := connection.Exec(t.Context(), fmt.Sprintf(
		"INSERT INTO %s.workspaces (id, status, managed_lark_credential_mode) VALUES ($1, 'active', 'webhook_swap')", quotedSchema,
	), profile.WorkspaceID); err != nil {
		connection.Close(t.Context())
		t.Fatal(err)
	}
	if _, err := connection.Exec(t.Context(), fmt.Sprintf(
		"INSERT INTO %s.executors (id, workspace_id, status) VALUES ($1, $2, 'enrolling')", quotedSchema,
	), profile.ExecutorID, profile.WorkspaceID); err != nil {
		connection.Close(t.Context())
		t.Fatal(err)
	}
	connection.Close(t.Context())

	result, err := bootstrapManagedEnvironmentProfileConfig(t.Context(), connectionConfig, schema, profile)
	if err != nil || !result.Created || result.SchemaVersion != int64(len(catalog)) {
		t.Fatalf("first managed profile bootstrap = %+v, error = %v", result, err)
	}
	retry, err := bootstrapManagedEnvironmentProfileConfig(t.Context(), connectionConfig, schema, profile)
	if err != nil || retry.Created || retry.SchemaVersion != result.SchemaVersion {
		t.Fatalf("repeat managed profile bootstrap = %+v, error = %v", retry, err)
	}

	connection = openPostgresTestConnection(t, connectionConfig)
	defer connection.Close(t.Context())
	var backendKind, status, platform, rootKind string
	var insecure bool
	if err := connection.QueryRow(t.Context(), fmt.Sprintf(`
SELECT backend_kind, status, platform, insecure_dev, root_descriptor->>'kind'
FROM %s.executor_environments
WHERE id = $1`, quotedSchema), profile.EnvironmentID).Scan(
		&backendKind, &status, &platform, &insecure, &rootKind,
	); err != nil {
		t.Fatal(err)
	}
	if backendKind != DispatchTargetTAE || status != ExecutorEnvironmentStatusOnline || platform != "linux-amd64" || insecure || rootKind != "managed" {
		t.Fatalf("stored managed profile = backend %q status %q platform %q insecure %v root kind %q",
			backendKind, status, platform, insecure, rootKind)
	}

	conflicting := profile
	conflicting.OwnerPolicySHA256 = sha256.Sum256([]byte("different managed policy"))
	if _, err := bootstrapManagedEnvironmentProfileConfig(t.Context(), connectionConfig, schema, conflicting); !errors.Is(err, ErrManagedEnvironmentProfileConflict) {
		t.Fatalf("conflicting managed profile bootstrap error = %v", err)
	}
	var storedOwner []byte
	if err := connection.QueryRow(t.Context(), fmt.Sprintf(
		"SELECT owner_policy_sha256 FROM %s.executor_environments WHERE id = $1", quotedSchema,
	), profile.EnvironmentID).Scan(&storedOwner); err != nil {
		t.Fatal(err)
	}
	if string(storedOwner) != string(profile.OwnerPolicySHA256[:]) {
		t.Fatal("conflicting bootstrap changed the stored managed owner policy")
	}
}

func validManagedEnvironmentProfile() ManagedEnvironmentProfile {
	return ManagedEnvironmentProfile{
		WorkspaceID:   "40000000-0000-4000-8000-000000000004",
		ExecutorID:    "20000000-0000-4000-8000-000000000002",
		EnvironmentID: "60000000-0000-4000-8000-000000000008",
		RootDescriptor: json.RawMessage(
			`{"kind":"managed","root":"/workspace","displayName":"Managed SG","defaultCwd":"."}`,
		),
		OwnerPolicySHA256: sha256.Sum256([]byte("managed owner policy")),
		CodexRelease:      "0.146.0-managed",
		CodexCommit:       strings.Repeat("a", 40),
		CodexSHA256:       sha256.Sum256([]byte("managed runtime codex")),
	}
}
