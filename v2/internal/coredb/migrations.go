// Package coredb owns the PostgreSQL schema and migration history used by
// agentserver-core.
package coredb

import (
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

const maxMigrationBytes = 16 * 1024 * 1024

var (
	migrationFilenamePattern = regexp.MustCompile(`^([0-9]{4})_([a-z][a-z0-9_]{0,62})\.sql$`)
	migrationNamePattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

//go:embed migrations/*.sql
var embeddedMigrationFiles embed.FS

// Migration is one immutable, forward-only schema change.
type Migration struct {
	Version int64
	Name    string
	SQL     string
	SHA256  [sha256.Size]byte
}

// EmbeddedMigrations returns the migrations compiled into this binary.
func EmbeddedMigrations() ([]Migration, error) {
	return loadMigrations(embeddedMigrationFiles)
}

func loadMigrations(migrationFS fs.FS) ([]Migration, error) {
	paths, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("enumerate embedded migrations: %w", err)
	}
	if len(paths) == 0 {
		return nil, errors.New("migration catalog is empty")
	}

	migrations := make([]Migration, 0, len(paths))
	for _, path := range paths {
		filename := strings.TrimPrefix(path, "migrations/")
		matches := migrationFilenamePattern.FindStringSubmatch(filename)
		if matches == nil {
			return nil, fmt.Errorf("migration filename %q must match NNNN_name.sql", filename)
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version in %q: %w", filename, err)
		}
		contents, err := fs.ReadFile(migrationFS, path)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", filename, err)
		}
		if len(contents) > maxMigrationBytes {
			return nil, fmt.Errorf("migration %q exceeds %d bytes", filename, maxMigrationBytes)
		}
		if len(contents) > 0 && contents[0] == 0xef && len(contents) >= 3 && contents[1] == 0xbb && contents[2] == 0xbf {
			return nil, fmt.Errorf("migration %q must not contain a UTF-8 byte order mark", filename)
		}
		if strings.TrimSpace(string(contents)) == "" {
			return nil, fmt.Errorf("migration %q is empty", filename)
		}

		migrations = append(migrations, Migration{
			Version: version,
			Name:    matches[2],
			SQL:     string(contents),
			SHA256:  sha256.Sum256(contents),
		})
	}
	if err := validateCatalog(migrations); err != nil {
		return nil, err
	}
	return migrations, nil
}

func validateCatalog(migrations []Migration) error {
	if len(migrations) == 0 {
		return errors.New("migration catalog is empty")
	}
	for index, migration := range migrations {
		expectedVersion := int64(index + 1)
		if migration.Version != expectedVersion {
			return fmt.Errorf("migration catalog has version %04d at position %04d; versions must be continuous from 0001", migration.Version, expectedVersion)
		}
		if !migrationNamePattern.MatchString(migration.Name) {
			return fmt.Errorf("migration %04d has invalid name %q", migration.Version, migration.Name)
		}
		if strings.TrimSpace(migration.SQL) == "" {
			return fmt.Errorf("migration %04d_%s is empty", migration.Version, migration.Name)
		}
		actualHash := sha256.Sum256([]byte(migration.SQL))
		if migration.SHA256 != actualHash {
			return fmt.Errorf("migration %04d_%s catalog checksum does not match its SQL", migration.Version, migration.Name)
		}
	}
	return nil
}
