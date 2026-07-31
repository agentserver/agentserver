package devstack

import (
	"crypto/ed25519"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"time"
)

const developmentTrustDomain = "agentserver.dev"

type developmentPKI struct {
	caPEM      []byte
	identities map[string]developmentTLSIdentity
}

type developmentTLSIdentity struct {
	spiffeID       string
	certificatePEM []byte
	privateKeyPEM  []byte
}

var developmentServices = []string{
	"agentserver-core",
	"browser-gateway",
	"executor-gateway",
	"harness-pool",
	"harness-worker",
	"llmproxy",
}

func generateDevelopmentPKI(random io.Reader, now time.Time) (developmentPKI, error) {
	if random == nil {
		return developmentPKI{}, errors.New("development PKI random source is required")
	}
	if now.IsZero() {
		return developmentPKI{}, errors.New("development PKI clock is required")
	}
	caPublic, caPrivate, err := ed25519.GenerateKey(random)
	if err != nil {
		return developmentPKI{}, fmt.Errorf("generate development CA key: %w", err)
	}
	caSerial, err := randomSerial(random)
	if err != nil {
		return developmentPKI{}, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "agentserver v2 insecure development CA"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(30 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(random, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		return developmentPKI{}, fmt.Errorf("create development CA certificate: %w", err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		return developmentPKI{}, fmt.Errorf("parse generated development CA certificate: %w", err)
	}
	result := developmentPKI{
		caPEM:      pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		identities: make(map[string]developmentTLSIdentity, len(developmentServices)),
	}
	for _, service := range developmentServices {
		identity, err := generateDevelopmentIdentity(random, now, service, caCertificate, caPrivate)
		if err != nil {
			return developmentPKI{}, err
		}
		result.identities[service] = identity
	}
	return result, nil
}

func generateDevelopmentIdentity(
	random io.Reader,
	now time.Time,
	service string,
	ca *x509.Certificate,
	caPrivate ed25519.PrivateKey,
) (developmentTLSIdentity, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return developmentTLSIdentity{}, fmt.Errorf("generate %s development TLS key: %w", service, err)
	}
	serial, err := randomSerial(random)
	if err != nil {
		return developmentTLSIdentity{}, err
	}
	spiffeID := "spiffe://" + developmentTrustDomain + "/ns/insecure-dev/sa/" + service
	uri, err := url.Parse(spiffeID)
	if err != nil {
		return developmentTLSIdentity{}, fmt.Errorf("parse generated %s SPIFFE identity: %w", service, err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: service + ".insecure-dev.agentserver"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(7 * 24 * time.Hour),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		URIs:                  []*url.URL{uri},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(random, template, ca, publicKey, caPrivate)
	if err != nil {
		return developmentTLSIdentity{}, fmt.Errorf("create %s development TLS certificate: %w", service, err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return developmentTLSIdentity{}, fmt.Errorf("marshal %s development TLS private key: %w", service, err)
	}
	return developmentTLSIdentity{
		spiffeID:       spiffeID,
		certificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}),
		privateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
	}, nil
}

func randomSerial(random io.Reader) (*big.Int, error) {
	var raw [16]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return nil, fmt.Errorf("generate development certificate serial: %w", err)
	}
	raw[0] &= 0x7f
	nonzero := false
	for _, value := range raw {
		if value != 0 {
			nonzero = true
			break
		}
	}
	if !nonzero {
		raw[len(raw)-1] = 1
	}
	return new(big.Int).SetBytes(raw[:]), nil
}
