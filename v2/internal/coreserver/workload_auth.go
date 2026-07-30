package coreserver

import (
	"errors"
	"net/http"
	"net/url"
)

type SPIFFEWorkloadAuthorizer struct {
	allowedURI string
}

func NewSPIFFEWorkloadAuthorizer(allowedURI string) (*SPIFFEWorkloadAuthorizer, error) {
	parsed, err := url.Parse(allowedURI)
	if err != nil || parsed.Scheme != "spiffe" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("allowed workload identity must be an absolute SPIFFE URI")
	}
	return &SPIFFEWorkloadAuthorizer{allowedURI: parsed.String()}, nil
}

func (authorizer *SPIFFEWorkloadAuthorizer) AuthorizeWorkload(request *http.Request, _ string) error {
	if request.TLS == nil || len(request.TLS.VerifiedChains) == 0 || len(request.TLS.VerifiedChains[0]) == 0 {
		return errors.New("verified workload client certificate is required")
	}
	leaf := request.TLS.VerifiedChains[0][0]
	for _, identity := range leaf.URIs {
		if identity.String() == authorizer.allowedURI {
			return nil
		}
	}
	return errors.New("workload SPIFFE identity is not authorized")
}
