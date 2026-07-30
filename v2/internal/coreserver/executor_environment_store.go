package coreserver

import (
	"context"
	"errors"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type StateStoreExecutorEnvironmentQueries struct {
	Store *coredb.StateStore
}

func (queries StateStoreExecutorEnvironmentQueries) ListExecutorEnvironments(ctx context.Context, request corecontract.ListExecutorEnvironmentsRequest) ([]corecontract.ExecutorEnvironment, error) {
	if queries.Store == nil {
		return nil, errors.New("nil core state store")
	}
	environments, err := queries.Store.ListOnlineExecutorEnvironments(ctx, coredb.ListOnlineExecutorEnvironmentsQuery{
		WorkspaceID: request.WorkspaceID,
		ExecutorID:  request.ExecutorID,
	})
	if err != nil {
		return nil, err
	}
	result := make([]corecontract.ExecutorEnvironment, len(environments))
	for index, environment := range environments {
		result[index] = corecontract.ExecutorEnvironment{
			EnvironmentID:        environment.EnvironmentID,
			ExecutorID:           environment.ExecutorID,
			RootDescriptor:       append([]byte(nil), environment.RootDescriptor...),
			Platform:             environment.Platform,
			OuterProfileVersion:  environment.OuterProfileVersion,
			InsecureDev:          environment.InsecureDev,
			EnvironmentVersion:   environment.EnvironmentVersion,
			ConnectionGeneration: environment.ConnectionGeneration,
		}
	}
	return result, nil
}
