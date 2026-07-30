package coredb

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrateRejectsInvalidURLWithoutLeakingCredential(t *testing.T) {
	const databaseURL = "postgres://migration-user:do-not-print-this@[invalid"
	_, err := Migrate(t.Context(), databaseURL)
	if err == nil {
		t.Fatal("Migrate() error = nil, want invalid URL error")
	}
	if strings.Contains(err.Error(), "do-not-print-this") || strings.Contains(err.Error(), databaseURL) {
		t.Fatalf("Migrate() leaked database credential in error: %v", err)
	}
}

func TestSafeConnectErrorRedactsPassword(t *testing.T) {
	config, err := pgx.ParseConfig("postgres://migration-user:do-not-print-this@localhost/agentserver")
	if err != nil {
		t.Fatal(err)
	}
	safe := safeConnectError(config, errors.New("authentication failed for do-not-print-this"))
	if strings.Contains(safe.Error(), "do-not-print-this") {
		t.Fatalf("safeConnectError() leaked password: %v", safe)
	}
	if !strings.Contains(safe.Error(), "[REDACTED]") {
		t.Fatalf("safeConnectError() = %v, want redaction marker", safe)
	}
}
