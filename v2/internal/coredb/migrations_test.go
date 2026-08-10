package coredb

import (
	"crypto/sha256"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrations(t *testing.T) {
	migrations, err := EmbeddedMigrations()
	if err != nil {
		t.Fatalf("EmbeddedMigrations() error = %v", err)
	}
	if len(migrations) != 27 {
		t.Fatalf("migration count = %d, want 27", len(migrations))
	}
	migration := migrations[0]
	if migration.Version != 1 || migration.Name != "session_run_kernel" {
		t.Fatalf("migration identity = %04d_%s, want 0001_session_run_kernel", migration.Version, migration.Name)
	}
	if got := sha256.Sum256([]byte(migration.SQL)); got != migration.SHA256 {
		t.Fatalf("migration checksum = %x, want %x", migration.SHA256, got)
	}
	if migrations[1].Version != 2 || migrations[1].Name != "state_kernel_commands" {
		t.Fatalf("second migration identity = %04d_%s, want 0002_state_kernel_commands", migrations[1].Version, migrations[1].Name)
	}
	if migrations[2].Version != 3 || migrations[2].Name != "execution_operation_kernel" {
		t.Fatalf("third migration identity = %04d_%s, want 0003_execution_operation_kernel", migrations[2].Version, migrations[2].Name)
	}
	if migrations[3].Version != 4 || migrations[3].Name != "executor_connection_kernel" {
		t.Fatalf("fourth migration identity = %04d_%s, want 0004_executor_connection_kernel", migrations[3].Version, migrations[3].Name)
	}
	if migrations[4].Version != 5 || migrations[4].Name != "optional_operation_skip" {
		t.Fatalf("fifth migration identity = %04d_%s, want 0005_optional_operation_skip", migrations[4].Version, migrations[4].Name)
	}
	if migrations[5].Version != 6 || migrations[5].Name != "filesystem_read_profile" {
		t.Fatalf("sixth migration identity = %04d_%s, want 0006_filesystem_read_profile", migrations[5].Version, migrations[5].Name)
	}
	if migrations[6].Version != 7 || migrations[6].Name != "brain_tool_catalog_kernel" {
		t.Fatalf("seventh migration identity = %04d_%s, want 0007_brain_tool_catalog_kernel", migrations[6].Version, migrations[6].Name)
	}
	if migrations[7].Version != 8 || migrations[7].Name != "run_launch_authority" {
		t.Fatalf("eighth migration identity = %04d_%s, want 0008_run_launch_authority", migrations[7].Version, migrations[7].Name)
	}
	if migrations[8].Version != 9 || migrations[8].Name != "browser_run_projection" {
		t.Fatalf("ninth migration identity = %04d_%s, want 0009_browser_run_projection", migrations[8].Version, migrations[8].Name)
	}
	if migrations[9].Version != 10 || migrations[9].Name != "checkpoint_artifact_profile" {
		t.Fatalf("tenth migration identity = %04d_%s, want 0010_checkpoint_artifact_profile", migrations[9].Version, migrations[9].Name)
	}
	if migrations[10].Version != 11 || migrations[10].Name != "run_finalization_identity" {
		t.Fatalf("eleventh migration identity = %04d_%s, want 0011_run_finalization_identity", migrations[10].Version, migrations[10].Name)
	}
	if migrations[11].Version != 12 || migrations[11].Name != "approval_authority" {
		t.Fatalf("twelfth migration identity = %04d_%s, want 0012_approval_authority", migrations[11].Version, migrations[11].Name)
	}
	if migrations[12].Version != 13 || migrations[12].Name != "oidc_login_bridge" {
		t.Fatalf("thirteenth migration identity = %04d_%s, want 0013_oidc_login_bridge", migrations[12].Version, migrations[12].Name)
	}
	if migrations[13].Version != 14 || migrations[13].Name != "executor_enrollment_identity" {
		t.Fatalf("fourteenth migration identity = %04d_%s, want 0014_executor_enrollment_identity", migrations[13].Version, migrations[13].Name)
	}
	if migrations[14].Version != 15 || migrations[14].Name != "executor_gateway_recovery" {
		t.Fatalf("fifteenth migration identity = %04d_%s, want 0015_executor_gateway_recovery", migrations[14].Version, migrations[14].Name)
	}
	if migrations[15].Version != 16 || migrations[15].Name != "workspace_llm_gateway" {
		t.Fatalf("sixteenth migration identity = %04d_%s, want 0016_workspace_llm_gateway", migrations[15].Version, migrations[15].Name)
	}
	if migrations[16].Version != 17 || migrations[16].Name != "production_bootstrap_seed" {
		t.Fatalf("seventeenth migration identity = %04d_%s, want 0017_production_bootstrap_seed", migrations[16].Version, migrations[16].Name)
	}
	if migrations[17].Version != 18 || migrations[17].Name != "platform_workspace_resources" {
		t.Fatalf("eighteenth migration identity = %04d_%s, want 0018_platform_workspace_resources", migrations[17].Version, migrations[17].Name)
	}
	if migrations[18].Version != 19 || migrations[18].Name != "user_sessions" {
		t.Fatalf("nineteenth migration identity = %04d_%s, want 0019_user_sessions", migrations[18].Version, migrations[18].Name)
	}
	if migrations[19].Version != 20 || migrations[19].Name != "managed_execution_targets" {
		t.Fatalf("twentieth migration identity = %04d_%s, want 0020_managed_execution_targets", migrations[19].Version, migrations[19].Name)
	}
	if migrations[20].Version != 21 || migrations[20].Name != "managed_lark_egress_authority" {
		t.Fatalf("twenty-first migration identity = %04d_%s, want 0021_managed_lark_egress_authority", migrations[20].Version, migrations[20].Name)
	}
	if migrations[21].Version != 22 || migrations[21].Name != "managed_lark_grant_refresh" {
		t.Fatalf("twenty-second migration identity = %04d_%s, want 0022_managed_lark_grant_refresh", migrations[21].Version, migrations[21].Name)
	}
	if migrations[22].Version != 23 || migrations[22].Name != "checkpoint_tool_pack_authority" {
		t.Fatalf("twenty-third migration identity = %04d_%s, want 0023_checkpoint_tool_pack_authority", migrations[22].Version, migrations[22].Name)
	}
	if migrations[23].Version != 24 || migrations[23].Name != "workspace_credential_bindings" {
		t.Fatalf("twenty-fourth migration identity = %04d_%s, want 0024_workspace_credential_bindings", migrations[23].Version, migrations[23].Name)
	}
	if migrations[24].Version != 25 || migrations[24].Name != "workspace_credential_audit_context" {
		t.Fatalf("twenty-fifth migration identity = %04d_%s, want 0025_workspace_credential_audit_context", migrations[24].Version, migrations[24].Name)
	}
	if migrations[25].Version != 26 || migrations[25].Name != "workspace_credential_process_env_audit" {
		t.Fatalf("twenty-sixth migration identity = %04d_%s, want 0026_workspace_credential_process_env_audit", migrations[25].Version, migrations[25].Name)
	}
	if migrations[26].Version != 27 || migrations[26].Name != "workspace_managed_credential_mode" {
		t.Fatalf("twenty-seventh migration identity = %04d_%s, want 0027_workspace_managed_credential_mode", migrations[26].Version, migrations[26].Name)
	}
	if !strings.Contains(migrations[25].SQL, "'process_env'") {
		t.Fatal("process environment audit migration does not admit the process_env stage")
	}
	for _, required := range []string{
		"ADD COLUMN managed_lark_credential_mode",
		"ALTER COLUMN managed_lark_credential_mode DROP DEFAULT",
		"CREATE TABLE workspace_managed_credential_mode_events",
		"previous_mode <> current_mode",
	} {
		if !strings.Contains(migrations[26].SQL, required) {
			t.Fatalf("workspace mode migration is missing %q", required)
		}
	}
}

func TestLoadMigrationsRejectsInvalidCatalog(t *testing.T) {
	tests := []struct {
		name      string
		files     fstest.MapFS
		wantError string
	}{
		{
			name:      "empty catalog",
			files:     fstest.MapFS{},
			wantError: "catalog is empty",
		},
		{
			name: "invalid filename",
			files: fstest.MapFS{
				"migrations/01_bad.sql": {Data: []byte("SELECT 1;\n")},
			},
			wantError: "must match NNNN_name.sql",
		},
		{
			name: "zero version",
			files: fstest.MapFS{
				"migrations/0000_bad.sql": {Data: []byte("SELECT 1;\n")},
			},
			wantError: "versions must be continuous",
		},
		{
			name: "version gap",
			files: fstest.MapFS{
				"migrations/0001_first.sql": {Data: []byte("SELECT 1;\n")},
				"migrations/0003_third.sql": {Data: []byte("SELECT 3;\n")},
			},
			wantError: "versions must be continuous",
		},
		{
			name: "empty SQL",
			files: fstest.MapFS{
				"migrations/0001_empty.sql": {Data: []byte(" \n\t")},
			},
			wantError: "is empty",
		},
		{
			name: "byte order mark",
			files: fstest.MapFS{
				"migrations/0001_bom.sql": {Data: []byte("\xef\xbb\xbfSELECT 1;\n")},
			},
			wantError: "byte order mark",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadMigrations(test.files)
			if err == nil || !contains(err.Error(), test.wantError) {
				t.Fatalf("loadMigrations() error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func migrationForTest(version int64, name, sql string) Migration {
	return Migration{
		Version: version,
		Name:    name,
		SQL:     sql,
		SHA256:  sha256.Sum256([]byte(sql)),
	}
}

func contains(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return substring == ""
}
