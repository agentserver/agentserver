package adapter

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type testRoundTripFunc func(*http.Request) (*http.Response, error)

func (function testRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestIdentityHTTPClientInjectsJWTIntoCloneOnly(t *testing.T) {
	var captured *http.Request
	base := &http.Client{Transport: testRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		captured = request
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})}
	client, err := NewIdentityHTTPClient(base, HeaderSourceFunc(func(context.Context) (http.Header, error) {
		return http.Header{"X-Jwt-Token": []string{"header.payload.signature"}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://tae.example.test/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Request-Id", "request-1")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if captured == nil || captured == request || captured.Header.Get("X-Jwt-Token") != "header.payload.signature" ||
		captured.Header.Get("X-Request-Id") != "request-1" {
		t.Fatalf("captured request = %#v", captured)
	}
	if request.Header.Get("X-Jwt-Token") != "" {
		t.Fatal("identity transport mutated the caller request")
	}
	if base.Transport == client.Transport {
		t.Fatal("identity client mutated or reused the caller transport wrapper")
	}
}

func TestIdentityHTTPClientFailsClosedBeforeDispatch(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		header http.Header
		source HeaderSource
	}{
		{name: "existing JWT", header: http.Header{"X-Jwt-Token": []string{"caller-token"}}, source: staticJWTSource()},
		{name: "existing empty JWT", header: http.Header{"x-jwt-token": []string{""}}, source: staticJWTSource()},
		{name: "existing ZTI", header: http.Header{"X-Zti-Token": []string{"caller-token"}}, source: staticJWTSource()},
		{name: "source failure", source: HeaderSourceFunc(func(context.Context) (http.Header, error) {
			return nil, errors.New("secret source failure")
		})},
		{name: "unsupported header", source: HeaderSourceFunc(func(context.Context) (http.Header, error) {
			return http.Header{"Authorization": []string{"Bearer secret"}}, nil
		})},
		{name: "multiple identities", source: HeaderSourceFunc(func(context.Context) (http.Header, error) {
			return http.Header{"X-Jwt-Token": []string{"jwt"}, "X-Zti-Token": []string{"zti"}}, nil
		})},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			base := &http.Client{Transport: testRoundTripFunc(func(*http.Request) (*http.Response, error) {
				called = true
				return nil, errors.New("unexpected dispatch")
			})}
			client, err := NewIdentityHTTPClient(base, testCase.source)
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://tae.example.test/session", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header = testCase.header.Clone()
			_, err = client.Do(request)
			if err == nil || called {
				t.Fatalf("fail-closed result = %v, called=%v", err, called)
			}
			if strings.Contains(err.Error(), "caller-token") || strings.Contains(err.Error(), "secret source failure") || strings.Contains(err.Error(), "Bearer secret") {
				t.Fatalf("provider identity leaked through error: %v", err)
			}
		})
	}
}

func TestIdentityHTTPClientRefreshesAfter401WithoutReplaying(t *testing.T) {
	normal := testCompactJWT(time.Now().Add(10 * time.Minute))
	forced := testCompactJWT(time.Now().Add(20 * time.Minute))
	generator := &fakeAKSKGenerator{normalTokens: []string{normal}, forcedTokens: []string{forced}}
	source, err := NewByteCloudJWTHeaderSource(ByteCloudJWTHeaderSourceConfig{
		AccessKeyID: "AK-example", SecretAccessKey: "SK-secret", Site: ByteCloudSiteI18NTT,
		JWTEndpoint: ByteCloudJWTEndpointSG, ProxyURL: TAEProxyURLSG, Generator: generator,
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	var observed []string
	base := &http.Client{Transport: testRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		observed = append(observed, request.Header.Get("X-Jwt-Token"))
		status := http.StatusOK
		if requests == 1 {
			status = http.StatusUnauthorized
		}
		return &http.Response{
			StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request,
		}, nil
	})}
	client, err := NewIdentityHTTPClient(base, source)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://tae.example.test/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || requests != 1 {
		t.Fatalf("first response/requests = %d/%d; a rejected operation must not be replayed", response.StatusCode, requests)
	}
	request, err = http.NewRequestWithContext(t.Context(), http.MethodGet, "https://tae.example.test/session", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || requests != 2 || len(observed) != 2 || observed[0] != normal || observed[1] != forced {
		t.Fatalf("responses used requests=%d identities=%q status=%d", requests, observed, response.StatusCode)
	}
	generator.mu.Lock()
	normalCalls, forcedCalls := generator.normalCalls, generator.forcedCalls
	generator.mu.Unlock()
	if normalCalls != 1 || forcedCalls != 1 {
		t.Fatalf("identity generation calls = normal:%d forced:%d", normalCalls, forcedCalls)
	}
}

func TestSGTAETargetPoliciesAreClosedWorld(t *testing.T) {
	for _, target := range []struct {
		name     string
		validate targetValidator
		address  string
	}{
		{name: "control", validate: validateSGTAEControlTarget, address: SGTAEControlPlaneHost + ":443"},
		{name: "data", validate: validateSGTAEDataTarget, address: "session-123." + SGTAEDomainSuffix + ":443"},
	} {
		t.Run(target.name, func(t *testing.T) {
			if err := target.validate("tcp", target.address); err != nil {
				t.Fatalf("allowed target was rejected: %v", err)
			}
		})
	}
	for name, testCase := range map[string]struct {
		validate targetValidator
		network  string
		address  string
	}{
		"control sibling": {validateSGTAEControlTarget, "tcp", "other." + SGTAEDomainSuffix + ":443"},
		"data control":    {validateSGTAEDataTarget, "tcp", SGTAEControlPlaneHost + ":443"},
		"data subdomain":  {validateSGTAEDataTarget, "tcp", "nested.session." + SGTAEDomainSuffix + ":443"},
		"uppercase":       {validateSGTAEDataTarget, "tcp", "SESSION." + SGTAEDomainSuffix + ":443"},
		"wrong port":      {validateSGTAEControlTarget, "tcp", SGTAEControlPlaneHost + ":80"},
		"udp":             {validateSGTAEControlTarget, "udp", SGTAEControlPlaneHost + ":443"},
		"public target":   {validateSGTAEControlTarget, "tcp", "example.com:443"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := testCase.validate(testCase.network, testCase.address); err == nil {
				t.Fatal("forbidden SG TAE proxy target was accepted")
			}
		})
	}
}

func TestPinnedSOCKS5HDialerDelegatesTargetDNS(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	targets := make(chan string, 1)
	serverErrors := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErrors <- acceptErr
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		greeting := make([]byte, 3)
		if _, readErr := io.ReadFull(connection, greeting); readErr != nil || string(greeting) != string([]byte{5, 1, 0}) {
			serverErrors <- errors.New("invalid SOCKS5 greeting")
			return
		}
		if _, writeErr := connection.Write([]byte{5, 0}); writeErr != nil {
			serverErrors <- writeErr
			return
		}
		header := make([]byte, 5)
		if _, readErr := io.ReadFull(connection, header); readErr != nil || string(header[:4]) != string([]byte{5, 1, 0, 3}) {
			serverErrors <- errors.New("SOCKS5 request did not use a domain target")
			return
		}
		host := make([]byte, int(header[4]))
		port := make([]byte, 2)
		if _, readErr := io.ReadFull(connection, host); readErr != nil {
			serverErrors <- readErr
			return
		}
		if _, readErr := io.ReadFull(connection, port); readErr != nil {
			serverErrors <- readErr
			return
		}
		targets <- net.JoinHostPort(string(host), strconv.Itoa(int(binary.BigEndian.Uint16(port))))
		_, writeErr := connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		serverErrors <- writeErr
	}()

	proxyURL := "socks5h://" + listener.Addr().String()
	dialContext, err := newPinnedSOCKS5HDialContext(proxyURL, func(network, address string) error {
		if network != "tcp" || address != "does-not-resolve.invalid:443" {
			return errors.New("unexpected target")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := dialContext(t.Context(), "tcp", "does-not-resolve.invalid:443")
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	if target := <-targets; target != "does-not-resolve.invalid:443" {
		t.Fatalf("SOCKS5 target = %q", target)
	}
	if err := <-serverErrors; err != nil {
		t.Fatal(err)
	}
}

func TestSGTAEHTTPClientsRequireExactProxy(t *testing.T) {
	for _, constructor := range []func(StrictHTTPClientConfig, string) (*http.Client, error){
		NewSGTAEControlHTTPClient,
		NewSGTAEDataHTTPClient,
	} {
		if _, err := constructor(StrictHTTPClientConfig{}, "socks5h://other.example:1080"); err == nil {
			t.Fatal("non-production SG TAE proxy was accepted")
		}
		client, err := constructor(StrictHTTPClientConfig{}, TAEProxyURLSG)
		if err != nil {
			t.Fatal(err)
		}
		transport, ok := client.Transport.(*http.Transport)
		if !ok || transport.Proxy != nil || transport.DialContext == nil {
			t.Fatalf("proxied strict transport = %#v", client.Transport)
		}
		client.CloseIdleConnections()
	}
}

func staticJWTSource() HeaderSource {
	return HeaderSourceFunc(func(context.Context) (http.Header, error) {
		return http.Header{"X-Jwt-Token": []string{"header.payload.signature"}}, nil
	})
}
