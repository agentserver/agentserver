package coreserver

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const externalManagedSandboxTokenHeader = "X-AgentServer-Managed-Sandbox-Token"

type SPIFFEWorkloadAuthorizer struct {
	allowedURIs   map[string]struct{}
	externalToken []byte
}

func NewSPIFFEWorkloadAuthorizerWithExternalToken(token string, allowedURIs ...string) (*SPIFFEWorkloadAuthorizer, error) {
	if token == "" || len(token) > 8192 || strings.ContainsAny(token, "\x00\r\n") {
		return nil, errors.New("external managed sandbox token is invalid")
	}
	a, err := NewSPIFFEWorkloadAuthorizer(allowedURIs...)
	if err != nil {
		return nil, err
	}
	a.externalToken = []byte(token)
	return a, nil
}

func NewSPIFFEWorkloadAuthorizer(allowedURIs ...string) (*SPIFFEWorkloadAuthorizer, error) {
	if len(allowedURIs) < 1 || len(allowedURIs) > 32 {
		return nil, errors.New("workload authorizer requires between one and 32 SPIFFE identities")
	}
	allowed := make(map[string]struct{}, len(allowedURIs))
	for _, allowedURI := range allowedURIs {
		parsed, err := url.Parse(allowedURI)
		if err != nil || parsed.Scheme != "spiffe" || parsed.Host == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" || parsed.String() != allowedURI {
			return nil, errors.New("allowed workload identity must be an absolute canonical SPIFFE URI")
		}
		if _, duplicate := allowed[allowedURI]; duplicate {
			return nil, errors.New("allowed workload SPIFFE identity is repeated")
		}
		allowed[allowedURI] = struct{}{}
	}
	return &SPIFFEWorkloadAuthorizer{allowedURIs: allowed}, nil
}

func (authorizer *SPIFFEWorkloadAuthorizer) AuthorizeWorkload(request *http.Request, _ string) error {
	if request == nil || request.TLS == nil {
		return errors.New("verified TLS workload connection is required")
	}
	if request.URL != nil && len(authorizer.externalToken) > 0 && len(request.TLS.PeerCertificates) == 0 {
		values := request.Header.Values(externalManagedSandboxTokenHeader)
		path := request.URL.Path
		managedSandboxPath := path == corecontract.ReserveManagedSandboxPath ||
			path == corecontract.ListManagedSandboxesForReconcilePath ||
			path == corecontract.AuthorizeManagedSandboxOperationPath ||
			strings.HasPrefix(path, corecontract.ManagedSandboxPathPrefix)
		if len(values) == 1 && request.URL.RawPath == "" && managedSandboxPath && subtle.ConstantTimeCompare([]byte(values[0]), authorizer.externalToken) == 1 {
			return nil
		}
	}
	if request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.VerifiedChains[0]) == 0 {
		return errors.New("verified workload client certificate is required")
	}
	leaf := request.TLS.VerifiedChains[0][0]
	if len(leaf.URIs) != 1 {
		return errors.New("verified workload certificate must contain exactly one SPIFFE identity")
	}
	if _, allowed := authorizer.allowedURIs[leaf.URIs[0].String()]; !allowed {
		return errors.New("verified workload certificate must contain exactly the authorized SPIFFE identity")
	}
	return nil
}
