package main

import (
	"crypto/tls"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coreserver"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/devstack"
	"github.com/agentserver/agentserver/v2/internal/devstacktest"
)

func TestCoreExternalTLSKeepsInternalCertificateAuthentication(t *testing.T) {
	fixture, err := devstacktest.Prepare(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env, err := devstack.ReadEnvironmentFile(fixture.Prepared.EnvironmentFiles["agentserver-core"])
	if err != nil {
		t.Fatal(err)
	}
	for _, external := range []bool{false, true} {
		cfg, err := coreTLSConfigWithExternal(env[coreTLSCertificateEnvironment], env[coreTLSKeyEnvironment], env[coreClientCAEnvironment], external)
		if err != nil {
			t.Fatal(err)
		}
		want := tls.RequireAndVerifyClientCert
		if external {
			want = tls.VerifyClientCertIfGiven
		}
		if cfg.ClientAuth != want || cfg.ClientCAs == nil || cfg.MinVersion != tls.VersionTLS13 {
			t.Fatalf("external=%v lost client identity verification", external)
		}
	}
}

func TestCoreExternalAndInternalAuthenticationOverTLS(t *testing.T) {
	fixture, err := devstacktest.Prepare(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env, err := devstack.ReadEnvironmentFile(fixture.Prepared.EnvironmentFiles["agentserver-core"])
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := coreTLSConfigWithExternal(env[coreTLSCertificateEnvironment], env[coreTLSKeyEnvironment], env[coreClientCAEnvironment], true)
	if err != nil {
		t.Fatal(err)
	}
	identity := cfg.Certificates[0].Leaf.URIs[0].String()
	// devstacktest deliberately issues fixture certificates at a fixed date.
	fixtureNow := cfg.Certificates[0].Leaf.NotBefore.Add(time.Hour)
	cfg.Time = func() time.Time { return fixtureNow }
	internal, err := coreserver.NewSPIFFEWorkloadAuthorizer(identity)
	if err != nil {
		t.Fatal(err)
	}
	external, err := coreserver.NewSPIFFEWorkloadAuthorizerWithExternalToken("test-capability", identity)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorizer := internal
		if r.URL.Path == corecontract.ListManagedSandboxesForReconcilePath {
			authorizer = external
		}
		if err := authorizer.AuthorizeWorkload(r, "test"); err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	server.TLS = cfg
	server.StartTLS()
	defer server.Close()
	for _, tc := range []struct {
		name, path, token string
		certificate       bool
		want              int
	}{
		{"internal-mtls", "/internal-only", "", true, 204},
		{"external-token", corecontract.ListManagedSandboxesForReconcilePath, "test-capability", false, 204},
		{"external-anonymous", corecontract.ListManagedSandboxesForReconcilePath, "", false, 403},
		{"external-wrong-token", corecontract.ListManagedSandboxesForReconcilePath, "wrong", false, 403},
		{"token-not-internal", "/internal-only", "test-capability", false, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientTLS := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: cfg.ClientCAs, Time: cfg.Time}
			if tc.certificate {
				clientTLS.Certificates = cfg.Certificates
			}
			transport := &http.Transport{TLSClientConfig: clientTLS}
			defer transport.CloseIdleConnections()
			r, _ := http.NewRequestWithContext(t.Context(), "POST", server.URL+tc.path, nil)
			if tc.token != "" {
				r.Header.Set("X-AgentServer-Managed-Sandbox-Token", tc.token)
			}
			response, err := (&http.Client{Transport: transport}).Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.want {
				t.Fatalf("got %d want %d", response.StatusCode, tc.want)
			}
		})
	}
}
