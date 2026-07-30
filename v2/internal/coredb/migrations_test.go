package coredb

import (
	"crypto/sha256"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrations(t *testing.T) {
	migrations, err := EmbeddedMigrations()
	if err != nil {
		t.Fatalf("EmbeddedMigrations() error = %v", err)
	}
	if len(migrations) != 3 {
		t.Fatalf("migration count = %d, want 3", len(migrations))
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
