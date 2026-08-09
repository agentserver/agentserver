package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

func newSandboxGatewayServerTLSConfig(config sandboxGatewayConfig) (*tls.Config, error) {
	certificate, err := loadSandboxGatewayCertificate(config.tlsCertificate, config.tlsKey, config.spiffeIdentity)
	if err != nil {
		return nil, fmt.Errorf("load sandbox-gateway server identity: %w", err)
	}
	clientCAs, err := loadSandboxGatewayCertPool("sandbox-gateway client CA", config.clientCA)
	if err != nil {
		return nil, err
	}
	allowed := map[string]struct{}{config.executorIdentity: {}, config.harnessIdentity: {}}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		NextProtos:   []string{"h2", "http/1.1"},
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return errors.New("sandbox-gateway client has no verified certificate chain")
			}
			leaf := state.PeerCertificates[0]
			if len(leaf.URIs) != 1 {
				return errors.New("sandbox-gateway client certificate must contain exactly one URI identity")
			}
			if _, ok := allowed[leaf.URIs[0].String()]; !ok {
				return errors.New("sandbox-gateway client SPIFFE identity is not allowed")
			}
			return nil
		},
	}, nil
}

func loadSandboxGatewayCertificate(certificateFile, keyFile, expectedIdentity string) (tls.Certificate, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return tls.Certificate{}, err
	}
	if len(certificate.Certificate) == 0 {
		return tls.Certificate{}, errors.New("TLS certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse TLS leaf certificate: %w", err)
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != expectedIdentity {
		return tls.Certificate{}, errors.New("TLS leaf certificate does not contain the exact configured sandbox-gateway SPIFFE identity")
	}
	certificate.Leaf = leaf
	return certificate, nil
}

func loadSandboxGatewayCertPool(label, path string) (*x509.CertPool, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(contents) {
		return nil, fmt.Errorf("%s contains no certificates", label)
	}
	return pool, nil
}
