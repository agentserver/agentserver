package adapter

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"code.byted.org/paas/cloud-sdk-go/aksk"
)

type fakeAKSKGenerator struct {
	mu             sync.Mutex
	normalTokens   []string
	forcedTokens   []string
	normalCalls    int
	forcedCalls    int
	normalFailures int
	forcedFailures int
	lastCredential aksk.Credential
	lastSite       string
	lastHost       string
	err            error
}

func (generator *fakeAKSKGenerator) GenerateWithAKSK(_ context.Context, credential aksk.Credential, site string, hosts ...string) (string, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	generator.normalCalls++
	generator.lastCredential, generator.lastSite = credential, site
	if len(hosts) == 1 {
		generator.lastHost = hosts[0]
	} else {
		generator.lastHost = ""
	}
	if generator.err != nil {
		return "", generator.err
	}
	if generator.normalFailures > 0 {
		generator.normalFailures--
		return "", errors.New("transient normal exchange failure")
	}
	if len(generator.normalTokens) == 0 {
		return "", errors.New("no normal token")
	}
	token := generator.normalTokens[0]
	generator.normalTokens = generator.normalTokens[1:]
	return token, nil
}

func (generator *fakeAKSKGenerator) GenerateWithAKSKWithoutCache(_ context.Context, credential aksk.Credential, site string, hosts ...string) (string, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	generator.forcedCalls++
	generator.lastCredential, generator.lastSite = credential, site
	if len(hosts) == 1 {
		generator.lastHost = hosts[0]
	} else {
		generator.lastHost = ""
	}
	if generator.err != nil {
		return "", generator.err
	}
	if generator.forcedFailures > 0 {
		generator.forcedFailures--
		return "", errors.New("transient forced exchange failure")
	}
	if len(generator.forcedTokens) == 0 {
		return "", errors.New("no forced token")
	}
	token := generator.forcedTokens[0]
	generator.forcedTokens = generator.forcedTokens[1:]
	return token, nil
}

func TestNewByteCloudJWTHeaderSourceValidatesApplicationIdentity(t *testing.T) {
	cases := []struct {
		name     string
		access   string
		secret   string
		site     string
		endpoint string
		timeout  time.Duration
	}{
		{name: "empty access key", access: "", secret: "SK-secret", site: ByteCloudSiteI18NTT, endpoint: ByteCloudJWTEndpointSG},
		{name: "access key delimiter", access: "AK/secret", secret: "SK-secret", site: ByteCloudSiteI18NTT, endpoint: ByteCloudJWTEndpointSG},
		{name: "secret newline", access: "AK-example", secret: "SK-secret\n", site: ByteCloudSiteI18NTT, endpoint: ByteCloudJWTEndpointSG},
		{name: "padded site", access: "AK-example", secret: "SK-secret", site: " " + ByteCloudSiteI18NTT, endpoint: ByteCloudJWTEndpointSG},
		{name: "unsupported site", access: "AK-example", secret: "SK-secret", site: "cn-sg", endpoint: ByteCloudJWTEndpointSG},
		{name: "missing endpoint", access: "AK-example", secret: "SK-secret", site: ByteCloudSiteI18NTT},
		{name: "cross-region endpoint", access: "AK-example", secret: "SK-secret", site: ByteCloudSiteI18NTT, endpoint: "https://cloud-i18n.bytedance.net"},
		{name: "short timeout", access: "AK-example", secret: "SK-secret", site: ByteCloudSiteI18NTT, endpoint: ByteCloudJWTEndpointSG, timeout: time.Millisecond},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewByteCloudJWTHeaderSource(ByteCloudJWTHeaderSourceConfig{
				AccessKeyID: testCase.access, SecretAccessKey: testCase.secret, Site: testCase.site,
				JWTEndpoint: testCase.endpoint, ProxyURL: TAEProxyURLSG,
				RequestTimeout: testCase.timeout, Generator: &fakeAKSKGenerator{},
			})
			if err == nil {
				t.Fatal("invalid application identity was accepted")
			}
			if strings.Contains(err.Error(), testCase.secret) {
				t.Fatalf("secret leaked in constructor error: %v", err)
			}
		})
	}
	for _, proxyURL := range []string{
		"",
		"socks5://ssh-egress-merlin-i18nbd-syd2a-83092-headless.ssh-egress.svc.cluster.local:1080",
		"socks5h://ssh-egress-merlin-i18nbd-useast14a-83093.ssh-egress.svc.cluster.local:1080",
	} {
		_, err := NewByteCloudJWTHeaderSource(ByteCloudJWTHeaderSourceConfig{
			AccessKeyID: "AK-example", SecretAccessKey: "SK-secret", Site: ByteCloudSiteI18NTT,
			JWTEndpoint: ByteCloudJWTEndpointSG, ProxyURL: proxyURL, Generator: &fakeAKSKGenerator{},
		})
		if err == nil {
			t.Fatalf("unsafe ByteCloud JWT proxy %q was accepted", proxyURL)
		}
	}
}

func TestByteCloudJWTHeaderSourceUsesForceRefreshOverride(t *testing.T) {
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
	header, err := source.Headers(t.Context())
	if err != nil || header.Get("X-Jwt-Token") != normal {
		t.Fatalf("normal header = %#v, %v", header, err)
	}
	if _, err := source.ForceRefresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	header, err = source.Headers(t.Context())
	if err != nil || header.Get("X-Jwt-Token") != forced {
		t.Fatalf("forced header = %#v, %v", header, err)
	}
	generator.mu.Lock()
	normalCalls, forcedCalls, credential, site, host := generator.normalCalls, generator.forcedCalls, generator.lastCredential, generator.lastSite, generator.lastHost
	generator.mu.Unlock()
	if normalCalls != 1 || forcedCalls != 1 || credential.AccessKeyID != "AK-example" || credential.SecretAccessKey != "SK-secret" ||
		site != ByteCloudSiteI18NTT || host != ByteCloudJWTEndpointSG {
		t.Fatalf("generator calls/credential/site/host = %d/%d/%+v/%q/%q", normalCalls, forcedCalls, credential, site, host)
	}
}

func TestByteCloudJWTHeaderSourceRetriesIdempotentSGExchangeOnce(t *testing.T) {
	token := testCompactJWT(time.Now().Add(10 * time.Minute))
	generator := &fakeAKSKGenerator{normalFailures: 1, normalTokens: []string{token}}
	source, err := NewByteCloudJWTHeaderSource(ByteCloudJWTHeaderSourceConfig{
		AccessKeyID: "AK-example", SecretAccessKey: "SK-secret", Site: ByteCloudSiteI18NTT,
		JWTEndpoint: ByteCloudJWTEndpointSG, ProxyURL: TAEProxyURLSG,
		RequestTimeout: 2 * time.Second, Generator: generator,
	})
	if err != nil {
		t.Fatal(err)
	}
	header, err := source.Headers(t.Context())
	if err != nil || header.Get("X-Jwt-Token") != token {
		t.Fatalf("retried exchange = %#v, %v", header, err)
	}
	generator.mu.Lock()
	normalCalls, host := generator.normalCalls, generator.lastHost
	generator.mu.Unlock()
	if normalCalls != 2 || host != ByteCloudJWTEndpointSG {
		t.Fatalf("exchange calls/host = %d/%q", normalCalls, host)
	}
}

func TestByteCloudJWTHeaderSourceRejectsUnsafeJWTWithoutLeakingIt(t *testing.T) {
	unsafe := "header.payload.signature\r\nsecret"
	generator := &fakeAKSKGenerator{normalTokens: []string{unsafe}}
	source, err := NewByteCloudJWTHeaderSource(ByteCloudJWTHeaderSourceConfig{
		AccessKeyID: "AK-example", SecretAccessKey: "SK-secret", Site: ByteCloudSiteI18NTT,
		JWTEndpoint: ByteCloudJWTEndpointSG, ProxyURL: TAEProxyURLSG, Generator: generator,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Headers(t.Context())
	if err == nil || strings.Contains(err.Error(), unsafe) || strings.Contains(err.Error(), "SK-secret") {
		t.Fatalf("unsafe JWT result = %v", err)
	}
}

func TestByteCloudJWTHeaderSourceDoesNotRetainOpaqueForceRefreshToken(t *testing.T) {
	opaque := "header.payload.signature"
	normal := testCompactJWT(time.Now().Add(10 * time.Minute))
	generator := &fakeAKSKGenerator{normalTokens: []string{normal}, forcedTokens: []string{opaque}}
	source, err := NewByteCloudJWTHeaderSource(ByteCloudJWTHeaderSourceConfig{
		AccessKeyID: "AK-example", SecretAccessKey: "SK-secret", Site: ByteCloudSiteI18NTT,
		JWTEndpoint: ByteCloudJWTEndpointSG, ProxyURL: TAEProxyURLSG, Generator: generator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.ForceRefresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	header, err := source.Headers(t.Context())
	if err != nil || header.Get("X-Jwt-Token") != normal {
		t.Fatalf("opaque override header = %#v, %v", header, err)
	}
}

func TestByteCloudJWTHeaderSourceGeneratorFailureIsRedacted(t *testing.T) {
	secret := "SK-super-secret-value"
	generator := &fakeAKSKGenerator{err: errors.New("exchange failed for " + secret)}
	source, err := NewByteCloudJWTHeaderSource(ByteCloudJWTHeaderSourceConfig{
		AccessKeyID: "AK-example", SecretAccessKey: secret, Site: ByteCloudSiteI18NTT,
		JWTEndpoint: ByteCloudJWTEndpointSG, ProxyURL: TAEProxyURLSG, Generator: generator,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Headers(t.Context())
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "exchange failed") {
		t.Fatalf("redacted generator error = %v", err)
	}
}

func TestByteCloudJWTHeaderSourceCollapsesRepeatedRejectedTokenRefresh(t *testing.T) {
	rejected := testCompactJWT(time.Now().Add(10 * time.Minute))
	forced := testCompactJWT(time.Now().Add(20 * time.Minute))
	generator := &fakeAKSKGenerator{forcedTokens: []string{forced}}
	source, err := NewByteCloudJWTHeaderSource(ByteCloudJWTHeaderSourceConfig{
		AccessKeyID: "AK-example", SecretAccessKey: "SK-secret", Site: ByteCloudSiteI18NTT,
		JWTEndpoint: ByteCloudJWTEndpointSG, ProxyURL: TAEProxyURLSG, Generator: generator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.refreshRejectedIdentity(t.Context(), rejected); err != nil {
		t.Fatal(err)
	}
	if err := source.refreshRejectedIdentity(t.Context(), rejected); err != nil {
		t.Fatal(err)
	}
	generator.mu.Lock()
	forcedCalls := generator.forcedCalls
	generator.mu.Unlock()
	if forcedCalls != 1 {
		t.Fatalf("forced refresh calls = %d, want 1", forcedCalls)
	}
}

func testCompactJWT(expiry time.Time) string {
	encode := func(value string) string { return base64.RawURLEncoding.EncodeToString([]byte(value)) }
	return encode(`{"alg":"HS256","typ":"JWT"}`) + "." + encode(`{"exp":`+strconv.FormatInt(expiry.Unix(), 10)+`}`) + ".signature"
}
