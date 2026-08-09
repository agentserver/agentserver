package corecredentials

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// ByteCloudSiteI18NTT is the BC1 audit site for the SG workspace provider.
	ByteCloudSiteI18NTT = "i18n-tt"
	// ByteCloudJWTEndpointSG is pinned so the Core resolver cannot silently
	// fall back to another region when minting a workspace JWT.
	ByteCloudJWTEndpointSG = "https://cloud-i18n-sg.bytedance.net"

	defaultByteCloudJWTTimeout = 5 * time.Second
	maximumByteCloudAKSKBytes  = 4096
	maximumByteCloudJWTBytes   = 64 * 1024
	maximumByteCloudCacheItems = 256
	byteCloudJWTExpirySkew     = time.Second
	byteCloudJWTPath           = "/auth/api/v1/jwt"
)

// ByteCloudJWTGenerator is the intentionally tiny adapter boundary around
// the BC1 AK/SK -> JWT exchange. The production implementation below follows
// the published BC1 protocol; tests can inject a deterministic generator
// without ever placing a real secret in an HTTP fixture.
type ByteCloudJWTGenerator interface {
	GenerateWithAKSK(context.Context, string, string, string, string) (string, error)
	GenerateWithAKSKWithoutCache(context.Context, string, string, string, string) (string, error)
}

// ByteCloudTokenExchanger turns a workspace AK/SK envelope into a short-lived
// JWT. The normal path uses a bounded per-process cache; ForceRefresh bypasses
// it for an operator/401 recovery path. Neither method returns or includes the
// AK, SK, or JWT in an error string.
type ByteCloudTokenExchanger struct {
	generator ByteCloudJWTGenerator
	timeout   time.Duration
}

func NewByteCloudTokenExchanger(timeout time.Duration, generator ByteCloudJWTGenerator) (*ByteCloudTokenExchanger, error) {
	if timeout == 0 {
		timeout = defaultByteCloudJWTTimeout
	}
	if timeout < time.Second || timeout > 30*time.Second {
		return nil, errors.New("ByteCloud workspace JWT timeout must be between one second and 30 seconds")
	}
	if generator == nil {
		generator = newByteCloudHTTPJWTGenerator(timeout)
	}
	return &ByteCloudTokenExchanger{generator: generator, timeout: timeout}, nil
}

func (exchanger *ByteCloudTokenExchanger) Exchange(ctx context.Context, accessKeyID, secretAccessKey string) (string, time.Time, error) {
	return exchanger.exchange(ctx, accessKeyID, secretAccessKey, false)
}

func (exchanger *ByteCloudTokenExchanger) ForceRefresh(ctx context.Context, accessKeyID, secretAccessKey string) (string, time.Time, error) {
	return exchanger.exchange(ctx, accessKeyID, secretAccessKey, true)
}

func (exchanger *ByteCloudTokenExchanger) exchange(ctx context.Context, accessKeyID, secretAccessKey string, bypassCache bool) (string, time.Time, error) {
	if exchanger == nil || exchanger.generator == nil {
		return "", time.Time{}, errors.New("ByteCloud workspace JWT exchanger is unavailable")
	}
	if err := validateByteCloudAKSK(accessKeyID, secretAccessKey); err != nil {
		return "", time.Time{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, errors.New("ByteCloud workspace JWT exchange was canceled")
	}
	exchangeContext, cancel := context.WithTimeout(ctx, exchanger.timeout)
	defer cancel()
	attemptTimeout := exchanger.timeout / 2
	if attemptTimeout < time.Second {
		attemptTimeout = exchanger.timeout
	}
	var token string
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		attemptContext, cancelAttempt := context.WithTimeout(exchangeContext, attemptTimeout)
		if bypassCache {
			token, lastErr = exchanger.generator.GenerateWithAKSKWithoutCache(
				attemptContext, accessKeyID, secretAccessKey, ByteCloudSiteI18NTT, ByteCloudJWTEndpointSG,
			)
		} else {
			token, lastErr = exchanger.generator.GenerateWithAKSK(
				attemptContext, accessKeyID, secretAccessKey, ByteCloudSiteI18NTT, ByteCloudJWTEndpointSG,
			)
		}
		cancelAttempt()
		if lastErr == nil {
			break
		}
		if exchangeContext.Err() != nil {
			break
		}
	}
	if lastErr != nil {
		return "", time.Time{}, errors.New("ByteCloud workspace JWT exchange failed")
	}
	expiresAt, ok := parseByteCloudJWTExpiry(token)
	if !ok || !expiresAt.After(time.Now().UTC().Add(byteCloudJWTExpirySkew)) {
		return "", time.Time{}, errors.New("ByteCloud workspace JWT is invalid or expiring")
	}
	return token, expiresAt.UTC(), nil
}

func validateByteCloudAKSK(accessKeyID, secretAccessKey string) error {
	if len(accessKeyID) == 0 || len(accessKeyID) > maximumByteCloudAKSKBytes ||
		strings.TrimSpace(accessKeyID) != accessKeyID || strings.ContainsAny(accessKeyID, "\r\n\x00/,") {
		return errors.New("ByteCloud workspace access key is invalid")
	}
	if len(secretAccessKey) == 0 || len(secretAccessKey) > maximumByteCloudAKSKBytes ||
		strings.TrimSpace(secretAccessKey) != secretAccessKey || strings.ContainsAny(secretAccessKey, "\r\n\x00") {
		return errors.New("ByteCloud workspace secret key is invalid")
	}
	return nil
}

func parseByteCloudJWTExpiry(token string) (time.Time, bool) {
	if token == "" || len(token) > maximumByteCloudJWTBytes || strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n\x00") {
		return time.Time{}, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return time.Time{}, false
	}
	for _, part := range parts {
		if _, err := base64.RawURLEncoding.DecodeString(part); err != nil {
			return time.Time{}, false
		}
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
	return time.Unix(seconds, 0).UTC(), true
}

type byteCloudCachedJWT struct {
	token     string
	expiresAt time.Time
}

type byteCloudHTTPJWTGenerator struct {
	client  *http.Client
	timeout time.Duration
	mu      sync.Mutex
	cache   map[[32]byte]byteCloudCachedJWT
}

func newByteCloudHTTPJWTGenerator(timeout time.Duration) *byteCloudHTTPJWTGenerator {
	return &byteCloudHTTPJWTGenerator{
		client:  &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		timeout: timeout, cache: make(map[[32]byte]byteCloudCachedJWT),
	}
}

func (generator *byteCloudHTTPJWTGenerator) GenerateWithAKSK(ctx context.Context, accessKeyID, secretAccessKey, site, endpoint string) (string, error) {
	if generator == nil {
		return "", errors.New("ByteCloud JWT generator is unavailable")
	}
	key := sha256.Sum256([]byte(accessKeyID + "\x00" + secretAccessKey + "\x00" + site + "\x00" + endpoint))
	generator.mu.Lock()
	if cached, ok := generator.cache[key]; ok && cached.expiresAt.After(time.Now().UTC().Add(byteCloudJWTExpirySkew)) {
		generator.mu.Unlock()
		return cached.token, nil
	}
	generator.mu.Unlock()

	// Do not hold the cache mutex across the network exchange. A slow provider
	// must not serialize unrelated workspaces or turn cache pressure into a
	// process-wide outage.
	token, err := generator.exchange(ctx, accessKeyID, secretAccessKey, site, endpoint)
	if err == nil {
		if expiresAt, ok := parseByteCloudJWTExpiry(token); ok {
			generator.mu.Lock()
			generator.pruneExpiredLocked(time.Now().UTC())
			if len(generator.cache) >= maximumByteCloudCacheItems {
				generator.evictSoonestExpiringLocked()
			}
			generator.cache[key] = byteCloudCachedJWT{token: token, expiresAt: expiresAt}
			generator.mu.Unlock()
		}
	}
	return token, err
}

func (generator *byteCloudHTTPJWTGenerator) pruneExpiredLocked(now time.Time) {
	for key, cached := range generator.cache {
		if !cached.expiresAt.After(now.Add(byteCloudJWTExpirySkew)) {
			delete(generator.cache, key)
		}
	}
}

func (generator *byteCloudHTTPJWTGenerator) evictSoonestExpiringLocked() {
	var oldestKey [32]byte
	var oldest time.Time
	for key, cached := range generator.cache {
		if oldest.IsZero() || cached.expiresAt.Before(oldest) {
			oldestKey, oldest = key, cached.expiresAt
		}
	}
	if !oldest.IsZero() {
		delete(generator.cache, oldestKey)
	}
}

func (generator *byteCloudHTTPJWTGenerator) GenerateWithAKSKWithoutCache(ctx context.Context, accessKeyID, secretAccessKey, site, endpoint string) (string, error) {
	if generator == nil {
		return "", errors.New("ByteCloud JWT generator is unavailable")
	}
	return generator.exchange(ctx, accessKeyID, secretAccessKey, site, endpoint)
}

func (generator *byteCloudHTTPJWTGenerator) exchange(ctx context.Context, accessKeyID, secretAccessKey, site, endpoint string) (string, error) {
	if generator == nil || generator.client == nil || site != ByteCloudSiteI18NTT || endpoint != ByteCloudJWTEndpointSG {
		return "", errors.New("ByteCloud JWT exchange configuration is invalid")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("ByteCloud JWT endpoint is invalid")
	}
	query := parsed.Query()
	query.Set("platform_name", "bytecloud_app_svc")
	parsed.Path = byteCloudJWTPath
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", errors.New("construct ByteCloud JWT request")
	}
	nonce, err := newByteCloudNonce()
	if err != nil {
		return "", errors.New("allocate ByteCloud JWT request nonce")
	}
	timestamp := time.Now().UTC().Truncate(time.Second)
	canonicalHeaders := "host:" + parsed.Host + "\n"
	payloadHash := sha256Hex(nil)
	canonicalRequest := strings.Join([]string{http.MethodGet, byteCloudJWTPath, "platform_name=bytecloud_app_svc", canonicalHeaders, "host", payloadHash}, "\n")
	date := timestamp.Format("20060102")
	credentialScope := date + "/" + site + "/bc_request"
	canonicalHash := sha256Hex([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{"BC1-HMAC-SHA256", timestamp.Format("20060102T150405Z"), nonce, credentialScope, canonicalHash}, "\n")
	kDate := byteCloudHMAC([]byte("BC1"+secretAccessKey), []byte(date))
	kSite := byteCloudHMAC(kDate, []byte(site))
	kSigning := byteCloudHMAC(kSite, []byte("bc_request"))
	signature := hex.EncodeToString(byteCloudHMAC(kSigning, []byte(stringToSign)))
	authorization := fmt.Sprintf("BC1-HMAC-SHA256 Credential=%s/%s, Timestamp=%s, Nonce=%s, SignedHeaders=host, Signature=%s",
		accessKeyID, credentialScope, timestamp.Format("20060102T150405Z"), nonce, signature)
	request.Header.Set("Authorization", authorization)
	response, err := generator.client.Do(request)
	if err != nil {
		return "", errors.New("execute ByteCloud JWT request")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 16*1024))
		return "", errors.New("ByteCloud JWT endpoint denied the exchange")
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 16*1024))
	token := response.Header.Get("X-Jwt-Token")
	if _, ok := parseByteCloudJWTExpiry(token); !ok {
		return "", errors.New("ByteCloud JWT endpoint returned an invalid token")
	}
	return token, nil
}

func newByteCloudNonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func byteCloudHMAC(key, value []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}

func sha256Hex(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
