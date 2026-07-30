package coredb

import (
	"crypto/sha256"
	"fmt"
)

// AppliedMigration is an immutable migration history row read from PostgreSQL.
type AppliedMigration struct {
	Version int64
	Name    string
	SHA256  [sha256.Size]byte
}

func pendingMigrations(catalog []Migration, applied []AppliedMigration) ([]Migration, error) {
	if err := validateCatalog(catalog); err != nil {
		return nil, err
	}

	for index, recorded := range applied {
		expectedVersion := int64(index + 1)
		if recorded.Version != expectedVersion {
			return nil, fmt.Errorf("migration history has a gap: expected version %04d, found %04d", expectedVersion, recorded.Version)
		}
		if index >= len(catalog) {
			return nil, fmt.Errorf("database has migration %04d_%s, which is unknown to this binary; refusing downgrade", recorded.Version, recorded.Name)
		}

		expected := catalog[index]
		if recorded.Name != expected.Name {
			return nil, fmt.Errorf("migration %04d name mismatch: database has %q, binary has %q", recorded.Version, recorded.Name, expected.Name)
		}
		if recorded.SHA256 != expected.SHA256 {
			return nil, fmt.Errorf("migration %04d_%s checksum mismatch; an applied migration must never be edited", recorded.Version, recorded.Name)
		}
	}

	pending := make([]Migration, len(catalog)-len(applied))
	copy(pending, catalog[len(applied):])
	return pending, nil
}
