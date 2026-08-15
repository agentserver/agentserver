package coreserver

import (
	"errors"
	"net/http"
	"net/url"
)

type SPIFFEWorkloadAuthorizer struct {
	allowedURIs map[string]struct{}
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
