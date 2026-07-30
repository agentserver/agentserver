package coredb

import (
	"strings"
	"testing"
)

func TestPendingMigrationsAcceptsExactPrefix(t *testing.T) {
	catalog := []Migration{
		migrationForTest(1, "first", "SELECT 1;"),
		migrationForTest(2, "second", "SELECT 2;"),
	}
	applied := []AppliedMigration{{
		Version: catalog[0].Version,
		Name:    catalog[0].Name,
		SHA256:  catalog[0].SHA256,
	}}

	pending, err := pendingMigrations(catalog, applied)
	if err != nil {
		t.Fatalf("pendingMigrations() error = %v", err)
	}
	if len(pending) != 1 || pending[0].Version != 2 {
		t.Fatalf("pending migrations = %+v, want only version 2", pending)
	}
}

func TestPendingMigrationsRejectsChangedAppliedMigration(t *testing.T) {
	original := migrationForTest(1, "first", "SELECT 1;")
	mutatedCatalog := []Migration{migrationForTest(1, "first", "SELECT 2;")}
	applied := []AppliedMigration{{Version: 1, Name: "first", SHA256: original.SHA256}}

	_, err := pendingMigrations(mutatedCatalog, applied)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("pendingMigrations() error = %v, want checksum mismatch", err)
	}
}

func TestPendingMigrationsRejectsChangedName(t *testing.T) {
	catalog := []Migration{migrationForTest(1, "first", "SELECT 1;")}
	applied := []AppliedMigration{{Version: 1, Name: "renamed", SHA256: catalog[0].SHA256}}

	_, err := pendingMigrations(catalog, applied)
	if err == nil || !strings.Contains(err.Error(), "name mismatch") {
		t.Fatalf("pendingMigrations() error = %v, want name mismatch", err)
	}
}

func TestPendingMigrationsRejectsUnknownNewerVersion(t *testing.T) {
	catalog := []Migration{migrationForTest(1, "first", "SELECT 1;")}
	applied := []AppliedMigration{
		{Version: 1, Name: "first", SHA256: catalog[0].SHA256},
		{Version: 2, Name: "future"},
	}

	_, err := pendingMigrations(catalog, applied)
	if err == nil || !strings.Contains(err.Error(), "unknown to this binary") || !strings.Contains(err.Error(), "refusing downgrade") {
		t.Fatalf("pendingMigrations() error = %v, want downgrade refusal", err)
	}
}

func TestPendingMigrationsRejectsHistoryGap(t *testing.T) {
	catalog := []Migration{
		migrationForTest(1, "first", "SELECT 1;"),
		migrationForTest(2, "second", "SELECT 2;"),
	}
	applied := []AppliedMigration{{Version: 2, Name: "second", SHA256: catalog[1].SHA256}}

	_, err := pendingMigrations(catalog, applied)
	if err == nil || !strings.Contains(err.Error(), "history has a gap") {
		t.Fatalf("pendingMigrations() error = %v, want history gap", err)
	}
}

func TestPendingMigrationsRejectsCorruptCatalogChecksum(t *testing.T) {
	catalog := []Migration{migrationForTest(1, "first", "SELECT 1;")}
	catalog[0].SHA256[0] ^= 0xff

	_, err := pendingMigrations(catalog, nil)
	if err == nil || !strings.Contains(err.Error(), "catalog checksum") {
		t.Fatalf("pendingMigrations() error = %v, want catalog checksum error", err)
	}
}
