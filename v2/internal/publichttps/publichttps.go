// Package publichttps provides the outbound HTTPS boundary used for
// workspace-configured OIDC providers and LLM gateways. URL validation alone
// is not enough for user-controlled hosts: the controlled dialer resolves DNS,
// rejects every non-public answer, and dials one of the exact validated
// addresses so a second resolver lookup cannot rebind the connection.
package publichttps

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

var forbiddenPrefixes = mustPrefixes(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.31.196.0/24",
	"192.52.193.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"192.175.48.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"::/96",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001::/23",
	"2001:db8::/32",
	"2002::/16",
	"2620:4f:8000::/48",
	"3fff::/20",
	"5f00::/16",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type ClientConfig struct {
	Resolver Resolver
	Dialer   Dialer
	Timeout  time.Duration
	// NoOverallTimeout is for streaming protocols whose caller already owns a
	// hard context deadline. DNS, connect, TLS, and response-header bounds still
	// apply; only http.Client's whole-response timer is disabled.
	NoOverallTimeout      bool
	ResponseHeaderTimeout time.Duration
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
}

// ValidateURL accepts one canonical public HTTPS URL. Workspace-controlled
// destinations use the default HTTPS port only and may not contain URL
// credentials, query, fragment, an encoded path, or a literal IP address.
// When requiredPath is non-empty, the decoded path must match it exactly.
func ValidateURL(raw, requiredPath string) (*url.URL, error) {
	if raw == "" || len(raw) > 4096 || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\x00\r\n") {
		return nil, errors.New("public HTTPS URL is empty or outside protocol bounds")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" ||
		parsed.Opaque != "" || parsed.ForceQuery || (parsed.Port() != "" && parsed.Port() != "443") {
		return nil, errors.New("public HTTPS URL must use the default HTTPS port without credentials, query, fragment, or encoded path")
	}
	hostname := parsed.Hostname()
	if net.ParseIP(hostname) != nil || !validDNSName(hostname) {
		return nil, errors.New("public HTTPS URL host must be a canonical lowercase DNS name, not a literal IP address")
	}
	if requiredPath != "" && parsed.Path != requiredPath {
		return nil, fmt.Errorf("public HTTPS URL path must be exactly %s", requiredPath)
	}
	return parsed, nil
}

// ValidateIssuer applies the public URL profile to an OIDC issuer while
// retaining the OIDC requirement that its configured identifier has no
// trailing slash.
func ValidateIssuer(raw string) (*url.URL, error) {
	parsed, err := ValidateURL(raw, "")
	if err != nil {
		return nil, err
	}
	if parsed.Path == "/" || strings.HasSuffix(parsed.Path, "/") {
		return nil, errors.New("OIDC issuer must not end in a slash")
	}
	return parsed, nil
}

// NewClient returns a system-trusted HTTPS client that never uses ambient
// proxies, never follows redirects, and performs a controlled DNS-to-dial
// operation for every new connection.
func NewClient(config ClientConfig) (*http.Client, error) {
	if config.Resolver == nil {
		config.Resolver = net.DefaultResolver
	}
	if config.Dialer == nil {
		config.Dialer = &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	}
	if config.Timeout == 0 && !config.NoOverallTimeout {
		config.Timeout = defaultTimeout
	}
	if config.ResponseHeaderTimeout == 0 {
		config.ResponseHeaderTimeout = 20 * time.Second
	}
	if config.MaxIdleConns == 0 {
		config.MaxIdleConns = 64
	}
	if config.MaxIdleConnsPerHost == 0 {
		config.MaxIdleConnsPerHost = 16
	}
	if (!config.NoOverallTimeout && (config.Timeout < time.Second || config.Timeout > 2*time.Minute)) ||
		(config.NoOverallTimeout && config.Timeout != 0) ||
		config.ResponseHeaderTimeout < time.Second || config.ResponseHeaderTimeout > time.Minute ||
		config.MaxIdleConns < 1 || config.MaxIdleConns > 1024 ||
		config.MaxIdleConnsPerHost < 1 || config.MaxIdleConnsPerHost > 256 {
		return nil, errors.New("public HTTPS client limits are outside the supported bounds")
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           controlledDialContext(config.Resolver, config.Dialer),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          config.MaxIdleConns,
		MaxIdleConnsPerHost:   config.MaxIdleConnsPerHost,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		DisableCompression:    true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   config.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("public HTTPS redirects are forbidden")
		},
	}, nil
}

func controlledDialContext(resolver Resolver, dialer Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" || net.ParseIP(host) != nil || !validDNSName(host) {
			return nil, errors.New("public HTTPS dial target is invalid")
		}
		addresses, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("resolve public HTTPS host: %w", err)
		}
		for _, candidate := range addresses {
			if !IsPublicAddress(candidate) {
				return nil, errors.New("public HTTPS DNS answer contains a non-public address")
			}
		}
		var dialErr error
		for _, candidate := range addresses {
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
			if err == nil {
				return connection, nil
			}
			dialErr = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, fmt.Errorf("dial public HTTPS host: %w", dialErr)
	}
}

// IsPublicAddress is exported so deployment and focused security tests can
// use the same application-layer destination policy.
func IsPublicAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range forbiddenPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

// ForbiddenPrefixes returns a defensive copy of the exact special-use
// exclusions enforced by the controlled dialer. Deployment renderers use it
// to keep their broad TCP/443 egress rule aligned with the application-layer
// SSRF boundary.
func ForbiddenPrefixes() []netip.Prefix {
	return append([]netip.Prefix(nil), forbiddenPrefixes...)
}

func validDNSName(host string) bool {
	if host == "" || len(host) > 253 || host != strings.ToLower(host) || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
				return false
			}
		}
	}
	return true
}

func mustPrefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, len(values))
	for index, value := range values {
		result[index] = netip.MustParsePrefix(value)
	}
	return result
}
