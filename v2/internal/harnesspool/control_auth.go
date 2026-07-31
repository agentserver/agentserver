package harnesspool

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const controlCapabilityBytes = 32

type ControlCapabilityGenerator func() (string, error)

func newControlCapability() (string, error) {
	raw := make([]byte, controlCapabilityBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func validateControlCapability(token string) error {
	if token == "" || strings.TrimSpace(token) != token {
		return errors.New("control capability must be a canonical base64url token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != controlCapabilityBytes || base64.RawURLEncoding.EncodeToString(raw) != token {
		return errors.New("control capability must be a canonical 256-bit base64url token")
	}
	return nil
}

func controlCapabilityDigest(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

func bearerCapability(request *http.Request) (string, error) {
	if request == nil {
		return "", errors.New("control request is required")
	}
	values := request.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", errors.New("exactly one bearer authorization value is required")
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if strings.ContainsAny(token, " \t\r\n,") {
		return "", errors.New("bearer capability has invalid whitespace or delimiters")
	}
	if err := validateControlCapability(token); err != nil {
		return "", err
	}
	return token, nil
}

// authenticateHarnessWorker requires a certificate chain already verified by
// the holder's TLS stack and exactly one URI SAN naming the configured worker
// workload. Forwarded identity headers are intentionally ignored.
func authenticateHarnessWorker(request *http.Request, expectedIdentity string) error {
	if request == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 {
		return errors.New("verified worker client certificate is required")
	}
	var leaf *x509.Certificate
	for _, chain := range request.TLS.VerifiedChains {
		if len(chain) == 0 || chain[0] == nil {
			return errors.New("verified worker certificate chain is empty")
		}
		if leaf == nil {
			leaf = chain[0]
			continue
		}
		if !leaf.Equal(chain[0]) {
			return errors.New("verified chains disagree on the worker leaf certificate")
		}
	}
	if leaf == nil || len(leaf.URIs) != 1 || leaf.URIs[0].String() != expectedIdentity {
		return errors.New("worker certificate does not contain the exact configured workload identity")
	}
	return nil
}

func validateSPIFFEIdentity(field, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "spiffe" || parsed.Host == "" || parsed.Path == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New(field + " must be a credential-free SPIFFE URI")
	}
	return nil
}
