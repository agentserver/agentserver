package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/providers/tae/adapter"
)

func TestRunAcceptsOnlyProductionServe(t *testing.T) {
	called := false
	var stdout, stderr bytes.Buffer
	exit := run(t.Context(), []string{"serve"}, func(string) string { return "value" }, &stdout, &stderr,
		func(_ context.Context, getenv func(string) string, output, _ io.Writer) error {
			called = true
			if getenv("x") != "value" {
				t.Fatal("getenv was not forwarded")
			}
			_, _ = io.WriteString(output, "started\n")
			return nil
		}, nil)
	if exit != 0 || !called || stdout.String() != "started\n" || stderr.Len() != 0 {
		t.Fatalf("run = %d called=%v stdout=%q stderr=%q", exit, called, stdout.String(), stderr.String())
	}
	for _, args := range [][]string{{}, {"serve", "--insecure-dev"}, {"unknown"}} {
		stderr.Reset()
		if exit := run(t.Context(), args, func(string) string { return "" }, io.Discard, &stderr, nil, nil); exit != 2 || !strings.Contains(stderr.String(), "tae-sandbox-gateway serve") {
			t.Fatalf("run(%v) = %d %q", args, exit, stderr.String())
		}
	}
}

func TestLoadProviderConfigFailsClosedOnUnsafeRuntime(t *testing.T) {
	for name, environment := range map[string]map[string]string{
		"missing-auth-mode":    {authModeEnvironment: ""},
		"wrong-auth-mode":      {authModeEnvironment: "zti"},
		"padded-auth-mode":     {authModeEnvironment: " " + byteCloudAppAKSKAuthMode},
		"wrong-bytecloud-site": {authModeEnvironment: byteCloudAppAKSKAuthMode, byteCloudSiteEnvironment: "cn"},
		"missing-jwt-endpoint": {byteCloudJWTEndpointEnvironment: ""},
		"wrong-jwt-endpoint":   {byteCloudJWTEndpointEnvironment: "https://cloud-i18n.bytedance.net"},
		"missing-tae-proxy":    {taeProxyEnvironment: ""},
		"wrong-tae-proxy":      {taeProxyEnvironment: "socks5h://ssh-egress.example:1080"},
		"tls-bypass":           {unsafeTLSBypassEnvironment: "1"},
		"proxy-bypass":         {"HTTPS_PROXY": "http://proxy.internal:8080"},
		"relative-access-key":  {byteCloudAccessKeyEnvironment: "material/access-key"},
		"bad-jwt-timeout":      {byteCloudJWTTimeoutEnvironment: "31s"},
		"bad-timeout":          {controlTimeoutEnvironment: "5m"},
		"too-many-reconnects":  {reconnectAttemptsEnvironment: "100"},
		"unbounded-file":       {maxReadBytesEnvironment: "999999999"},
		"digest-image":         {sandboxImageEnvironment: "registry.example/sandbox@sha256:" + strings.Repeat("a", 64)},
		"mutable-image":        {sandboxImageEnvironment: "registry.example/sandbox:latest"},
	} {
		t.Run(name, func(t *testing.T) {
			values := validProviderEnvironment()
			for key, value := range environment {
				values[key] = value
			}
			if _, err := loadProviderConfig(func(key string) string { return values[key] }); err == nil {
				t.Fatal("unsafe provider configuration was accepted")
			}
		})
	}
}

func TestLoadProviderConfigDefaultsAreBounded(t *testing.T) {
	values := validProviderEnvironment()
	config, err := loadProviderConfig(func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.controlTimeout != 45*time.Second || config.headerTimeout != 15*time.Second ||
		config.streamGrace != 30*time.Second || config.reconnectAttempts != 2 ||
		config.signalTimeout != 3*time.Second || config.maxReadBytes != 8*1024*1024 || config.sandboxImage == "" {
		t.Fatalf("defaults = %+v", config)
	}
	if config.authMode != byteCloudAppAKSKAuthMode || config.byteCloudSite != adapter.ByteCloudSiteI18NTT ||
		config.accessKeyFile == "" || config.secretKeyFile == "" || config.jwtEndpoint != adapter.ByteCloudJWTEndpointSG ||
		config.proxyURL != adapter.TAEProxyURLSG || config.jwtRequestTimeout != 5*time.Second {
		t.Fatalf("application identity defaults = %+v", config)
	}
}

func validProviderEnvironment() map[string]string {
	return map[string]string{
		sandboxImageEnvironment:         "registry-sg.byted.cs.ac.cn/agentserver/managed-sandbox:sha256-" + strings.Repeat("a", 64),
		authModeEnvironment:             byteCloudAppAKSKAuthMode,
		byteCloudSiteEnvironment:        adapter.ByteCloudSiteI18NTT,
		byteCloudJWTEndpointEnvironment: adapter.ByteCloudJWTEndpointSG,
		taeProxyEnvironment:             adapter.TAEProxyURLSG,
		byteCloudAccessKeyEnvironment:   "/var/run/agentserver/material/bytecloud-access-key-id",
		byteCloudSecretEnvironment:      "/var/run/agentserver/material/bytecloud-secret-access-key",
	}
}

func TestRunReportsServeFailure(t *testing.T) {
	var stderr bytes.Buffer
	exit := run(t.Context(), []string{"serve"}, func(string) string { return "" }, io.Discard, &stderr,
		func(context.Context, func(string) string, io.Writer, io.Writer) error { return errors.New("boom") }, nil)
	if exit != 1 || !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("run = %d stderr=%q", exit, stderr.String())
	}
}

func TestRunProbeReturnsReportStatusWithoutAddingDiagnostics(t *testing.T) {
	for _, testCase := range []struct {
		passed bool
		want   int
	}{{passed: true, want: 0}, {passed: false, want: 1}} {
		var stdout, stderr bytes.Buffer
		exit := run(t.Context(), []string{"probe-network"}, func(string) string { return "value" }, &stdout, &stderr,
			nil, func(_ context.Context, getenv func(string) string, output io.Writer) (bool, error) {
				if getenv("x") != "value" {
					t.Fatal("getenv was not forwarded")
				}
				_, _ = io.WriteString(output, "{\"report\":true}\n")
				return testCase.passed, nil
			})
		if exit != testCase.want || stdout.String() != "{\"report\":true}\n" || stderr.Len() != 0 {
			t.Fatalf("passed=%v run=%d stdout=%q stderr=%q", testCase.passed, exit, stdout.String(), stderr.String())
		}
	}
}
