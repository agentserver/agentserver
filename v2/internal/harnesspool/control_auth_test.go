package harnesspool

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"
)

const testWorkerTLSIdentity = "spiffe://agentserver.local/ns/agentserver/sa/harness-worker"

func TestControlBearerCapabilityIsCanonicalAndSingular(t *testing.T) {
	token := fixedControlCapability(1)
	request, err := http.NewRequest(http.MethodGet, "https://pool.example/control", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if got, err := bearerCapability(request); err != nil || got != token {
		t.Fatalf("bearerCapability() = %q, %v", got, err)
	}

	tests := []string{
		"",
		"bearer " + token,
		"Bearer  " + token,
		"Bearer " + token + " ",
		"Bearer short",
		"Bearer " + token + ",other",
	}
	for _, authorization := range tests {
		request.Header = make(http.Header)
		if authorization != "" {
			request.Header.Add("Authorization", authorization)
		}
		if _, err := bearerCapability(request); err == nil {
			t.Fatalf("unsafe Authorization %q was accepted", authorization)
		}
	}
	request.Header = make(http.Header)
	request.Header.Add("Authorization", "Bearer "+token)
	request.Header.Add("Authorization", "Bearer "+token)
	if _, err := bearerCapability(request); err == nil {
		t.Fatal("duplicate Authorization values were accepted")
	}
}

func TestControlWorkerAuthenticationRequiresExactVerifiedSPIFFELeaf(t *testing.T) {
	identity, err := url.Parse(testWorkerTLSIdentity)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{URIs: []*url.URL{identity}}
	request := &http.Request{TLS: &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}}
	if err := authenticateHarnessWorker(request, testWorkerTLSIdentity); err != nil {
		t.Fatal(err)
	}

	request.TLS.VerifiedChains = nil
	if err := authenticateHarnessWorker(request, testWorkerTLSIdentity); err == nil {
		t.Fatal("unverified client certificate was accepted")
	}
	other, _ := url.Parse("spiffe://agentserver.local/ns/agentserver/sa/other")
	request.TLS.VerifiedChains = [][]*x509.Certificate{{{URIs: []*url.URL{other}}}}
	if err := authenticateHarnessWorker(request, testWorkerTLSIdentity); err == nil {
		t.Fatal("wrong worker identity was accepted")
	}
	request.TLS.VerifiedChains = [][]*x509.Certificate{{{URIs: []*url.URL{identity, other}}}}
	if err := authenticateHarnessWorker(request, testWorkerTLSIdentity); err == nil {
		t.Fatal("ambiguous worker identities were accepted")
	}
	request.TLS.VerifiedChains = [][]*x509.Certificate{
		{{Raw: []byte("leaf-a"), URIs: []*url.URL{identity}}},
		{{Raw: []byte("leaf-b"), URIs: []*url.URL{identity}}},
	}
	if err := authenticateHarnessWorker(request, testWorkerTLSIdentity); err == nil {
		t.Fatal("verified chains with different leaves were accepted")
	}
}

func fixedControlCapability(fill byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, controlCapabilityBytes))
}
