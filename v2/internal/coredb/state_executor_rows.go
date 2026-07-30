package coredb

import (
	"crypto/sha256"
	"fmt"
)

func scanExecutorConnection(scanner rowScanner) (ExecutorConnection, error) {
	var connection ExecutorConnection
	var runtimeManifestSHA256 []byte
	var execProtocolSourceSHA256 []byte
	var environmentSetSHA256 []byte
	err := scanner.Scan(
		&connection.ExecutorID,
		&connection.Generation,
		&connection.ConnectionID,
		&connection.SessionID,
		&connection.GatewayInstanceID,
		&connection.AgentxVersion,
		&runtimeManifestSHA256,
		&execProtocolSourceSHA256,
		&environmentSetSHA256,
		&connection.Status,
		&connection.ExpiresAt,
		&connection.AcquiredAt,
		&connection.RenewedAt,
		&connection.Version,
	)
	if err != nil {
		return ExecutorConnection{}, err
	}
	if len(runtimeManifestSHA256) != sha256.Size {
		return ExecutorConnection{}, fmt.Errorf("executor connection %s has invalid %d-byte runtime manifest hash", connection.ExecutorID, len(runtimeManifestSHA256))
	}
	if len(execProtocolSourceSHA256) != sha256.Size {
		return ExecutorConnection{}, fmt.Errorf("executor connection %s has invalid %d-byte exec protocol source hash", connection.ExecutorID, len(execProtocolSourceSHA256))
	}
	copy(connection.RuntimeManifestSHA256[:], runtimeManifestSHA256)
	copy(connection.ExecProtocolSourceSHA256[:], execProtocolSourceSHA256)
	if len(environmentSetSHA256) != sha256.Size {
		return ExecutorConnection{}, fmt.Errorf("executor connection %s has invalid %d-byte environment set hash", connection.ExecutorID, len(environmentSetSHA256))
	}
	copy(connection.EnvironmentSetSHA256[:], environmentSetSHA256)
	return connection, nil
}

func executorConnectionColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "executor_id::text, " +
		alias + "generation, " +
		alias + "connection_id::text, " +
		alias + "session_id::text, " +
		alias + "gateway_instance_id::text, " +
		alias + "agentx_version, " +
		alias + "runtime_manifest_sha256, " +
		alias + "exec_protocol_source_sha256, " +
		alias + "environment_set_sha256, " +
		alias + "status, " +
		alias + "expires_at, " +
		alias + "acquired_at, " +
		alias + "renewed_at, " +
		alias + "version"
}
