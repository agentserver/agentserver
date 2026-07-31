package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/harnesspool"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

const harnessPoolShutdownTimeout = 30 * time.Second

const maximumHarnessPoolFailureLogBytes = 4 * 1024

type harnessPoolReadiness struct {
	ready atomic.Bool
}

type harnessPoolFailureReporter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (reporter *harnessPoolFailureReporter) ReportPoolFailure(failure harnesspool.PoolFailure) {
	if reporter == nil || reporter.writer == nil || failure.Err == nil {
		return
	}
	message := strings.NewReplacer("\r", " ", "\n", " ").Replace(failure.Err.Error())
	message = strings.ToValidUTF8(message, "�")
	if len(message) > maximumHarnessPoolFailureLogBytes {
		limit := maximumHarnessPoolFailureLogBytes
		for limit > 0 && !utf8.ValidString(message[:limit]) {
			limit--
		}
		message = message[:limit] + "...(truncated)"
	}
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	_, _ = fmt.Fprintf(
		reporter.writer,
		"harness-pool attempt failure: stage=%s run=%s attempt=%s error=%s\n",
		failure.Stage, failure.RunID, failure.RunAttemptID, message,
	)
}

func serveHarnessPool(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) error {
	if ctx == nil {
		return errors.New("harness-pool serve context is required")
	}
	config, err := loadHarnessPoolDevelopmentConfig(getenv)
	if err != nil {
		return err
	}
	poolInstanceID, err := harnesspool.NewPoolInstanceID()
	if err != nil {
		return fmt.Errorf("allocate harness-pool instance identity: %w", err)
	}
	identities, err := harnesspool.NewDefaultControlIdentityAllocator(poolInstanceID)
	if err != nil {
		return err
	}
	signer, err := harnesspool.LoadEd25519ManifestSigner(config.manifestKeyID, config.manifestKeyFile)
	if err != nil {
		return fmt.Errorf("configure run manifest signer: %w", err)
	}
	coreHTTPClient, err := newHarnessPoolCoreHTTPClient(
		config.coreCA, config.tlsCertificate, config.tlsKey,
		config.coreServerName, config.poolTLSIdentity,
	)
	if err != nil {
		return err
	}
	defer coreHTTPClient.CloseIdleConnections()
	coreClient, err := harnesspool.NewCoreClient(config.coreURL, coreHTTPClient)
	if err != nil {
		return err
	}
	objects, err := harnesspool.NewLocalDevelopmentObjectStore(config.objectRoot)
	if err != nil {
		return fmt.Errorf("configure insecure-development object store: %w", err)
	}
	controlTLS, err := newHarnessPoolControlTLSConfig(
		config.tlsCertificate, config.tlsKey, config.workerClientCA, config.poolTLSIdentity,
	)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.listenAddress)
	if err != nil {
		return fmt.Errorf("listen on harness-pool control address: %w", err)
	}
	defer listener.Close()
	callbackEndpoint, err := harnessPoolCallbackEndpoint(config.listenAddress, listener.Addr())
	if err != nil {
		return err
	}

	controlConfig := harnesspool.DefaultControlServerConfig(
		poolInstanceID, poolInstanceID, callbackEndpoint, config.poolTLSIdentity,
		developmentControlAudience, config.serviceAccount, config.workerTLSIdentity,
	)
	controls, err := harnesspool.NewControlServer(controlConfig)
	if err != nil {
		return err
	}
	profile := developmentRunLaunchProfile(config, callbackEndpoint)
	resolver, err := harnesspool.NewConfiguredRunLaunchInputResolver(coreClient, profile)
	if err != nil {
		return err
	}
	preparer, err := harnesspool.NewLaunchPreparer(coreClient, identities, resolver, signer)
	if err != nil {
		return err
	}
	capabilityConfig := harnesspool.DefaultDevelopmentAttemptRuntimeCapabilitySourceConfig(config.executorID)
	capabilities, err := harnesspool.NewDevelopmentAttemptRuntimeCapabilitySource(config.capabilityCodec, capabilityConfig)
	if err != nil {
		return err
	}
	launcher, err := harnesspool.NewLocalProcessLauncher(harnesspool.LocalProcessLauncherConfig{
		WorkerExecutable: config.workerExecutable,
		WorkerArguments:  []string{developmentWorkerArguments, "--config=" + config.workerConfig},
		RuntimeRoot:      config.runtimeRoot,
		Environment: []string{
			"LANG=C", "LC_ALL=C", "NO_COLOR=1", "PATH=/usr/bin:/bin", "TMPDIR=/tmp",
		},
		ObjectSource:               objects,
		ExpectedAppCredential:      &config.appCredential,
		ExpectedWorkerImageDigest:  config.workerDigest,
		ExpectedServiceAccount:     config.serviceAccount,
		InputWriteTimeout:          30 * time.Second,
		TerminateGrace:             10 * time.Second,
		ProcessGroupCleanupTimeout: 30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("configure local attempt launcher: %w", err)
	}
	finalizer, err := harnesspool.NewCheckpointFinalizer(
		coreClient, identities, objects,
		harnesspool.CheckpointFinalizerConfig{StagingRoot: config.checkpointRoot},
	)
	if err != nil {
		return err
	}
	supervisor, err := harnesspool.NewControlAttemptSupervisor(
		controls, launcher, capabilities, finalizer,
		harnesspool.DefaultControlAttemptSupervisorConfig(),
	)
	if err != nil {
		return err
	}
	controller, err := harnesspool.NewController(coreClient, identities, harnesspool.ControllerConfig{
		HolderID: poolInstanceID, DispatchLockTTL: 30 * time.Second,
		AttemptLeaseTTL: 45 * time.Second, LongPollTimeout: 20 * time.Second,
		ContentionBackoff: time.Second,
	})
	if err != nil {
		return err
	}
	pool, err := harnesspool.NewPool(
		controller, preparer, coreClient, identities, supervisor,
		&harnessPoolFailureReporter{writer: stderr},
		harnesspool.PoolConfig{
			MaxConcurrentAttempts: config.maxConcurrent,
			LeaseRenewInterval:    15 * time.Second,
			IdleBackoff:           100 * time.Millisecond, FailureBackoff: time.Second,
			CleanupTimeout: 30 * time.Second,
		},
	)
	if err != nil {
		return err
	}

	readiness := &harnessPoolReadiness{}
	server := &http.Server{
		Handler:           harnessPoolRoutes(controls, readiness),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 * 1024,
		TLSConfig:         controlTLS,
	}
	readiness.ready.Store(true)
	fmt.Fprintf(
		stdout,
		"harness-pool serve: INSECURE DEV capabilities/object store; holder %s; control %s; max attempts %d\n",
		poolInstanceID, callbackEndpoint, config.maxConcurrent,
	)
	return runHarnessPoolServices(ctx, pool, controls, server, tls.NewListener(listener, controlTLS), readiness)
}

func developmentRunLaunchProfile(config harnessPoolDevelopmentConfig, callbackEndpoint string) harnesspool.RunLaunchProfile {
	maxApproval := min(config.maxRunDuration, 5*time.Minute)
	maxExecution := min(config.maxRunDuration, 10*time.Minute)
	return harnesspool.RunLaunchProfile{
		CodexRuntimeManifestDigest: config.runtimeDigest,
		Model: runmanifest.ModelRoute{
			Model: config.model, Provider: config.modelProvider, Endpoint: config.modelEndpoint,
			TLSIdentity: config.modelTLSIdentity, Audience: developmentModelAudience,
		},
		ExecutorMCPEndpoint:    config.executorMCPEndpoint,
		ExecutorMCPTLSIdentity: config.executorMCPIdentity,
		ExecutorMCPAudience:    developmentExecutorMCPAudience,
		Limits: runmanifest.RunLimits{
			MaxRunDurationMS:                config.maxRunDuration.Milliseconds(),
			MaxApprovalTTLMS:                maxApproval.Milliseconds(),
			GatewayActiveExecutionTimeoutMS: maxExecution.Milliseconds(),
			MCPTransportGraceMS:             5_000, WorkerCallbackGraceMS: 10_000,
			MaxEventBufferBytes: 8 * 1024 * 1024, MaxControlBufferBytes: 2 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: config.allowlistVersion,
		WorkerImageDigest:          config.workerDigest, ExpectedServiceAccount: config.serviceAccount,
		ControllerCallbackEndpoint: callbackEndpoint,
		ControllerCallbackIdentity: config.poolTLSIdentity,
		ControllerCallbackAudience: developmentControlAudience,
	}
}

func runHarnessPoolServices(
	ctx context.Context,
	pool *harnesspool.Pool,
	controls *harnesspool.ControlServer,
	server *http.Server,
	listener net.Listener,
	readiness *harnessPoolReadiness,
) error {
	if ctx == nil || pool == nil || controls == nil || server == nil || listener == nil || readiness == nil {
		return errors.New("complete harness-pool service runtime is required")
	}
	runContext, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(errors.New("harness-pool service ended"))
	poolDone := make(chan error, 1)
	serverDone := make(chan error, 1)
	go func() { poolDone <- pool.Run(runContext) }()
	go func() { serverDone <- server.Serve(listener) }()

	var poolErr, serverErr error
	poolFinished := false
	serverFinished := false
	select {
	case <-ctx.Done():
		cancelRun(context.Cause(ctx))
	case poolErr = <-poolDone:
		poolFinished = true
		if poolErr == nil {
			poolErr = errors.New("harness-pool controller stopped unexpectedly")
		}
		cancelRun(poolErr)
	case serverErr = <-serverDone:
		serverFinished = true
		if serverErr == nil {
			serverErr = errors.New("harness-pool control server stopped unexpectedly")
		}
		cancelRun(serverErr)
	}
	if !poolFinished {
		poolErr = <-poolDone
	}
	readiness.ready.Store(false)
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), harnessPoolShutdownTimeout)
	defer cancelShutdown()
	controlErr := controls.Shutdown(shutdownContext)
	shutdownErr := server.Shutdown(shutdownContext)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	if !serverFinished {
		serverErr = <-serverDone
	}
	if errors.Is(serverErr, http.ErrServerClosed) {
		serverErr = nil
	}
	return errors.Join(poolErr, controlErr, shutdownErr, serverErr)
}

func harnessPoolRoutes(controls http.Handler, readiness *harnessPoolReadiness) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(harnesspool.HarnessControlPath, controls)
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		writeHarnessPoolHealth(response, http.StatusOK, `{"status":"ok"}`)
	})
	mux.HandleFunc("GET /readyz", func(response http.ResponseWriter, _ *http.Request) {
		if readiness == nil || !readiness.ready.Load() {
			writeHarnessPoolHealth(response, http.StatusServiceUnavailable, `{"status":"not_ready"}`)
			return
		}
		writeHarnessPoolHealth(response, http.StatusOK, `{"status":"ready"}`)
	})
	return mux
}

func writeHarnessPoolHealth(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, body+"\n")
}

func harnessPoolCallbackEndpoint(configuredAddress string, actualAddress net.Addr) (string, error) {
	configuredHost, _, err := net.SplitHostPort(configuredAddress)
	if err != nil {
		return "", fmt.Errorf("parse configured harness-pool callback address: %w", err)
	}
	_, actualPort, err := net.SplitHostPort(actualAddress.String())
	if err != nil {
		return "", fmt.Errorf("parse bound harness-pool callback address: %w", err)
	}
	return "https://" + net.JoinHostPort(configuredHost, actualPort) + harnesspool.HarnessControlPath, nil
}

func newHarnessPoolCoreHTTPClient(
	caFile, certificateFile, keyFile, serverName, expectedClientIdentity string,
) (*http.Client, error) {
	certificate, err := loadHarnessPoolCertificate(certificateFile, keyFile, expectedClientIdentity)
	if err != nil {
		return nil, fmt.Errorf("load harness-pool core client identity: %w", err)
	}
	rootCAs, err := loadHarnessPoolCertPool("core server CA", caFile)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 35 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: rootCAs,
			Certificates: []tls.Certificate{certificate}, ServerName: serverName,
		},
	}
	return &http.Client{Transport: transport}, nil
}

func newHarnessPoolControlTLSConfig(
	certificateFile, keyFile, clientCAFile, expectedServerIdentity string,
) (*tls.Config, error) {
	certificate, err := loadHarnessPoolCertificate(certificateFile, keyFile, expectedServerIdentity)
	if err != nil {
		return nil, fmt.Errorf("load harness-pool control TLS identity: %w", err)
	}
	clientCAs, err := loadHarnessPoolCertPool("harness worker client CA", clientCAFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
		ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: clientCAs,
		NextProtos: []string{"http/1.1"},
	}, nil
}

func loadHarnessPoolCertificate(certificateFile, keyFile, expectedIdentity string) (tls.Certificate, error) {
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
		return tls.Certificate{}, errors.New("TLS leaf certificate does not contain the exact configured harness-pool SPIFFE identity")
	}
	certificate.Leaf = leaf
	return certificate, nil
}

func loadHarnessPoolCertPool(label, path string) (*x509.CertPool, error) {
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
