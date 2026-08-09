package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/egressgateway"
)

type developmentZTIVerifier struct {
	token     string
	principal egressgateway.ZTIPrincipal
}

func (verifier *developmentZTIVerifier) VerifyZTI(ctx context.Context, token string) (egressgateway.ZTIPrincipal, error) {
	if verifier == nil || ctx == nil || ctx.Err() != nil || len(token) != len(verifier.token) ||
		subtle.ConstantTimeCompare([]byte(token), []byte(verifier.token)) != 1 {
		return egressgateway.ZTIPrincipal{}, errors.New("development ZTI verification failed")
	}
	return verifier.principal, nil
}

type developmentLiveAuthority struct {
	credential egressgateway.Credential
}

func (authority *developmentLiveAuthority) AuthorizeLarkReadOnly(
	ctx context.Context,
	_ egresscapability.Claims,
	_ egressgateway.ZTIPrincipal,
) (egressgateway.Credential, error) {
	if authority == nil || ctx == nil || ctx.Err() != nil {
		return egressgateway.Credential{}, errors.New("development live authority is unavailable")
	}
	return authority.credential, nil
}

type developmentAuditSink struct {
	mu     sync.Mutex
	writer io.Writer
}

func (sink *developmentAuditSink) RecordEgressDecision(ctx context.Context, record egressgateway.AuditRecord) error {
	if sink == nil || sink.writer == nil || ctx == nil {
		return errors.New("development egress audit sink is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		Component string                    `json:"component"`
		Record    egressgateway.AuditRecord `json:"record"`
	}{Component: "egress-authorizer", Record: record})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	sink.mu.Lock()
	defer sink.mu.Unlock()
	written, err := sink.writer.Write(payload)
	if err != nil {
		return err
	}
	if written != len(payload) {
		return io.ErrShortWrite
	}
	return nil
}

func defaultEgressDependencies(
	ctx context.Context,
	config egressAuthorizerConfig,
	auditWriter io.Writer,
	now time.Time,
) (egressDependencies, error) {
	if config.production {
		return productionEgressDependencies(ctx, config)
	}
	if ctx == nil {
		return egressDependencies{}, errors.New("development egress dependency context is required")
	}
	return egressDependencies{
		ZTI: &developmentZTIVerifier{
			token:     config.devZTIToken,
			principal: egressgateway.ZTIPrincipal{PSM: config.allowedTAEPSM, User: "insecure-development"},
		},
		Authority: &developmentLiveAuthority{credential: egressgateway.Credential{
			AccessToken: config.devLarkAccessToken, ExpiresAt: now.Add(config.devCredentialLifetime),
		}},
		Audit: &developmentAuditSink{writer: auditWriter},
	}, nil
}

var _ egressgateway.ZTIVerifier = (*developmentZTIVerifier)(nil)
var _ egressgateway.LiveAuthority = (*developmentLiveAuthority)(nil)
var _ egressgateway.AuditSink = (*developmentAuditSink)(nil)
