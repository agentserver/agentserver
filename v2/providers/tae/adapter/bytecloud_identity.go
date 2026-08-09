package adapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"code.byted.org/paas/cloud-sdk-go/aksk"
	cloudjwt "code.byted.org/paas/cloud-sdk-go/jwt"
)

const (
	// ByteCloudSiteI18NTT is the site used by the pinned SG TAE control plane.
	// The location of the workload (SG) is not itself a ByteCloud site; this
	// value describes the I18N-TT control plane selected by the TAE region.
	ByteCloudSiteI18NTT = aksk.SiteI18NTT
	// ByteCloudJWTEndpointSG is the only application-JWT exchange origin used
	// by the SG provider. The generic I18N SDK host set includes cross-region
	// endpoints that are not reachable from SG production.
	ByteCloudJWTEndpointSG            = "https://cloud-i18n-sg.bytedance.net"
	defaultByteCloudJWTRequestTimeout = 5 * time.Second
	maxByteCloudCredentialBytes       = 4096
	maxByteCloudJWTBytes              = 64 * 1024
	byteCloudJWTRefreshSkew           = 30 * time.Second
)

// AKSKJWTGenerator is the small part of the official ByteCloud Auth SDK used
// by the provider. Keeping this interface local makes token exchange fully
// testable without a live credential and prevents SDK types from crossing the
// provider-neutral gateway contract.
type AKSKJWTGenerator interface {
	GenerateWithAKSK(context.Context, aksk.Credential, string, ...string) (string, error)
	GenerateWithAKSKWithoutCache(context.Context, aksk.Credential, string, ...string) (string, error)
}

type ByteCloudJWTHeaderSourceConfig struct {
	AccessKeyID     string
	SecretAccessKey string
	Site            string
	JWTEndpoint     string
	ProxyURL        string
	RequestTimeout  time.Duration
	Generator       AKSKJWTGenerator
}

// ByteCloudJWTHeaderSource obtains a short-lived application JWT on demand.
// Normal requests use the official SDK's AK+site cache and singleflight
// refresh. ForceRefresh is reserved for an authentication 401 or an
// operator-triggered readiness check; its result is held as a bounded,
// expiry-aware override so it is effective even though the SDK intentionally
// does not expose cache invalidation.
type ByteCloudJWTHeaderSource struct {
	credential     aksk.Credential
	site           string
	jwtEndpoint    string
	generator      AKSKJWTGenerator
	requestTimeout time.Duration
	attemptTimeout time.Duration

	mu           sync.RWMutex
	refreshMu    sync.Mutex
	forcedToken  string
	forcedExpiry time.Time
}

func NewByteCloudJWTHeaderSource(config ByteCloudJWTHeaderSourceConfig) (*ByteCloudJWTHeaderSource, error) {
	if err := validateByteCloudCredential(config.AccessKeyID, config.SecretAccessKey); err != nil {
		return nil, err
	}
	if config.Site != ByteCloudSiteI18NTT {
		return nil, fmt.Errorf("ByteCloud application JWT site must be exactly %s for SG", ByteCloudSiteI18NTT)
	}
	if config.JWTEndpoint != ByteCloudJWTEndpointSG {
		return nil, fmt.Errorf("ByteCloud application JWT endpoint must be exactly %s for SG", ByteCloudJWTEndpointSG)
	}
	if config.ProxyURL != TAEProxyURLSG {
		return nil, fmt.Errorf("ByteCloud application JWT proxy must be exactly %s for SG", TAEProxyURLSG)
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = defaultByteCloudJWTRequestTimeout
	}
	if config.RequestTimeout < time.Second || config.RequestTimeout > 30*time.Second {
		return nil, errors.New("ByteCloud JWT request timeout must be between one second and 30 seconds")
	}
	generator := config.Generator
	attemptTimeout := config.RequestTimeout / 2
	if generator == nil {
		requestTimeout := attemptTimeout
		generator = cloudjwt.NewAKSKGenerator(
			cloudjwt.WithReqTimeout(&requestTimeout),
			// Agent traffic must be identified as such by the official SDK.
			cloudjwt.WithAgentUse(true),
			// Keep proxying scoped to the JWT exchange. TAE control/data-plane
			// clients deliberately retain their direct, separately audited paths.
			cloudjwt.WithProxyURL(config.ProxyURL),
		)
	}
	return &ByteCloudJWTHeaderSource{
		credential: aksk.Credential{AccessKeyID: config.AccessKeyID, SecretAccessKey: config.SecretAccessKey},
		site:       config.Site, jwtEndpoint: config.JWTEndpoint, generator: generator,
		requestTimeout: config.RequestTimeout, attemptTimeout: attemptTimeout,
	}, nil
}

func (source *ByteCloudJWTHeaderSource) Headers(ctx context.Context) (http.Header, error) {
	if source == nil || source.generator == nil {
		return nil, errors.New("ByteCloud application JWT generator is unavailable")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, errors.New("ByteCloud application JWT exchange was canceled")
		}
	}
	if token := source.forcedTokenIfLive(time.Now()); token != "" {
		return jwtHeader(token), nil
	}
	token, err := source.exchangeJWT(ctx, false)
	if err != nil {
		return nil, errors.New("ByteCloud application JWT exchange failed")
	}
	if err := validateByteCloudJWT(token); err != nil {
		return nil, err
	}
	return jwtHeader(token), nil
}

// ForceRefresh bypasses the SDK cache once and returns the newly exchanged
// identity header. The token itself is never included in an error or log.
func (source *ByteCloudJWTHeaderSource) ForceRefresh(ctx context.Context) (http.Header, error) {
	if source == nil || source.generator == nil {
		return nil, errors.New("ByteCloud application JWT generator is unavailable")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, errors.New("ByteCloud application JWT force refresh was canceled")
		}
	}
	// The official SDK deliberately exposes the cache-bypassing operation for
	// operators, but does not singleflight it. Serialize that exceptional path
	// so two concurrent readiness/401 recoveries cannot overwrite a newer
	// forced token with an older response.
	source.refreshMu.Lock()
	defer source.refreshMu.Unlock()
	return source.forceRefreshLocked(ctx)
}

// refreshRejectedIdentity is called only after TAE has definitively rejected
// an injected JWT with HTTP 401. It refreshes identity for a later operation;
// the rejected operation is deliberately never replayed here. Concurrent 401s
// for the same old token collapse behind the first successful forced token.
func (source *ByteCloudJWTHeaderSource) refreshRejectedIdentity(ctx context.Context, rejectedToken string) error {
	if source == nil || source.generator == nil {
		return errors.New("ByteCloud application JWT generator is unavailable")
	}
	if err := validateByteCloudJWT(rejectedToken); err != nil {
		return errors.New("rejected ByteCloud application JWT is invalid")
	}
	source.refreshMu.Lock()
	defer source.refreshMu.Unlock()
	if current := source.forcedTokenIfLive(time.Now()); current != "" && current != rejectedToken {
		return nil
	}
	_, err := source.forceRefreshLocked(ctx)
	return err
}

// forceRefreshLocked requires refreshMu to be held by the caller.
func (source *ByteCloudJWTHeaderSource) forceRefreshLocked(ctx context.Context) (http.Header, error) {
	token, err := source.exchangeJWT(ctx, true)
	if err != nil {
		return nil, errors.New("ByteCloud application JWT force refresh failed")
	}
	if err := validateByteCloudJWT(token); err != nil {
		return nil, err
	}
	expiresAt, hasExpiry := byteCloudJWTExpiry(token)
	source.mu.Lock()
	if hasExpiry {
		source.forcedToken = token
		source.forcedExpiry = expiresAt
	} else {
		// Do not retain an opaque token whose expiry cannot be checked.
		source.forcedToken = ""
		source.forcedExpiry = time.Time{}
	}
	source.mu.Unlock()
	return jwtHeader(token), nil
}

// exchangeJWT makes two bounded attempts against the same pinned SG origin.
// The operation is a signed GET and is safe to retry; this policy must not be
// generalized to TAE create/process operations. The parent timeout remains the
// total exchange budget, while each attempt receives half of that budget.
func (source *ByteCloudJWTHeaderSource) exchangeJWT(ctx context.Context, bypassCache bool) (string, error) {
	exchangeContext, cancelExchange := source.exchangeContext(ctx)
	defer cancelExchange()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		attemptContext, cancelAttempt := context.WithTimeout(exchangeContext, source.attemptTimeout)
		var token string
		if bypassCache {
			token, lastErr = source.generator.GenerateWithAKSKWithoutCache(
				attemptContext, source.credential, source.site, source.jwtEndpoint,
			)
		} else {
			token, lastErr = source.generator.GenerateWithAKSK(
				attemptContext, source.credential, source.site, source.jwtEndpoint,
			)
		}
		cancelAttempt()
		if lastErr == nil {
			return token, nil
		}
		if exchangeContext.Err() != nil {
			break
		}
	}
	return "", lastErr
}

func (source *ByteCloudJWTHeaderSource) exchangeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if source.requestTimeout <= 0 {
		return context.WithTimeout(ctx, defaultByteCloudJWTRequestTimeout)
	}
	return context.WithTimeout(ctx, source.requestTimeout)
}

func (source *ByteCloudJWTHeaderSource) forcedTokenIfLive(now time.Time) string {
	source.mu.RLock()
	token, expiry := source.forcedToken, source.forcedExpiry
	source.mu.RUnlock()
	if token == "" || !expiry.After(now.Add(byteCloudJWTRefreshSkew)) {
		if token != "" {
			source.mu.Lock()
			if source.forcedToken == token && !source.forcedExpiry.After(now.Add(byteCloudJWTRefreshSkew)) {
				source.forcedToken = ""
				source.forcedExpiry = time.Time{}
			}
			source.mu.Unlock()
		}
		return ""
	}
	return token
}

func jwtHeader(token string) http.Header {
	return http.Header{"X-Jwt-Token": []string{token}}
}

func validateByteCloudCredential(accessKeyID, secretAccessKey string) error {
	if len(accessKeyID) > maxByteCloudCredentialBytes || strings.TrimSpace(accessKeyID) != accessKeyID ||
		strings.ContainsAny(accessKeyID, "\r\n\x00") {
		return errors.New("ByteCloud application access key is invalid")
	}
	if err := aksk.ValidateAccessKeyID(accessKeyID); err != nil {
		return errors.New("ByteCloud application access key is invalid")
	}
	if secretAccessKey == "" || len(secretAccessKey) > maxByteCloudCredentialBytes ||
		strings.TrimSpace(secretAccessKey) != secretAccessKey || strings.ContainsAny(secretAccessKey, "\r\n\x00") {
		return errors.New("ByteCloud application secret key is invalid")
	}
	return nil
}

func validateByteCloudJWT(token string) error {
	if token == "" || len(token) > maxByteCloudJWTBytes || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n\x00") {
		return errors.New("ByteCloud application JWT is invalid")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("ByteCloud application JWT is not a compact JWT")
	}
	for _, part := range parts {
		if part == "" {
			return errors.New("ByteCloud application JWT contains an empty segment")
		}
		for _, character := range part {
			if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
				!(character >= '0' && character <= '9') && character != '-' && character != '_' {
				return errors.New("ByteCloud application JWT contains an unsafe character")
			}
		}
	}
	return nil
}

func byteCloudJWTExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		ExpiresAt json.Number `json:"exp"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&claims); err != nil {
		return time.Time{}, false
	}
	seconds, err := claims.ExpiresAt.Int64()
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0), true
}

var _ HeaderSource = (*ByteCloudJWTHeaderSource)(nil)
