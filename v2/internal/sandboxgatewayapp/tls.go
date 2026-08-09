package sandboxgatewayapp

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

func ServerTLSConfig(config Config) (*tls.Config, error) {
	certificate, err := loadCertificate(config.TLSCertificate, config.TLSKey, config.SPIFFEIdentity)
	if err != nil {
		return nil, fmt.Errorf("load sandbox-gateway server identity: %w", err)
	}
	clientCAs, err := loadCertPool("sandbox-gateway client CA", config.ClientCA)
	if err != nil {
		return nil, err
	}
	allowed := map[string]struct{}{config.ExecutorIdentity: {}, config.HarnessIdentity: {}}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs, NextProtos: []string{"h2", "http/1.1"},
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

func CoreHTTPClient(config Config) (*http.Client, error) {
	certificate, err := loadCertificate(config.CoreCertificate, config.CoreKey, config.SPIFFEIdentity)
	if err != nil {
		return nil, fmt.Errorf("load sandbox-gateway Core client identity: %w", err)
	}
	rootCAs, err := loadCertPool("Core server CA", config.CoreCA)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 16, MaxIdleConnsPerHost: 16, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 35 * time.Second,
		ExpectContinueTimeout: time.Second, DisableCompression: true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: rootCAs, Certificates: []tls.Certificate{certificate}, ServerName: config.CoreServerName,
		},
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

func loadCertificate(certificateFile, keyFile, expectedIdentity string) (tls.Certificate, error) {
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

func loadCertPool(label, path string) (*x509.CertPool, error) {
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
