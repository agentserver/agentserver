package coredb

import (
	"errors"
	"fmt"
	"time"
)

func managedSandboxColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "id::text, " +
		alias + "workspace_id::text, " +
		alias + "session_id::text, " +
		alias + "environment_id::text, " +
		alias + "provider_kind, " +
		alias + "generation, " +
		alias + "desired_state, " +
		alias + "observed_state, " +
		alias + "provider_region, " +
		alias + "provider_psm, " +
		alias + "provider_session_ref, " +
		alias + "create_idempotency_key::text, " +
		alias + "runtime_profile_digest, " +
		alias + "pack_set_digest, " +
		alias + "requested_ttl_seconds, " +
		alias + "idle_ttl_seconds, " +
		alias + "expires_at, " +
		alias + "idle_expires_at, " +
		alias + "last_observed_at, " +
		alias + "version, " +
		alias + "created_at, " +
		alias + "updated_at, " +
		alias + "deleted_at, " +
		alias + "last_error_code, " +
		alias + "last_error_digest"
}

func scanManagedSandbox(scanner rowScanner) (ManagedSandbox, error) {
	var sandbox ManagedSandbox
	var providerSessionRef *string
	var runtimeDigest []byte
	var packDigest []byte
	var requestedTTLSeconds int64
	var idleTTLSeconds int64
	var expiresAt *time.Time
	var idleExpiresAt *time.Time
	var lastObservedAt *time.Time
	var deletedAt *time.Time
	var lastErrorCode *string
	var lastErrorDigest []byte
	err := scanner.Scan(
		&sandbox.ID, &sandbox.WorkspaceID, &sandbox.SessionID, &sandbox.EnvironmentID,
		&sandbox.ProviderKind, &sandbox.Generation, &sandbox.DesiredState, &sandbox.ObservedState,
		&sandbox.ProviderRegion, &sandbox.ProviderPSM, &providerSessionRef,
		&sandbox.CreateIdempotencyKey, &runtimeDigest, &packDigest,
		&requestedTTLSeconds, &idleTTLSeconds,
		&expiresAt, &idleExpiresAt, &lastObservedAt,
		&sandbox.Version, &sandbox.CreatedAt, &sandbox.UpdatedAt, &deletedAt,
		&lastErrorCode, &lastErrorDigest,
	)
	if err != nil {
		return ManagedSandbox{}, err
	}
	if len(runtimeDigest) != 32 || len(packDigest) != 32 {
		return ManagedSandbox{}, errors.New("managed sandbox row contains an invalid profile digest")
	}
	copy(sandbox.RuntimeProfileDigest[:], runtimeDigest)
	copy(sandbox.PackSetDigest[:], packDigest)
	sandbox.RequestedTTL = time.Duration(requestedTTLSeconds) * time.Second
	sandbox.IdleTTL = time.Duration(idleTTLSeconds) * time.Second
	if providerSessionRef != nil {
		sandbox.ProviderSessionRef = *providerSessionRef
	}
	sandbox.ExpiresAt = expiresAt
	sandbox.IdleExpiresAt = idleExpiresAt
	sandbox.LastObservedAt = lastObservedAt
	sandbox.DeletedAt = deletedAt
	if lastErrorCode != nil {
		sandbox.LastErrorCode = *lastErrorCode
	}
	if lastErrorDigest != nil {
		if len(lastErrorDigest) != 32 {
			return ManagedSandbox{}, fmt.Errorf("managed sandbox %s contains an invalid error digest", sandbox.ID)
		}
		var digest [32]byte
		copy(digest[:], lastErrorDigest)
		sandbox.LastErrorDigest = &digest
	}
	return sandbox, nil
}
