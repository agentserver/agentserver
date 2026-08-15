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
	route := adapter.TAENetworkRoute{
		ControlPlaneHost: config.controlPlaneHost, DataPlaneDomainSuffix: config.dataPlaneSuffix,
		ProxyURL: config.proxyURL,
	}
	controlHTTPClient, err := adapter.NewTAEControlHTTPClient(adapter.StrictHTTPClientConfig{
		TotalTimeout: config.controlTimeout, ResponseHeaderTimeout: config.headerTimeout,
	}, route)
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
		Region: config.region, Site: config.byteCloudSite, JWTEndpoint: config.jwtEndpoint, ProxyURL: config.proxyURL,
		RequestTimeout: config.jwtRequestTimeout,
	})
	if err != nil {
		closeClients()
		return nil, fmt.Errorf("configure ByteCloud application identity: %w", err)
	}
	control, err := adapter.NewSDKControlPlane(ctx, adapter.SDKControlPlaneConfig{
		Region: config.region,
		PSM:    psm, SandboxID: config.sandboxID, RevisionID: config.sandboxRevisionID,
		HTTPClient: controlHTTPClient, Headers: headerSource,
		ControlPlaneURL: config.controlPlaneURL, RequestTimeout: config.controlTimeout,
	})
	if err != nil {
		closeClients()
		return nil, err
	}
	descriptor, err := control.DescribeSandbox(ctx)
	if err != nil {
		closeClients()
		return nil, fmt.Errorf("resolve TAE terminal sandbox descriptor: %w", err)
	}
	endpoint, err := adapter.NewSandboxdEndpointResolver(config.dataPlaneSuffix)
	if err != nil {
		closeClients()
		return nil, err
	}
	dataHTTPClient, err := adapter.NewTAEDataHTTPClient(adapter.StrictHTTPClientConfig{
		ResponseHeaderTimeout: config.headerTimeout,
	}, route)
	if err != nil {
		closeClients()
		return nil, fmt.Errorf("configure TAE data-plane HTTP client: %w", err)
	}
	closeClients = func() {
		controlHTTPClient.CloseIdleConnections()
		dataHTTPClient.CloseIdleConnections()
	}
	data, err := adapter.NewHTTPDataPlane(adapter.HTTPDataPlaneConfig{
		Client: dataHTTPClient, Headers: headerSource, Endpoint: endpoint,
		SandboxID: descriptor.ID, RequireHTTPS: true,
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
