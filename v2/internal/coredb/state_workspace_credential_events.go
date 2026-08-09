package coredb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// WorkspaceCredentialUseEvent is the durable, provider-neutral audit shape
// used by the v2 egress path.  It contains identifiers and the decision only;
// request headers, sealed bytes and materialized token values are never
// persisted.
type WorkspaceCredentialUseEvent struct {
	EventID              string
	At                   time.Time
	Stage                string
	CapabilityID         string
	WorkspaceID          string
	SessionID            string
	ActorID              string
	EnvironmentID        string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	ExecutionID          string
	OperationID          string
	SandboxID            string
	TargetGeneration     int64
	ProviderKind         string
	BindingID            string
	AuthorityVersion     int64
	CredentialVersion    int64
	TAEPSM               string
	Host                 string
	Path                 string
	Method               string
	Decision             string
	ReasonCode           string
}

// RecordWorkspaceCredentialUseEvent is deliberately idempotent. A webhook
// retry can repeat an audit write without changing the decision stream, while
// a failed write still makes the egress path fail closed in the caller.
func (s *StateStore) RecordWorkspaceCredentialUseEvent(ctx context.Context, event WorkspaceCredentialUseEvent) error {
	const operation = "RecordWorkspaceCredentialUseEvent"
	if err := validateWorkspaceCredentialUseEvent(event); err != nil {
		return commandError(ErrorInvalidArgument, operation, "credential_use", event.EventID, err.Error())
	}
	_, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (struct{}, error) {
		query := fmt.Sprintf(`
INSERT INTO %s (
 event_id, stage, capability_id, workspace_id, session_id, actor_id, environment_id,
 run_id, run_attempt_id, run_attempt_generation,
 execution_id, operation_id, sandbox_id, target_generation, provider_kind, binding_id,
 authority_version, credential_version, tae_psm, request_host, request_path, request_method,
 decision, reason_code, created_at)
VALUES ($1, $2, NULLIF($3,''), NULLIF($4,'')::uuid, NULLIF($5,'')::uuid,
        NULLIF($6,'')::uuid, NULLIF($7,'')::uuid, NULLIF($8,'')::uuid,
        NULLIF($9,'')::uuid, NULLIF($10,0), NULLIF($11,'')::uuid, NULLIF($12,'')::uuid,
        NULLIF($13,'')::uuid, NULLIF($14,0), NULLIF($15,''), NULLIF($16,'')::uuid,
        NULLIF($17,0), NULLIF($18,0), NULLIF($19,''), NULLIF($20,''), NULLIF($21,''),
        NULLIF($22,''), $23, $24, $25)
ON CONFLICT (event_id) DO NOTHING`, s.table("workspace_credential_use_events"))
		_, execErr := transaction.Exec(ctx, query,
			event.EventID, event.Stage, event.CapabilityID, event.WorkspaceID, event.SessionID,
			event.ActorID, event.EnvironmentID, event.RunID, event.RunAttemptID,
			event.RunAttemptGeneration, event.ExecutionID, event.OperationID, event.SandboxID,
			event.TargetGeneration, event.ProviderKind, event.BindingID, event.AuthorityVersion,
			event.CredentialVersion, event.TAEPSM, event.Host, event.Path, event.Method,
			event.Decision, event.ReasonCode, event.At.UTC(),
		)
		if execErr != nil {
			return struct{}{}, databaseError(operation+" insert", execErr)
		}
		return struct{}{}, nil
	})
	return err
}

func validateWorkspaceCredentialUseEvent(event WorkspaceCredentialUseEvent) error {
	if err := validateUUID("event_id", event.EventID); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"workspace_id": event.WorkspaceID, "session_id": event.SessionID, "actor_id": event.ActorID,
		"environment_id": event.EnvironmentID, "run_id": event.RunID,
		"run_attempt_id": event.RunAttemptID, "execution_id": event.ExecutionID,
		"operation_id": event.OperationID, "sandbox_id": event.SandboxID,
	} {
		if value != "" {
			if err := validateUUID(name, value); err != nil {
				return err
			}
		}
	}
	if event.Stage != "materialize" && event.Stage != "egress" {
		return errors.New("credential use audit stage is invalid")
	}
	if event.CapabilityID != "" {
		if err := validateBoundedText("capability_id", event.CapabilityID, 256); err != nil {
			return err
		}
	}
	if event.ProviderKind != "" && !corecredentialsKindPattern.MatchString(event.ProviderKind) {
		return errors.New("provider or binding identity is invalid")
	}
	if event.BindingID != "" {
		if err := validateUUID("binding_id", event.BindingID); err != nil {
			return err
		}
	}
	if event.RunAttemptGeneration < 0 || event.TargetGeneration < 0 || event.AuthorityVersion < 0 ||
		event.RunAttemptGeneration > maxSafeJSONInteger || event.TargetGeneration > maxSafeJSONInteger ||
		event.AuthorityVersion > maxSafeJSONInteger {
		return errors.New("credential use generations and authority version are invalid")
	}
	if event.CredentialVersion < 0 || event.CredentialVersion > maxSafeJSONInteger {
		return errors.New("credential version is invalid")
	}
	if event.At.IsZero() || event.At.After(time.Now().UTC().Add(5*time.Second)) {
		return errors.New("credential use timestamp is invalid")
	}
	for name, value := range map[string]string{
		"tae_psm": event.TAEPSM, "request_host": event.Host,
		"request_path": event.Path, "request_method": event.Method,
	} {
		maximum := 256
		switch name {
		case "request_host":
			maximum = 512
		case "request_path":
			maximum = 4096
		case "request_method":
			maximum = 16
		}
		if value != "" {
			if err := validateBoundedText(name, value, maximum); err != nil {
				return err
			}
		}
	}
	if event.Decision != "allow" && event.Decision != "deny" {
		return errors.New("credential use decision is invalid")
	}
	if err := validateBoundedText("reason_code", event.ReasonCode, 128); err != nil {
		return err
	}
	if event.Decision == "allow" {
		for name, value := range map[string]string{
			"capability_id": event.CapabilityID, "workspace_id": event.WorkspaceID,
			"session_id": event.SessionID, "actor_id": event.ActorID,
			"environment_id": event.EnvironmentID, "run_id": event.RunID,
			"run_attempt_id": event.RunAttemptID, "execution_id": event.ExecutionID,
			"operation_id": event.OperationID, "sandbox_id": event.SandboxID,
			"provider_kind": event.ProviderKind, "binding_id": event.BindingID,
			"tae_psm": event.TAEPSM, "request_host": event.Host,
			"request_path": event.Path, "request_method": event.Method,
		} {
			if value == "" {
				return fmt.Errorf("allowed credential use %s is required", name)
			}
		}
		if event.RunAttemptGeneration < 1 || event.TargetGeneration < 1 ||
			event.AuthorityVersion < 1 || event.CredentialVersion < 1 {
			return errors.New("allowed credential use versions are incomplete")
		}
	}
	if (event.RunAttemptID == "") != (event.RunAttemptGeneration == 0) ||
		(event.SandboxID == "") != (event.TargetGeneration == 0) ||
		(event.BindingID == "") != (event.AuthorityVersion == 0) {
		return errors.New("credential use partial scope is invalid")
	}
	return nil
}
