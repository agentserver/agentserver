package coreserver

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type StateStoreExecutorConnectionCommands struct {
	Store *coredb.StateStore
}

func (commands StateStoreExecutorConnectionCommands) AcquireExecutorConnection(ctx context.Context, request corecontract.AcquireExecutorConnectionRequest) (corecontract.ConnectionHolder, error) {
	if commands.Store == nil {
		return corecontract.ConnectionHolder{}, errors.New("nil core state store")
	}
	runtimeDigest, err := contractDigest(request.RuntimeManifestSHA256)
	if err != nil {
		return corecontract.ConnectionHolder{}, commandConversionError("AcquireExecutorConnection", request.ExecutorID, err)
	}
	protocolDigest, err := contractDigest(request.ExecProtocolSourceSHA256)
	if err != nil {
		return corecontract.ConnectionHolder{}, commandConversionError("AcquireExecutorConnection", request.ExecutorID, err)
	}
	environments, err := contractEnvironments(request.Environments)
	if err != nil {
		return corecontract.ConnectionHolder{}, commandConversionError("AcquireExecutorConnection", request.ExecutorID, err)
	}
	result, err := commands.Store.AcquireExecutorConnection(ctx, coredb.AcquireExecutorConnectionCommand{
		ExecutorID:               request.ExecutorID,
		ConnectionID:             request.ConnectionID,
		SessionID:                request.SessionID,
		GatewayInstanceID:        request.GatewayInstanceID,
		AgentxVersion:            request.AgentxVersion,
		RuntimeManifestSHA256:    runtimeDigest,
		ExecProtocolSourceSHA256: protocolDigest,
		Environments:             environments,
		LeaseTTL:                 time.Duration(request.ConnectionLeaseTTLMillis) * time.Millisecond,
	})
	if err != nil {
		return corecontract.ConnectionHolder{}, err
	}
	return contractHolder(result.Connection), nil
}

func (commands StateStoreExecutorConnectionCommands) RenewExecutorConnection(ctx context.Context, request corecontract.RenewExecutorConnectionRequest) (corecontract.ConnectionHolder, error) {
	if commands.Store == nil {
		return corecontract.ConnectionHolder{}, errors.New("nil core state store")
	}
	connection, err := commands.Store.RenewExecutorConnection(ctx, coredb.RenewExecutorConnectionCommand{
		ExecutorID:        request.Holder.ExecutorID,
		SessionID:         request.Holder.SessionID,
		GatewayInstanceID: request.Holder.GatewayInstanceID,
		Generation:        request.Holder.Generation,
		LeaseTTL:          time.Duration(request.ConnectionLeaseTTLMillis) * time.Millisecond,
	})
	if err != nil {
		return corecontract.ConnectionHolder{}, err
	}
	return contractHolder(connection), nil
}

func (commands StateStoreExecutorConnectionCommands) ActivateExecutorConnection(ctx context.Context, request corecontract.ActivateExecutorConnectionRequest) (corecontract.ConnectionHolder, error) {
	if commands.Store == nil {
		return corecontract.ConnectionHolder{}, errors.New("nil core state store")
	}
	environments, err := contractEnvironments(request.Environments)
	if err != nil {
		return corecontract.ConnectionHolder{}, commandConversionError("ActivateExecutorConnection", request.Holder.ExecutorID, err)
	}
	result, err := commands.Store.ActivateExecutorConnection(ctx, coredb.ActivateExecutorConnectionCommand{
		ExecutorID:        request.Holder.ExecutorID,
		SessionID:         request.Holder.SessionID,
		GatewayInstanceID: request.Holder.GatewayInstanceID,
		Generation:        request.Holder.Generation,
		Environments:      environments,
	})
	if err != nil {
		return corecontract.ConnectionHolder{}, err
	}
	return contractHolder(result.Connection), nil
}

func (commands StateStoreExecutorConnectionCommands) FenceExecutorConnection(ctx context.Context, request corecontract.FenceExecutorConnectionRequest) error {
	if commands.Store == nil {
		return errors.New("nil core state store")
	}
	_, err := commands.Store.FenceExecutorConnection(ctx, coredb.FenceExecutorConnectionCommand{
		ExecutorID:        request.Holder.ExecutorID,
		SessionID:         request.Holder.SessionID,
		GatewayInstanceID: request.Holder.GatewayInstanceID,
		Generation:        request.Holder.Generation,
	})
	return err
}

func contractDigest(value string) ([32]byte, error) {
	var result [32]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(result) {
		return result, errors.New("digest must be lowercase 64-character SHA-256 hex")
	}
	if hex.EncodeToString(decoded) != value {
		return result, errors.New("digest must use lowercase hexadecimal")
	}
	copy(result[:], decoded)
	return result, nil
}

func contractEnvironments(source []corecontract.EnvironmentDeclaration) ([]coredb.ExecutorEnvironmentDeclaration, error) {
	converted := make([]coredb.ExecutorEnvironmentDeclaration, len(source))
	for index, environment := range source {
		digest, err := contractDigest(environment.CodexSHA256)
		if err != nil {
			return nil, err
		}
		converted[index] = coredb.ExecutorEnvironmentDeclaration{
			ID:                  environment.ID,
			Platform:            environment.Platform,
			CodexRelease:        environment.CodexRelease,
			CodexCommit:         environment.CodexCommit,
			CodexSHA256:         digest,
			OuterProfileVersion: environment.OuterProfileVersion,
			ProcessMethods:      append([]string(nil), environment.ProcessMethods...),
			InsecureDev:         environment.InsecureDev,
		}
	}
	return converted, nil
}

func contractHolder(connection coredb.ExecutorConnection) corecontract.ConnectionHolder {
	return corecontract.ConnectionHolder{
		ExecutorID:        connection.ExecutorID,
		ConnectionID:      connection.ConnectionID,
		SessionID:         connection.SessionID,
		GatewayInstanceID: connection.GatewayInstanceID,
		Generation:        connection.Generation,
		Status:            connection.Status,
		ExpiresAt:         connection.ExpiresAt,
	}
}
