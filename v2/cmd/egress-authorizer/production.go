package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"code.byted.org/security/zti-jwt-golang/common"
	ztitoken "code.byted.org/security/zti-jwt-golang/token"
	"code.byted.org/security/zti-jwt-golang/ztijwt/utils"
	"github.com/agentserver/agentserver/v2/internal/egressgateway"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
)

const (
	productionROWTrustDomain   = "prod-row.byted.org"
	maximumEgressTLSFileBytes  = 1024 * 1024
	productionCoreProbeTimeout = 10 * time.Second
)

type verifyProductionZTIFunc func(string, string, string) (*common.ZeroTrustIdentity, error)

type productionZTIVerifier struct {
	allowedPSM string
	verify     verifyProductionZTIFunc
	now        func() time.Time
}

func productionEgressDependencies(ctx context.Context, config egressAuthorizerConfig) (egressDependencies, error) {
	if ctx == nil || !config.production || config.policyBootstrap {
		return egressDependencies{}, errors.New("production egress context and configuration are required")
	}
	if err := initializeProductionZTI(ctx); err != nil {
		return egressDependencies{}, fmt.Errorf("initialize ROW ZTI JWKS: %w", err)
	}
	zti, err := newProductionZTIVerifier(config.allowedTAEPSM, ztitoken.VerifyZtiJwt, time.Now)
	if err != nil {
		return egressDependencies{}, err
	}
	httpClient, err := newProductionEgressCoreHTTPClient(config)
	if err != nil {
		return egressDependencies{}, err
	}
	resolver, err := egressgateway.NewCoreCredentialClient(config.coreURL, httpClient)
	if err != nil {
		httpClient.CloseIdleConnections()
		return egressDependencies{}, err
	}
	probeContext, cancelProbe := context.WithTimeout(ctx, productionCoreProbeTimeout)
	defer cancelProbe()
	if err := resolver.Probe(probeContext); err != nil {
		httpClient.CloseIdleConnections()
		return egressDependencies{}, fmt.Errorf("prewarm v2 egress credential Core mTLS connection: %w", err)
	}
	audit, err := egressgateway.NewCoreEgressAuditClient(config.coreURL, httpClient)
	if err != nil {
		httpClient.CloseIdleConnections()
		return egressDependencies{}, err
	}
	policy, err := productionProviderPolicy(config)
	if err != nil {
		httpClient.CloseIdleConnections()
		return egressDependencies{}, err
	}
	return egressDependencies{
		ZTI: zti, Resolver: resolver, ProviderPolicy: policy, Audit: audit,
		Close: httpClient.CloseIdleConnections,
	}, nil
}

func productionProviderPolicy(config egressAuthorizerConfig) (egressgateway.ProviderEgressPolicy, error) {
	// The first managed pack is Lark read-only. Provider kinds are still
	// resolved through the workspace binding API; adding a new provider requires
	// an explicit policy rule and digest rather than widening this allowlist.
	digest := config.taePolicy.PolicySHA256
	if digest == "" {
		return nil, errors.New("TAE policy digest is required for the provider policy")
	}
	// Do not represent the pack as a broad /documents prefix: the compiled pack
	// contains path templates and must reject unknown descendants.
	return egressgateway.ProviderPolicyFunc(func(providerKind, host, requestPath, method, policySHA256 string) bool {
		return providerKind == "lark" && policySHA256 == digest && larkegresspolicy.Allows(host, requestPath, method)
	}), nil
}

func initializeProductionZTI(ctx context.Context) error {
	if ctx == nil {
		return errors.New("ZTI initialization context is required")
	}
	for name, value := range map[string]string{
		common.TurnOffExtension: "true",
		"ZTI_FORCE_DOWNGRADE":   "false",
		utils.ZtiEnv:            "row",
	} {
		if err := os.Setenv(name, value); err != nil {
			return fmt.Errorf("set mandatory ZTI process policy %s: %w", name, err)
		}
	}
	environment := utils.ZtiEnvFromString("row")
	if environment == utils.ZtiEnvUnknown {
		return errors.New("ROW ZTI environment is unavailable in the linked SDK")
	}
	return ztitoken.Init(ctx, environment)
}

func newProductionZTIVerifier(
	allowedPSM string,
	verify verifyProductionZTIFunc,
	now func() time.Time,
) (*productionZTIVerifier, error) {
	if !validEgressText(allowedPSM, 256) || verify == nil || now == nil {
		return nil, errors.New("production ZTI verifier requires an exact TAE PSM, verifier, and clock")
	}
	return &productionZTIVerifier{allowedPSM: allowedPSM, verify: verify, now: now}, nil
}

func (verifier *productionZTIVerifier) VerifyZTI(ctx context.Context, raw string) (egressgateway.ZTIPrincipal, error) {
	if verifier == nil || verifier.verify == nil || verifier.now == nil || ctx == nil || ctx.Err() != nil ||
		!validEgressText(raw, 32*1024) || strings.ContainsAny(raw, " \t\r\n") {
		return egressgateway.ZTIPrincipal{}, errors.New("production ZTI request is invalid")
	}
	identity, err := verifier.verify(raw, "", "agentserver-egress-authorizer")
	if err != nil {
		return egressgateway.ZTIPrincipal{}, errors.New("production ZTI signature verification failed")
	}
	if err := ctx.Err(); err != nil {
		return egressgateway.ZTIPrincipal{}, err
	}
	if identity == nil || identity.SpiffeID.IsZero() || identity.Claims == nil || identity.Claims.Expiry == nil {
		return egressgateway.ZTIPrincipal{}, errors.New("production ZTI has no verified JWT-SVID claims")
	}
	if err := identity.ValidateZTI(); err != nil {
		return egressgateway.ZTIPrincipal{}, errors.New("production ZTI SPIFFE subject is invalid")
	}
	now := verifier.now().UTC()
	claimedExpiry := identity.Claims.Expiry.Time().UTC()
	spiffeSubject := identity.SpiffeID.String()
	if identity.SpiffeID.TrustDomain().String() != productionROWTrustDomain ||
		identity.Claims.Subject != spiffeSubject || !validEgressText(spiffeSubject, 2048) ||
		identity.Expiry.IsZero() || !identity.Expiry.UTC().Equal(claimedExpiry) || !claimedExpiry.After(now) {
		return egressgateway.ZTIPrincipal{}, errors.New("production ZTI trust domain, subject, or expiry is invalid")
	}
	if identity.Claims.NotBefore != nil && now.Before(identity.Claims.NotBefore.Time().UTC()) {
		return egressgateway.ZTIPrincipal{}, errors.New("production ZTI is not active yet")
	}
	if identity.Claims.IssuedAt != nil && identity.Claims.IssuedAt.Time().UTC().After(now.Add(30*time.Second)) {
		return egressgateway.ZTIPrincipal{}, errors.New("production ZTI issue time is in the future")
	}
	if identity.Namespace == "" || strings.EqualFold(identity.Namespace, "user") || identity.ID != verifier.allowedPSM ||
		!identity.DelegatedPrincipalSpiffeID.IsZero() || len(identity.DelegationChain) != 0 {
		return egressgateway.ZTIPrincipal{}, errors.New("production ZTI does not identify the exact non-delegated TAE PSM")
	}
	user := ""
	if identity.LegacyID != nil {
		if identity.LegacyID.PSM != verifier.allowedPSM {
			return egressgateway.ZTIPrincipal{}, errors.New("production ZTI legacy identity has a different PSM")
		}
		user = identity.LegacyID.User
		if user != "" && !validEgressText(user, 2048) {
			return egressgateway.ZTIPrincipal{}, errors.New("production ZTI legacy user is invalid")
		}
	}
	return egressgateway.ZTIPrincipal{PSM: verifier.allowedPSM, User: user}, nil
}

func newProductionEgressCoreHTTPClient(config egressAuthorizerConfig) (*http.Client, error) {
	certificate, err := loadEgressTLSIdentity(config.coreCertificate, config.coreKey, config.spiffeIdentity)
	if err != nil {
		return nil, fmt.Errorf("load egress-authorizer Core client identity: %w", err)
	}
	caPEM, err := readStableEgressFile("Core server CA", config.coreCA, maximumEgressTLSFileBytes, false)
	if err != nil {
		return nil, err
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("Core server CA file contains no certificates")
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   3 * time.Second,
		ResponseHeaderTimeout: 300 * time.Millisecond,
		ExpectContinueTimeout: 100 * time.Millisecond,
		DisableCompression:    true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: rootCAs,
			Certificates: []tls.Certificate{certificate}, ServerName: config.coreServerName,
		},
	}
	return &http.Client{Transport: transport}, nil
}

func loadEgressTLSIdentity(certificatePath, keyPath, expectedSPIFFEIdentity string) (tls.Certificate, error) {
	if !validEgressSPIFFEIdentity(expectedSPIFFEIdentity) {
		return tls.Certificate{}, errors.New("expected egress-authorizer SPIFFE identity is invalid")
	}
	certificatePEM, err := readStableEgressFile("TLS certificate", certificatePath, maximumEgressTLSFileBytes, false)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := readStableEgressFile("TLS private key", keyPath, maximumEgressTLSFileBytes, true)
	if err != nil {
		return tls.Certificate{}, err
	}
	defer clear(keyPEM)
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse TLS key pair: %w", err)
	}
	if len(certificate.Certificate) == 0 {
		return tls.Certificate{}, errors.New("TLS certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse TLS leaf certificate: %w", err)
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != expectedSPIFFEIdentity {
		return tls.Certificate{}, errors.New("TLS leaf certificate does not contain the exact egress-authorizer SPIFFE identity")
	}
	certificate.Leaf = leaf
	return certificate, nil
}

func readStableEgressFile(label, path string, maximum int64, secret bool) ([]byte, error) {
	if !cleanAbsolutePath(path) {
		return nil, fmt.Errorf("%s path must be absolute and clean", label)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > maximum ||
		(secret && before.Mode().Perm()&0o037 != 0) {
		return nil, fmt.Errorf("%s must be a bounded regular file with safe permissions", label)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != before.Size() {
		clear(raw)
		return nil, fmt.Errorf("read stable %s", label)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
		clear(raw)
		return nil, fmt.Errorf("%s changed while it was being read", label)
	}
	return raw, nil
}

var _ egressgateway.ZTIVerifier = (*productionZTIVerifier)(nil)
