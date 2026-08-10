package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agentserver/agentserver/v2/internal/sandboxgatewayapp"
	"github.com/agentserver/agentserver/v2/internal/taeimage"
	"github.com/agentserver/agentserver/v2/providers/tae/adapter"
)

const (
	controlTimeoutEnvironment       = "AGENTSERVER_V2_TAE_CONTROL_TIMEOUT"
	headerTimeoutEnvironment        = "AGENTSERVER_V2_TAE_RESPONSE_HEADER_TIMEOUT"
	streamGraceEnvironment          = "AGENTSERVER_V2_TAE_STREAM_GRACE"
	reconnectAttemptsEnvironment    = "AGENTSERVER_V2_TAE_RECONNECT_ATTEMPTS"
	reconnectDelayEnvironment       = "AGENTSERVER_V2_TAE_RECONNECT_DELAY"
	signalTimeoutEnvironment        = "AGENTSERVER_V2_TAE_SIGNAL_TIMEOUT"
	maxReadBytesEnvironment         = "AGENTSERVER_V2_TAE_MAX_READ_SOURCE_BYTES"
	sandboxImageEnvironment         = "AGENTSERVER_V2_TAE_SANDBOX_IMAGE"
	authModeEnvironment             = "AGENTSERVER_V2_TAE_AUTH_MODE"
	byteCloudSiteEnvironment        = "AGENTSERVER_V2_TAE_BYTECLOUD_SITE"
	byteCloudAccessKeyEnvironment   = "AGENTSERVER_V2_TAE_BYTECLOUD_ACCESS_KEY_ID_FILE"
	byteCloudSecretEnvironment      = "AGENTSERVER_V2_TAE_BYTECLOUD_SECRET_ACCESS_KEY_FILE"
	byteCloudJWTEndpointEnvironment = "AGENTSERVER_V2_TAE_BYTECLOUD_JWT_ENDPOINT"
	byteCloudJWTTimeoutEnvironment  = "AGENTSERVER_V2_TAE_BYTECLOUD_JWT_TIMEOUT"
	taeProxyEnvironment             = "AGENTSERVER_V2_TAE_PROXY_URL"
	byteCloudAppAKSKAuthMode        = "bytecloud-app-aksk-v1"
	unsafeTLSBypassEnvironment      = "BYTEDAI_NO_SSL_VERIFY"
)

var forbiddenProxyEnvironments = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"}

type providerConfig struct {
	controlTimeout    time.Duration
	headerTimeout     time.Duration
	streamGrace       time.Duration
	reconnectAttempts int
	reconnectDelay    time.Duration
	signalTimeout     time.Duration
	maxReadBytes      int64
	sandboxImage      string
	authMode          string
	byteCloudSite     string
	accessKeyFile     string
	secretKeyFile     string
	jwtEndpoint       string
	proxyURL          string
	jwtRequestTimeout time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, serve, probeNetwork))
}

type serveFunc func(context.Context, func(string) string, io.Writer, io.Writer) error
type probeFunc func(context.Context, func(string) string, io.Writer) (bool, error)

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, serve serveFunc, probe probeFunc) int {
	if len(args) != 1 || (args[0] != "serve" && args[0] != "probe-network") {
		writeUsage(stderr)
		return 2
	}
	if ctx == nil || getenv == nil || stdout == nil || stderr == nil {
		fmt.Fprintln(stderr, "tae-sandbox-gateway: runtime is unavailable")
		return 1
	}
	if args[0] == "probe-network" {
		if probe == nil {
			fmt.Fprintln(stderr, "tae-sandbox-gateway probe-network: runtime is unavailable")
			return 1
		}
		passed, err := probe(ctx, getenv, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "tae-sandbox-gateway probe-network: %v\n", err)
			return 1
		}
		if !passed {
			return 1
		}
		return 0
	}
	if serve == nil {
		fmt.Fprintln(stderr, "tae-sandbox-gateway serve: runtime is unavailable")
		return 1
	}
	if err := serve(ctx, getenv, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "tae-sandbox-gateway serve: %v\n", err)
		return 1
	}
	return 0
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "usage: tae-sandbox-gateway serve")
	fmt.Fprintln(writer, "       tae-sandbox-gateway probe-network")
}

func serve(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) error {
	appConfig, err := sandboxgatewayapp.LoadProductionConfig(getenv)
	if err != nil {
		return err
	}
	providerConfig, err := loadProviderConfig(getenv)
	if err != nil {
		return err
	}
	clients, err := newTAEClients(ctx, providerConfig, appConfig.ProviderPSM)
	if err != nil {
		return err
	}
	defer clients.Close()
	provider, err := adapter.NewProvider(adapter.Config{
		Control: clients.control, Data: clients.data, Region: appConfig.ProviderRegion, PSM: appConfig.ProviderPSM,
		Root: appConfig.Root, StreamGrace: providerConfig.streamGrace,
		ReconnectAttempts: providerConfig.reconnectAttempts, ReconnectDelay: providerConfig.reconnectDelay,
		SignalTimeout: providerConfig.signalTimeout, MaxReadSourceBytes: providerConfig.maxReadBytes,
		Policy: appConfig.TAEPolicy,
	})
	if err != nil {
		return err
	}
	probeContext, cancelProbe := context.WithTimeout(ctx, min(providerConfig.controlTimeout, 30*time.Second))
	defer cancelProbe()
	if err := clients.refresh(probeContext); err != nil {
		return fmt.Errorf("exchange ByteCloud application JWT for TAE readiness: %w", err)
	}
	if _, err := clients.control.Search(probeContext, adapter.SearchInput{
		Metadata: map[string]string{adapter.MetadataSandboxID: "agentserver-readiness-probe-never-created"}, Limit: 1,
	}); err != nil {
		return fmt.Errorf("TAE control-plane readiness probe: %w", err)
	}
	return sandboxgatewayapp.Serve(ctx, appConfig, provider, stdout, stderr)
}

func loadProviderConfig(getenv func(string) string) (providerConfig, error) {
	if getenv == nil {
		return providerConfig{}, errors.New("TAE provider configuration source is required")
	}
	if value := strings.TrimSpace(getenv(unsafeTLSBypassEnvironment)); value != "" && value != "0" && !strings.EqualFold(value, "false") {
		return providerConfig{}, fmt.Errorf("%s must not request TLS verification bypass", unsafeTLSBypassEnvironment)
	}
	for _, name := range forbiddenProxyEnvironments {
		if getenv(name) != "" {
			return providerConfig{}, fmt.Errorf("%s must be unset for the production TAE provider", name)
		}
	}
	if authMode := getenv(authModeEnvironment); authMode != byteCloudAppAKSKAuthMode {
		return providerConfig{}, fmt.Errorf("%s must be exactly %s", authModeEnvironment, byteCloudAppAKSKAuthMode)
	}
	byteCloudSite := getenv(byteCloudSiteEnvironment)
	if byteCloudSite != adapter.ByteCloudSiteI18NTT {
		return providerConfig{}, fmt.Errorf("%s must be exactly %s for the SG TAE provider", byteCloudSiteEnvironment, adapter.ByteCloudSiteI18NTT)
	}
	jwtEndpoint := getenv(byteCloudJWTEndpointEnvironment)
	if jwtEndpoint != adapter.ByteCloudJWTEndpointSG {
		return providerConfig{}, fmt.Errorf("%s must be exactly %s for the SG TAE provider", byteCloudJWTEndpointEnvironment, adapter.ByteCloudJWTEndpointSG)
	}
	proxyURL := getenv(taeProxyEnvironment)
	if proxyURL != adapter.TAEProxyURLSG {
		return providerConfig{}, fmt.Errorf("%s must be exactly %s for the SG TAE provider", taeProxyEnvironment, adapter.TAEProxyURLSG)
	}
	accessKeyFile, err := requiredAbsolutePath(getenv(byteCloudAccessKeyEnvironment), byteCloudAccessKeyEnvironment)
	if err != nil {
		return providerConfig{}, err
	}
	secretKeyFile, err := requiredAbsolutePath(getenv(byteCloudSecretEnvironment), byteCloudSecretEnvironment)
	if err != nil {
		return providerConfig{}, err
	}
	jwtRequestTimeout, err := optionalDuration(getenv(byteCloudJWTTimeoutEnvironment), 5*time.Second, time.Second, 30*time.Second, byteCloudJWTTimeoutEnvironment)
	if err != nil {
		return providerConfig{}, err
	}
	sandboxImage := strings.TrimSpace(getenv(sandboxImageEnvironment))
	if err := taeimage.ValidateContentTag(sandboxImage); err != nil {
		return providerConfig{}, fmt.Errorf("%s: %w", sandboxImageEnvironment, err)
	}
	controlTimeout, err := optionalDuration(getenv(controlTimeoutEnvironment), 45*time.Second, time.Second, time.Minute, controlTimeoutEnvironment)
	if err != nil {
		return providerConfig{}, err
	}
	headerTimeout, err := optionalDuration(getenv(headerTimeoutEnvironment), 15*time.Second, time.Second, time.Minute, headerTimeoutEnvironment)
	if err != nil {
		return providerConfig{}, err
	}
	streamGrace, err := optionalDuration(getenv(streamGraceEnvironment), 30*time.Second, time.Second, 5*time.Minute, streamGraceEnvironment)
	if err != nil {
		return providerConfig{}, err
	}
	reconnectAttempts, err := optionalInt(getenv(reconnectAttemptsEnvironment), 2, 1, 5, reconnectAttemptsEnvironment)
	if err != nil {
		return providerConfig{}, err
	}
	reconnectDelay, err := optionalDuration(getenv(reconnectDelayEnvironment), 100*time.Millisecond, 10*time.Millisecond, 5*time.Second, reconnectDelayEnvironment)
	if err != nil {
		return providerConfig{}, err
	}
	signalTimeout, err := optionalDuration(getenv(signalTimeoutEnvironment), 3*time.Second, 100*time.Millisecond, 30*time.Second, signalTimeoutEnvironment)
	if err != nil {
		return providerConfig{}, err
	}
	maxReadBytes, err := optionalInt64(getenv(maxReadBytesEnvironment), 8*1024*1024, 1, 8*1024*1024, maxReadBytesEnvironment)
	if err != nil {
		return providerConfig{}, err
	}
	return providerConfig{
		controlTimeout: controlTimeout, headerTimeout: headerTimeout, streamGrace: streamGrace,
		reconnectAttempts: reconnectAttempts, reconnectDelay: reconnectDelay,
		signalTimeout: signalTimeout, maxReadBytes: maxReadBytes, sandboxImage: sandboxImage,
		authMode: byteCloudAppAKSKAuthMode, byteCloudSite: byteCloudSite,
		accessKeyFile: accessKeyFile, secretKeyFile: secretKeyFile,
		jwtEndpoint: jwtEndpoint, proxyURL: proxyURL, jwtRequestTimeout: jwtRequestTimeout,
	}, nil
}

func requiredAbsolutePath(value, name string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	if strings.TrimSpace(value) != value || !validCredentialPath(value) {
		return "", fmt.Errorf("%s must be an absolute clean path", name)
	}
	return value, nil
}

func optionalDuration(value string, fallback, minimum, maximum time.Duration, name string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < minimum || parsed > maximum || parsed%time.Millisecond != 0 {
		return 0, fmt.Errorf("%s must be a whole-millisecond duration between %s and %s", name, minimum, maximum)
	}
	return parsed, nil
}

func optionalInt(value string, fallback, minimum, maximum int, name string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return parsed, nil
}

func optionalInt64(value string, fallback, minimum, maximum int64, name string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return parsed, nil
}
