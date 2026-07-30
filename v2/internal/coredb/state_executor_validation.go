package coredb

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sort"
)

var executorProcessMethods = []string{
	"process/start",
	"process/read",
	"process/write",
	"process/terminate",
}

func validateAcquireExecutorConnection(command AcquireExecutorConnectionCommand) (int64, error) {
	for _, identifier := range []struct {
		field string
		value string
	}{
		{"executor_id", command.ExecutorID},
		{"connection_id", command.ConnectionID},
		{"session_id", command.SessionID},
		{"gateway_instance_id", command.GatewayInstanceID},
	} {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return 0, err
		}
	}
	if err := validateBoundedText("agentx_version", command.AgentxVersion, 256); err != nil {
		return 0, err
	}
	if isZeroDigest(command.RuntimeManifestSHA256) {
		return 0, errors.New("runtime_manifest_sha256 must not be all zeroes")
	}
	if isZeroDigest(command.ExecProtocolSourceSHA256) {
		return 0, errors.New("exec_protocol_source_sha256 must not be all zeroes")
	}
	if err := validateExecutorEnvironmentDeclarations(command.Environments); err != nil {
		return 0, err
	}
	return durationMilliseconds("lease_ttl", command.LeaseTTL, MaxExecutorConnectionTTL)
}

func validateActivateExecutorConnection(command ActivateExecutorConnectionCommand) error {
	if err := validateExecutorConnectionIdentity(command.ExecutorID, command.SessionID, command.GatewayInstanceID, command.Generation); err != nil {
		return err
	}
	return validateExecutorEnvironmentDeclarations(command.Environments)
}

func validateExecutorEnvironmentDeclarations(environments []ExecutorEnvironmentDeclaration) error {
	if len(environments) == 0 || len(environments) > 256 {
		return errors.New("environments must contain between 1 and 256 entries")
	}
	seen := make(map[string]struct{}, len(environments))
	for index, environment := range environments {
		if err := validateExecutorEnvironmentDeclaration(environment); err != nil {
			return fmt.Errorf("environment %d: %w", index, err)
		}
		if _, duplicate := seen[environment.ID]; duplicate {
			return fmt.Errorf("environment %d: duplicate env_id %q", index, environment.ID)
		}
		seen[environment.ID] = struct{}{}
	}
	return nil
}

func validateExecutorEnvironmentDeclaration(environment ExecutorEnvironmentDeclaration) error {
	if err := validateUUID("env_id", environment.ID); err != nil {
		return err
	}
	switch environment.Platform {
	case "linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64":
	default:
		return fmt.Errorf("platform %q is unsupported", environment.Platform)
	}
	if err := validateBoundedText("codex_release", environment.CodexRelease, 128); err != nil {
		return err
	}
	if len(environment.CodexCommit) != 40 {
		return errors.New("codex_commit must be a lowercase 40-character Git SHA")
	}
	for _, character := range environment.CodexCommit {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return errors.New("codex_commit must be a lowercase 40-character Git SHA")
		}
	}
	if isZeroDigest(environment.CodexSHA256) {
		return errors.New("codex_sha256 must not be all zeroes")
	}
	if environment.OuterProfileVersion != "process-v1" {
		return fmt.Errorf("outer_profile_version = %q, want process-v1", environment.OuterProfileVersion)
	}
	if !slices.Equal(environment.ProcessMethods, executorProcessMethods) {
		return fmt.Errorf("process_methods = %q, want exact %q", environment.ProcessMethods, executorProcessMethods)
	}
	return nil
}

func validateRenewExecutorConnection(command RenewExecutorConnectionCommand) (int64, error) {
	if err := validateExecutorConnectionIdentity(command.ExecutorID, command.SessionID, command.GatewayInstanceID, command.Generation); err != nil {
		return 0, err
	}
	return durationMilliseconds("lease_ttl", command.LeaseTTL, MaxExecutorConnectionTTL)
}

func validateFenceExecutorConnection(command FenceExecutorConnectionCommand) error {
	return validateExecutorConnectionIdentity(command.ExecutorID, command.SessionID, command.GatewayInstanceID, command.Generation)
}

func validateExecutorConnectionIdentity(executorID, sessionID, gatewayInstanceID string, generation int64) error {
	for _, identifier := range []struct {
		field string
		value string
	}{
		{"executor_id", executorID},
		{"session_id", sessionID},
		{"gateway_instance_id", gatewayInstanceID},
	} {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if generation < 1 {
		return errors.New("generation must be positive")
	}
	return nil
}

func isZeroDigest(value [32]byte) bool {
	return value == [32]byte{}
}

func hashExecutorEnvironmentDeclarations(environments []ExecutorEnvironmentDeclaration) [32]byte {
	ordered := append([]ExecutorEnvironmentDeclaration(nil), environments...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	hash := sha256.New()
	_, _ = hash.Write([]byte("agentserver-v2/executor-hello-environments/v1\x00"))
	writeEnvironmentHashUint32(hash, uint32(len(ordered)))
	for _, environment := range ordered {
		writeEnvironmentHashText(hash, environment.ID)
		writeEnvironmentHashText(hash, environment.Platform)
		writeEnvironmentHashText(hash, environment.CodexRelease)
		writeEnvironmentHashText(hash, environment.CodexCommit)
		_, _ = hash.Write(environment.CodexSHA256[:])
		writeEnvironmentHashText(hash, environment.OuterProfileVersion)
		writeEnvironmentHashUint32(hash, uint32(len(environment.ProcessMethods)))
		for _, method := range environment.ProcessMethods {
			writeEnvironmentHashText(hash, method)
		}
		if environment.InsecureDev {
			_, _ = hash.Write([]byte{1})
		} else {
			_, _ = hash.Write([]byte{0})
		}
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

type environmentHashWriter interface {
	Write([]byte) (int, error)
}

func writeEnvironmentHashText(writer environmentHashWriter, value string) {
	writeEnvironmentHashUint32(writer, uint32(len(value)))
	_, _ = writer.Write([]byte(value))
}

func writeEnvironmentHashUint32(writer environmentHashWriter, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	_, _ = writer.Write(encoded[:])
}
