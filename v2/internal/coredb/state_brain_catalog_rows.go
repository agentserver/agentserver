package coredb

import (
	"crypto/sha256"
	"fmt"
)

func scanBrainToolCatalog(scanner rowScanner) (BrainToolCatalog, error) {
	var catalog BrainToolCatalog
	var threadID *string
	var catalogDigest []byte
	var policyContextDigest []byte
	err := scanner.Scan(
		&catalog.ID,
		&catalog.WorkspaceID,
		&catalog.SessionID,
		&catalog.CreatedRunID,
		&catalog.CreatedRunAttemptID,
		&catalog.CreatedAttemptGeneration,
		&catalog.CreatedHolderID,
		&catalog.CreatedRunVersion,
		&catalog.CreatedAttemptVersion,
		&threadID,
		&catalog.ContractVersion,
		&catalog.CanonicalizerVersion,
		&catalog.CanonicalCatalog,
		&catalogDigest,
		&catalog.PolicyVersion,
		&policyContextDigest,
		&catalog.Version,
		&catalog.CreatedAt,
		&catalog.UpdatedAt,
	)
	if err != nil {
		return BrainToolCatalog{}, err
	}
	if threadID != nil {
		catalog.ThreadID = *threadID
	}
	if len(catalogDigest) != sha256.Size || len(policyContextDigest) != sha256.Size {
		return BrainToolCatalog{}, fmt.Errorf("brain tool catalog %s contains an invalid digest length", catalog.ID)
	}
	copy(catalog.CatalogDigest[:], catalogDigest)
	copy(catalog.PolicyContextDigest[:], policyContextDigest)
	catalog.CanonicalCatalog = append([]byte(nil), catalog.CanonicalCatalog...)
	return catalog, nil
}

func brainToolCatalogColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "id::text, " +
		alias + "workspace_id::text, " +
		alias + "session_id::text, " +
		alias + "created_run_id::text, " +
		alias + "created_run_attempt_id::text, " +
		alias + "created_attempt_generation, " +
		alias + "created_holder_id, " +
		alias + "created_run_version, " +
		alias + "created_attempt_version, " +
		alias + "thread_id, " +
		alias + "contract_version, " +
		alias + "canonicalizer_version, " +
		alias + "canonical_catalog, " +
		alias + "catalog_digest, " +
		alias + "policy_version, " +
		alias + "policy_context_digest, " +
		alias + "version, " +
		alias + "created_at, " +
		alias + "updated_at"
}
