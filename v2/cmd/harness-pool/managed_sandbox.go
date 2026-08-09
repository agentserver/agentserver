package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesspool"
	"github.com/agentserver/agentserver/v2/internal/sandboxcapability"
	"github.com/agentserver/agentserver/v2/internal/sandboxclient"
)

func configureHarnessPoolManagedSandbox(
	config harnessPoolConfig,
	mode harnessPoolServeMode,
) (harnesspool.ManagedSandboxLifecycle, *http.Client, error) {
	if config.managedSandbox == nil {
		return nil, nil, nil
	}
	if mode != harnessPoolServeProduction && mode != harnessPoolServeInsecureDevelopment {
		return nil, nil, errors.New("managed sandbox serve mode is invalid")
	}
	signer, err := sandboxcapability.LoadSigner(
		config.sandboxCapabilityIssuer,
		sandboxcapability.AudienceLifecycle,
		config.sandboxCapabilityKeyID,
		config.sandboxCapabilityKey,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("configure sandbox lifecycle capability signer: %w", err)
	}
	tokens, err := sandboxclient.NewSignedTokenSource(signer, time.Now, 30*time.Second)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := url.Parse(config.sandboxGatewayURL)
	if err != nil {
		return nil, nil, fmt.Errorf("parse sandbox-gateway URL: %w", err)
	}
	var httpClient *http.Client
	if parsed.Scheme == "https" {
		httpClient, err = newHarnessPoolCoreHTTPClient(
			config.sandboxGatewayCA,
			config.tlsCertificate,
			config.tlsKey,
			config.sandboxGatewayServer,
			config.poolTLSIdentity,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("configure sandbox-gateway mTLS client: %w", err)
		}
	} else {
		transport := &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout: 5 * time.Second, KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          16,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: 65 * time.Second,
			ExpectContinueTimeout: time.Second,
			DisableCompression:    true,
		}
		httpClient = &http.Client{Transport: transport}
	}
	baseURL := config.sandboxGatewayURL
	if parsed.Path == "/" {
		baseURL = strings.TrimSuffix(baseURL, "/")
	}
	client, err := sandboxclient.New(baseURL, httpClient, tokens)
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, nil, err
	}
	lifecycle, err := harnesspool.NewDefaultGatewayManagedSandboxLifecycle(client)
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, nil, err
	}
	return lifecycle, httpClient, nil
}
