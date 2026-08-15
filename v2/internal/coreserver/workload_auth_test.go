package coreserver

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/url"
	"testing"
)

func TestSPIFFEWorkloadAuthorizerRequiresVerifiedExactIdentity(t *testing.T) {
	authorizer, err := NewSPIFFEWorkloadAuthorizer("spiffe://agentserver.local/ns/agentserver/sa/executor-gateway")
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := url.Parse("spiffe://agentserver.local/ns/agentserver/sa/executor-gateway")
	request := &http.Request{TLS: &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{identity}}}}}}
	if err := authorizer.AuthorizeWorkload(request, "executor-connections.acquire"); err != nil {
		t.Fatalf("AuthorizeWorkload() error = %v", err)
	}
	request.TLS.VerifiedChains = nil
	if err := authorizer.AuthorizeWorkload(request, "executor-connections.acquire"); err == nil {
		t.Fatal("unverified certificate was authorized")
	}
	other, _ := url.Parse("spiffe://agentserver.local/ns/agentserver/sa/harness-pool")
	request.TLS.VerifiedChains = [][]*x509.Certificate{{{URIs: []*url.URL{identity, other}}}}
	if err := authorizer.AuthorizeWorkload(request, "executor-connections.acquire"); err == nil {
		t.Fatal("certificate carrying multiple workload identities was authorized")
	}
}

func TestSPIFFEWorkloadAuthorizerAcceptsOneOfSeveralExactIdentities(t *testing.T) {
	authorizer, err := NewSPIFFEWorkloadAuthorizer(
		"spiffe://agentserver.local/ns/agentserver/sa/sandbox-gateway-cn",
		"spiffe://agentserver.local/ns/agentserver/sa/sandbox-gateway-i18n-bd",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := requestWithVerifiedURI(t, "spiffe://agentserver.local/ns/agentserver/sa/sandbox-gateway-i18n-bd")
	if err := authorizer.AuthorizeWorkload(request, "managed-sandboxes.ensure"); err != nil {
		t.Fatalf("AuthorizeWorkload() error = %v", err)
	}
	request = requestWithVerifiedURI(t, "spiffe://agentserver.local/ns/agentserver/sa/sandbox-gateway-boe")
	if err := authorizer.AuthorizeWorkload(request, "managed-sandboxes.ensure"); err == nil {
		t.Fatal("AuthorizeWorkload() accepted an identity outside the exact set")
	}
}

func TestSPIFFEWorkloadAuthorizerRejectsDuplicateIdentity(t *testing.T) {
	identity := "spiffe://agentserver.local/ns/agentserver/sa/sandbox-gateway"
	if _, err := NewSPIFFEWorkloadAuthorizer(identity, identity); err == nil {
		t.Fatal("NewSPIFFEWorkloadAuthorizer() accepted a duplicate identity")
	}
}

func requestWithVerifiedURI(t *testing.T, raw string) *http.Request {
	t.Helper()
	identity, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Request{TLS: &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{URIs: []*url.URL{identity}}}}}}
}
