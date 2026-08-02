package main

import (
	"path/filepath"
	"testing"
)

func TestLoadLLMProxyConfig(t *testing.T) {
	values := validLLMProxyConfiguration(t)
	config, err := loadLLMProxyConfig(func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != "127.0.0.1:0" || config.coreServerName != "core.agentserver.test" {
		t.Fatalf("loaded llmproxy config = %+v", config)
	}
}

func TestLoadLLMProxyConfigRejectsUnsafeValues(t *testing.T) {
	for name, mutation := range map[string]func(map[string]string){
		"missing": func(values map[string]string) { delete(values, llmProxyCapabilityIssuerEnvironment) },
		"listen":  func(values map[string]string) { values[llmProxyListenAddressEnvironment] = "not-an-address" },
		"core cleartext": func(values map[string]string) {
			values[llmProxyCoreURLEnvironment] = "http://core.agentserver.test"
		},
		"core path": func(values map[string]string) {
			values[llmProxyCoreURLEnvironment] = "https://core.agentserver.test/internal"
		},
		"SPIFFE": func(values map[string]string) {
			values[llmProxySPIFFEIdentityEnvironment] = "https://identity.example.test/llmproxy"
		},
		"relative keyring": func(values map[string]string) { values[llmProxyCapabilityKeyringEnvironment] = "keyring" },
		"whitespace": func(values map[string]string) {
			values[llmProxyCapabilityIssuerEnvironment] = " https://core.agentserver.test"
		},
	} {
		t.Run(name, func(t *testing.T) {
			values := validLLMProxyConfiguration(t)
			mutation(values)
			if _, err := loadLLMProxyConfig(func(name string) string { return values[name] }); err == nil {
				t.Fatal("unsafe llmproxy configuration was accepted")
			}
		})
	}
}

func validLLMProxyConfiguration(t *testing.T) map[string]string {
	t.Helper()
	root := t.TempDir()
	path := func(name string) string { return filepath.Join(root, name) }
	return map[string]string{
		llmProxyListenAddressEnvironment:     "127.0.0.1:0",
		llmProxyTLSCertificateEnvironment:    path("llmproxy.crt"),
		llmProxyTLSKeyEnvironment:            path("llmproxy.key"),
		llmProxySPIFFEIdentityEnvironment:    "spiffe://agentserver.test/ns/agentserver/sa/llmproxy",
		llmProxyCoreURLEnvironment:           "https://core.agentserver.test",
		llmProxyCoreCAEnvironment:            path("core-ca.pem"),
		llmProxyCoreServerNameEnvironment:    "core.agentserver.test",
		llmProxyCapabilityIssuerEnvironment:  "https://core.agentserver.test",
		llmProxyCapabilityKeyringEnvironment: path("capability-keyring.json"),
	}
}
