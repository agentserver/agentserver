package adapter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

const (
	// SGTAEDomainSuffix is the I18N production sandboxd authority selected by
	// AIPaaSGatewayRegionI18nProd. Keeping it explicit here lets the transport
	// reject every destination outside the reviewed TAE control/data plane.
	SGTAEDomainSuffix     = "sg.ai-sandbox-i18n.byted.org"
	SGTAEControlPlaneHost = "controlplane." + SGTAEDomainSuffix

	// TAEProxyURLSG is the legacy single-profile i18n-tt route. The socks5h
	// scheme is mandatory: the configured Merlin proxy resolves both the fixed
	// control plane and per-session data-plane names on the remote side.
	TAEProxyURLSG = "socks5h://ssh-egress-merlin-i18nbd-syd2a-83092-headless.ssh-egress.svc.cluster.local:1080"
)

type StrictHTTPClientConfig struct {
	RootCAs               *x509.CertPool
	ServerName            string
	TotalTimeout          time.Duration
	ResponseHeaderTimeout time.Duration
	MaxIdleConnections    int
}

// TAENetworkRoute is immutable deployment authority for one provider process.
// An empty ProxyURL means direct dialing; otherwise only canonical socks5h is
// accepted and DNS resolution is delegated to that proxy.
type TAENetworkRoute struct {
	ControlPlaneHost      string
	DataPlaneDomainSuffix string
	ProxyURL              string
}

// NewIdentityHTTPClient returns a shallow clone of client whose requests are
// authenticated by the provider-owned dynamic HeaderSource. The original
// client is never mutated, which keeps transport ownership and idle-connection
// shutdown with the caller. Only provider identity headers are accepted; a
// caller cannot smuggle a second provider identity through the SDK client.
func NewIdentityHTTPClient(client *http.Client, headers HeaderSource) (*http.Client, error) {
	if client == nil || client.Transport == nil || headers == nil {
		return nil, errors.New("identity HTTP client and header source are required")
	}
	clone := *client
	clone.Transport = &identityRoundTripper{base: client.Transport, headers: headers}
	return &clone, nil
}

type identityRoundTripper struct {
	base    http.RoundTripper
	headers HeaderSource
}

type rejectedIdentityRefresher interface {
	refreshRejectedIdentity(context.Context, string) error
}

func (transport *identityRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport == nil || transport.base == nil || transport.headers == nil {
		return nil, errors.New("identity HTTP transport is unavailable")
	}
	if request == nil {
		return nil, errors.New("identity HTTP transport received a nil request")
	}
	if containsProviderIdentityHeader(request.Header) {
		return nil, errors.New("identity HTTP request already contains a provider token")
	}
	identity, err := transport.headers.Headers(request.Context())
	if err != nil {
		return nil, errors.New("provider identity is unavailable")
	}
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	if err := copyIdentityHeaders(clone.Header, identity); err != nil {
		return nil, errors.New("provider identity header is invalid")
	}
	response, err := transport.base.RoundTrip(clone)
	if response != nil && response.StatusCode == http.StatusUnauthorized {
		refreshRejectedProviderIdentity(transport.headers, request.Context(), identity)
	}
	return response, err
}

func refreshRejectedProviderIdentity(source HeaderSource, requestContext context.Context, identity http.Header) {
	refresher, ok := source.(rejectedIdentityRefresher)
	if !ok || identity == nil {
		return
	}
	token := identity.Get("X-Jwt-Token")
	if token == "" {
		return
	}
	// A completed 401 is authoritative, but the request context may be canceled
	// as soon as the caller receives it. Preserve values while giving the
	// bounded identity source its own exchange deadline.
	_ = refresher.refreshRejectedIdentity(context.WithoutCancel(requestContext), token)
}

func containsProviderIdentityHeader(headers http.Header) bool {
	for name := range headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if canonical == "X-Zti-Token" || canonical == "X-Jwt-Token" {
			return true
		}
	}
	return false
}

// NewStrictHTTPClient builds a direct strict client for isolated tests and
// non-SG helpers. Production SG control/data-plane traffic must use one of the
// pinned constructors below. InsecureSkipVerify is never exposed as a knob.
func NewStrictHTTPClient(config StrictHTTPClientConfig) (*http.Client, error) {
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	return newStrictHTTPClient(config, dialer.DialContext)
}

// NewSGTAEControlHTTPClient routes only the fixed I18N production control
// plane through the legacy reviewed i18n-tt SOCKS path. A caller cannot use
// this client as a general-purpose proxy transport.
func NewSGTAEControlHTTPClient(config StrictHTTPClientConfig, proxyURL string) (*http.Client, error) {
	return newSGTAEHTTPClient(config, proxyURL, validateSGTAEControlTarget)
}

// NewSGTAEDataHTTPClient routes only canonical per-session sandboxd hosts
// below the fixed I18N production suffix through the same i18n-tt route.
func NewSGTAEDataHTTPClient(config StrictHTTPClientConfig, proxyURL string) (*http.Client, error) {
	return newSGTAEHTTPClient(config, proxyURL, validateSGTAEDataTarget)
}

func NewTAEControlHTTPClient(config StrictHTTPClientConfig, route TAENetworkRoute) (*http.Client, error) {
	route, err := validateTAENetworkRoute(route)
	if err != nil {
		return nil, err
	}
	return newPinnedTAEHTTPClient(config, route.ProxyURL, func(network, address string) error {
		host, err := validateTAETLSAddress(network, address)
		if err != nil {
			return err
		}
		if host != route.ControlPlaneHost {
			return errors.New("TAE control-plane target is outside the configured route")
		}
		return nil
	})
}

func NewTAEDataHTTPClient(config StrictHTTPClientConfig, route TAENetworkRoute) (*http.Client, error) {
	route, err := validateTAENetworkRoute(route)
	if err != nil {
		return nil, err
	}
	return newPinnedTAEHTTPClient(config, route.ProxyURL, func(network, address string) error {
		host, err := validateTAETLSAddress(network, address)
		if err != nil {
			return err
		}
		sessionID, ok := strings.CutSuffix(host, "."+route.DataPlaneDomainSuffix)
		if !ok || sessionID == "controlplane" || !sessionDNSLabelPattern.MatchString(sessionID) || strings.ToLower(sessionID) != sessionID {
			return errors.New("TAE data-plane target is outside the configured route")
		}
		return nil
	})
}

type targetValidator func(network, address string) error

func newSGTAEHTTPClient(config StrictHTTPClientConfig, proxyURL string, validate targetValidator) (*http.Client, error) {
	if proxyURL != TAEProxyURLSG {
		return nil, fmt.Errorf("SG TAE proxy must be exactly %s", TAEProxyURLSG)
	}
	dialContext, err := newPinnedSOCKS5HDialContext(proxyURL, validate)
	if err != nil {
		return nil, err
	}
	return newStrictHTTPClient(config, dialContext)
}

func newPinnedTAEHTTPClient(config StrictHTTPClientConfig, proxyURL string, validate targetValidator) (*http.Client, error) {
	if validate == nil {
		return nil, errors.New("TAE target validator is required")
	}
	if proxyURL != "" {
		dialContext, err := newPinnedSOCKS5HDialContext(proxyURL, validate)
		if err != nil {
			return nil, err
		}
		return newStrictHTTPClient(config, dialContext)
	}
	direct := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	return newStrictHTTPClient(config, func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := validate(network, address); err != nil {
			return nil, err
		}
		return direct.DialContext(ctx, network, address)
	})
}

func validateTAENetworkRoute(route TAENetworkRoute) (TAENetworkRoute, error) {
	route.ControlPlaneHost = strings.TrimSpace(strings.ToLower(route.ControlPlaneHost))
	route.DataPlaneDomainSuffix = strings.TrimSpace(strings.ToLower(route.DataPlaneDomainSuffix))
	if route.ControlPlaneHost == "" || route.DataPlaneDomainSuffix == "" ||
		route.ControlPlaneHost != "controlplane."+route.DataPlaneDomainSuffix ||
		strings.HasSuffix(route.DataPlaneDomainSuffix, ".") || strings.ContainsAny(route.DataPlaneDomainSuffix, "\x00\r\n /:@") {
		return TAENetworkRoute{}, errors.New("TAE control/data-plane authority is invalid")
	}
	if route.ProxyURL != "" {
		if err := ValidateTAEProxyURL(route.ProxyURL); err != nil {
			return TAENetworkRoute{}, err
		}
	}
	return route, nil
}

func ValidateTAEProxyURL(rawProxyURL string) error {
	parsed, err := url.Parse(rawProxyURL)
	if err != nil || parsed.String() != rawProxyURL || parsed.Scheme != "socks5h" || parsed.User != nil ||
		parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		parsed.Path != "" || parsed.RawPath != "" {
		return errors.New("TAE proxy must be a canonical unauthenticated socks5h URL")
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || host == "" || strings.ToLower(host) != host ||
		parsed.Hostname() != host || parsed.Port() != port {
		return errors.New("TAE proxy authority is invalid")
	}
	return nil
}

func newStrictHTTPClient(config StrictHTTPClientConfig, dialContext func(context.Context, string, string) (net.Conn, error)) (*http.Client, error) {
	if dialContext == nil {
		return nil, errors.New("strict HTTP client dialer is required")
	}
	if config.TotalTimeout < 0 || config.TotalTimeout > 5*time.Minute {
		return nil, errors.New("strict HTTP client total timeout is invalid")
	}
	if config.ResponseHeaderTimeout == 0 {
		config.ResponseHeaderTimeout = 15 * time.Second
	}
	if config.ResponseHeaderTimeout < time.Second || config.ResponseHeaderTimeout > time.Minute {
		return nil, errors.New("strict HTTP response-header timeout is invalid")
	}
	if config.MaxIdleConnections == 0 {
		config.MaxIdleConnections = 64
	}
	if config.MaxIdleConnections < 1 || config.MaxIdleConnections > 1024 {
		return nil, errors.New("strict HTTP idle-connection limit is invalid")
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          config.MaxIdleConnections,
		MaxIdleConnsPerHost:   config.MaxIdleConnections,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: config.RootCAs, ServerName: config.ServerName,
		},
	}
	return &http.Client{
		Transport: transport, Timeout: config.TotalTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

func newPinnedSOCKS5HDialContext(rawProxyURL string, validate targetValidator) (func(context.Context, string, string) (net.Conn, error), error) {
	if validate == nil {
		return nil, errors.New("SOCKS5H target validator is required")
	}
	if err := ValidateTAEProxyURL(rawProxyURL); err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(rawProxyURL)
	forward := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	dialer, err := xproxy.SOCKS5("tcp", parsed.Host, nil, forward)
	if err != nil {
		return nil, errors.New("configure TAE SOCKS5H dialer")
	}
	contextDialer, ok := dialer.(xproxy.ContextDialer)
	if !ok {
		return nil, errors.New("TAE SOCKS5H dialer does not support context cancellation")
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if err := validate(network, address); err != nil {
			return nil, err
		}
		return contextDialer.DialContext(ctx, "tcp", address)
	}, nil
}

func validateSGTAEControlTarget(network, address string) error {
	host, err := validateSGTAETLSAddress(network, address)
	if err != nil {
		return err
	}
	if host != SGTAEControlPlaneHost {
		return errors.New("SG TAE control-plane proxy target is not allowed")
	}
	return nil
}

func validateSGTAEDataTarget(network, address string) error {
	host, err := validateSGTAETLSAddress(network, address)
	if err != nil {
		return err
	}
	sessionID, ok := strings.CutSuffix(host, "."+SGTAEDomainSuffix)
	if !ok || sessionID == "controlplane" || !sessionDNSLabelPattern.MatchString(sessionID) || strings.ToLower(sessionID) != sessionID {
		return errors.New("SG TAE data-plane proxy target is not an allowed session authority")
	}
	return nil
}

func validateSGTAETLSAddress(network, address string) (string, error) {
	return validateTAETLSAddress(network, address)
}

func validateTAETLSAddress(network, address string) (string, error) {
	if network != "tcp" {
		return "", errors.New("TAE route only permits TCP")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port != "443" || strings.ToLower(host) != host || strings.HasSuffix(host, ".") {
		return "", errors.New("TAE route target must be a canonical lowercase TLS authority")
	}
	return host, nil
}
