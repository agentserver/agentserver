package main

import (
	"context"
	"fmt"

	"github.com/agentserver/agentserver/v2/providers/tae/adapter"
)

type taeClients struct {
	refresh func(context.Context) error
	control adapter.ControlPlane
	data    adapter.DataPlane
	close   func()
}

func (clients *taeClients) Close() {
	if clients != nil && clients.close != nil {
		clients.close()
	}
}

func newTAEClients(ctx context.Context, config providerConfig, psm string) (*taeClients, error) {
	controlHTTPClient, err := adapter.NewSGTAEControlHTTPClient(adapter.StrictHTTPClientConfig{
		TotalTimeout: config.controlTimeout, ResponseHeaderTimeout: config.headerTimeout,
	}, config.proxyURL)
	if err != nil {
		return nil, fmt.Errorf("configure TAE control-plane HTTP client: %w", err)
	}
	closeClients := func() { controlHTTPClient.CloseIdleConnections() }
	credentials, err := loadByteCloudCredentials(config.accessKeyFile, config.secretKeyFile)
	if err != nil {
		closeClients()
		return nil, fmt.Errorf("load ByteCloud application credentials: %w", err)
	}
	headerSource, err := adapter.NewByteCloudJWTHeaderSource(adapter.ByteCloudJWTHeaderSourceConfig{
		AccessKeyID: credentials.accessKeyID, SecretAccessKey: credentials.secretAccessKey,
		Site: config.byteCloudSite, JWTEndpoint: config.jwtEndpoint, ProxyURL: config.proxyURL,
		RequestTimeout: config.jwtRequestTimeout,
	})
	if err != nil {
		closeClients()
		return nil, fmt.Errorf("configure ByteCloud application identity: %w", err)
	}
	control, err := adapter.NewSGSDKControlPlane(ctx, adapter.SDKControlPlaneConfig{
		PSM: psm, HTTPClient: controlHTTPClient, Headers: headerSource,
		RequestTimeout: config.controlTimeout,
	})
	if err != nil {
		closeClients()
		return nil, err
	}
	domainSuffix, err := adapter.SGDataplaneDomainSuffix()
	if err != nil {
		closeClients()
		return nil, fmt.Errorf("resolve pinned SG TAE data-plane domain: %w", err)
	}
	endpoint, err := adapter.NewSandboxdEndpointResolver(domainSuffix)
	if err != nil {
		closeClients()
		return nil, err
	}
	dataHTTPClient, err := adapter.NewSGTAEDataHTTPClient(adapter.StrictHTTPClientConfig{
		ResponseHeaderTimeout: config.headerTimeout,
	}, config.proxyURL)
	if err != nil {
		closeClients()
		return nil, fmt.Errorf("configure TAE data-plane HTTP client: %w", err)
	}
	closeClients = func() {
		controlHTTPClient.CloseIdleConnections()
		dataHTTPClient.CloseIdleConnections()
	}
	data, err := adapter.NewHTTPDataPlane(adapter.HTTPDataPlaneConfig{
		Client: dataHTTPClient, Headers: headerSource, Endpoint: endpoint, RequireHTTPS: true,
	})
	if err != nil {
		closeClients()
		return nil, err
	}
	return &taeClients{
		refresh: func(refreshContext context.Context) error {
			_, err := headerSource.ForceRefresh(refreshContext)
			return err
		},
		control: control, data: data, close: closeClients,
	}, nil
}
