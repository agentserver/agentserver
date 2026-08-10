package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"code.byted.org/inf/bytedai-go/region"
	"code.byted.org/inf/bytedai-go/sandbox"
	"github.com/agentserver/agentserver/v2/internal/managedruntime"
)

const defaultControlRequestTimeout = 45 * time.Second

type SDKControlPlaneConfig struct {
	PSM             string
	HTTPClient      *http.Client
	Headers         HeaderSource
	ControlPlaneURL string
	RequestTimeout  time.Duration
}

type SDKControlPlane struct {
	client         *sandbox.SandboxClient
	identityClient *http.Client
	metadataURL    string
	psm            string
	requestTimeout time.Duration
}

type SandboxDescriptor struct {
	ID   string
	PSM  string
	Type string
}

// NewSGSDKControlPlane pins the official SDK to I18N production. Region
// inference is intentionally not supported by the production adapter.
func NewSGSDKControlPlane(ctx context.Context, config SDKControlPlaneConfig) (*SDKControlPlane, error) {
	if ctx == nil || config.HTTPClient == nil || config.HTTPClient.Transport == nil || config.Headers == nil {
		return nil, errors.New("TAE SDK context, strict HTTP client, and identity header source are required")
	}
	if strings.TrimSpace(config.PSM) != config.PSM || config.PSM == "" || len(config.PSM) > 256 {
		return nil, errors.New("TAE SDK PSM is invalid")
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = defaultControlRequestTimeout
	}
	if config.RequestTimeout < time.Second || config.RequestTimeout > time.Minute {
		return nil, errors.New("TAE SDK request timeout must be between one second and one minute")
	}
	controlPlaneOrigin, err := region.AIPaaSGatewayRegionI18nProd.GetSandboxdControlPlaneDomain()
	if err != nil || controlPlaneOrigin != "https://"+SGTAEControlPlaneHost {
		return nil, fmt.Errorf("TAE SDK I18N production control plane drifted from https://%s", SGTAEControlPlaneHost)
	}
	if config.ControlPlaneURL != "" {
		endpoint, err := url.Parse(config.ControlPlaneURL)
		if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Opaque != "" ||
			endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawPath != "" ||
			(endpoint.Path != "" && endpoint.Path != "/") {
			return nil, errors.New("TAE SDK control-plane override must be a canonical HTTPS origin")
		}
		controlPlaneOrigin = strings.TrimSuffix(config.ControlPlaneURL, "/")
	}
	identityClient, err := NewIdentityHTTPClient(config.HTTPClient, config.Headers)
	if err != nil {
		return nil, fmt.Errorf("configure TAE SDK identity transport: %w", err)
	}
	client, err := sandbox.NewSandboxClientWithOptions(
		ctx, config.PSM, "", region.AIPaaSGatewayRegionI18nProd,
		&sandbox.SandboxClientOptions{LegacyHTTPClient: identityClient, ControlPlaneURL: config.ControlPlaneURL}, false,
	)
	if err != nil {
		return nil, fmt.Errorf("create pinned TAE SDK client: %w", err)
	}
	metadataURL := controlPlaneOrigin + "/api/v1/sandboxes/" + url.PathEscape(config.PSM)
	return &SDKControlPlane{
		client: client, identityClient: identityClient, metadataURL: metadataURL,
		psm: config.PSM, requestTimeout: config.RequestTimeout,
	}, nil
}

// DescribeSandbox resolves the immutable management-plane identity needed by
// the sandboxd gateway's X-Forwarded-Prefix. It deliberately uses the same
// pinned control-plane client and provider identity as lifecycle operations;
// callers cannot supply an alternate authority or sandbox ID.
func (control *SDKControlPlane) DescribeSandbox(ctx context.Context) (SandboxDescriptor, error) {
	if control == nil || control.identityClient == nil || control.metadataURL == "" || control.psm == "" {
		return SandboxDescriptor{}, &RequestError{Code: "provider_unavailable", Cause: errors.New("TAE sandbox descriptor client is unavailable")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestContext, cancel := context.WithTimeout(ctx, control.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, control.metadataURL, nil)
	if err != nil {
		return SandboxDescriptor{}, &RequestError{Code: "bad_request", Cause: errors.New("TAE sandbox descriptor request is invalid")}
	}
	traced, wrote := traceRequest(request.Context())
	response, err := control.identityClient.Do(request.WithContext(traced))
	if err != nil {
		code := "provider_unavailable"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			code = "request_timeout"
		}
		return SandboxDescriptor{}, &RequestError{WroteRequest: wrote.Load(), Code: code, Cause: errors.New("TAE sandbox descriptor request failed")}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		boundedBody, _ := io.ReadAll(io.LimitReader(response.Body, defaultMaxErrorBytes+1))
		providerCode, bodyRequestID := responseErrorMetadata(boundedBody, defaultMaxErrorBytes)
		requestID := providerRequestID(response.Header)
		if requestID == "" {
			requestID = bodyRequestID
		}
		return SandboxDescriptor{}, &RequestError{
			WroteRequest: wrote.Load(), StatusCode: response.StatusCode,
			Code: responseCode(response.StatusCode, boundedBody, defaultMaxErrorBytes), ProviderCode: providerCode, RequestID: requestID,
			Cause: errors.New("TAE sandbox descriptor returned a non-success response"),
		}
	}
	var envelope sandbox.SandboxMetaResponse
	boundedBody, err := io.ReadAll(io.LimitReader(response.Body, defaultMaxErrorBytes+1))
	if err != nil || int64(len(boundedBody)) > defaultMaxErrorBytes || decodeSingleJSON(boundedBody, &envelope) != nil {
		return SandboxDescriptor{}, &RequestError{
			WroteRequest: true, StatusCode: response.StatusCode, Code: "invalid_response",
			RequestID: providerRequestID(response.Header), Cause: errors.New("TAE sandbox descriptor response was invalid"),
		}
	}
	if envelope.Code != 0 || envelope.Data == nil || envelope.Data.Psm != control.psm ||
		envelope.Data.SandboxType != sandbox.SandboxTypeTerminal ||
		!sessionDNSLabelPattern.MatchString(envelope.Data.SandboxID) || strings.ToLower(envelope.Data.SandboxID) != envelope.Data.SandboxID {
		return SandboxDescriptor{}, &RequestError{
			WroteRequest: true, StatusCode: response.StatusCode, Code: "invalid_response",
			RequestID: providerRequestID(response.Header), Cause: errors.New("TAE sandbox descriptor did not match the configured terminal PSM"),
		}
	}
	return SandboxDescriptor{ID: envelope.Data.SandboxID, PSM: envelope.Data.Psm, Type: string(envelope.Data.SandboxType)}, nil
}

func (control *SDKControlPlane) Create(ctx context.Context, input CreateInput) (ControlSession, error) {
	if input.Command != managedruntime.ExecutablePath {
		return ControlSession{}, &RequestError{Code: "bad_request", Cause: errors.New("TAE session command differs from the managed runtime contract")}
	}
	seconds, err := wholeSeconds(input.TTL)
	if err != nil {
		return ControlSession{}, &RequestError{Code: "bad_request", Cause: err}
	}
	traced, wrote := traceRequest(ctx)
	session, err := control.client.CreateSessionWithOpts(traced, &sandbox.CreateSessionOpts{
		TTL: seconds, Metadata: cloneStrings(input.Metadata), Command: input.Command,
		Timeout: control.requestTimeout,
	})
	if err != nil {
		return ControlSession{}, controlError(err, wrote.Load())
	}
	converted, err := convertSDKSession(session)
	if err != nil {
		return ControlSession{}, &RequestError{WroteRequest: true, Code: "invalid_response", Cause: err}
	}
	return converted, nil
}

func (control *SDKControlPlane) Get(ctx context.Context, sessionID string) (ControlSession, error) {
	if sessionID == "" {
		return ControlSession{}, ErrSessionNotFound
	}
	traced, wrote := traceRequest(ctx)
	session, err := control.client.GetSessionWithOpts(traced, sessionID, sandbox.WithGetSessionTimeout(control.requestTimeout))
	if err != nil {
		requestError := controlError(err, wrote.Load())
		if requestError.StatusCode == http.StatusNotFound {
			return ControlSession{}, ErrSessionNotFound
		}
		return ControlSession{}, requestError
	}
	converted, err := convertSDKSession(session)
	if err != nil {
		return ControlSession{}, &RequestError{WroteRequest: true, Code: "invalid_response", Cause: err}
	}
	return converted, nil
}

func (control *SDKControlPlane) Search(ctx context.Context, input SearchInput) (SearchResult, error) {
	if input.Limit < 1 || input.Limit > 100 {
		return SearchResult{}, &RequestError{Code: "bad_request", Cause: errors.New("TAE search limit is invalid")}
	}
	traced, wrote := traceRequest(ctx)
	result, err := control.client.SessionsSearchWithResultOpts(traced, &sandbox.SessionsSearchOpts{
		SessionQueryParams: sandbox.SessionQueryParams{PageNumber: 1, PageSize: input.Limit},
		MetaData:           cloneStrings(input.Metadata), UseFuzzy: false, LiteMode: false,
	}, &sandbox.SessionResultOpts{Timeout: control.requestTimeout})
	if err != nil {
		return SearchResult{}, controlError(err, wrote.Load())
	}
	if result == nil {
		return SearchResult{}, &RequestError{WroteRequest: true, Code: "invalid_response", Cause: errors.New("TAE search returned no response data")}
	}
	converted, err := convertSDKSearchResult(result)
	if err != nil {
		return SearchResult{}, &RequestError{WroteRequest: true, Code: "invalid_response", Cause: err}
	}
	return converted, nil
}

func convertSDKSearchResult(result *sandbox.SessionsSearchResponseData) (SearchResult, error) {
	if result == nil {
		return SearchResult{}, errors.New("TAE search returned no response data")
	}
	if result.Total < 0 || result.Total < len(result.Sessions) {
		return SearchResult{}, errors.New("TAE search returned an invalid total")
	}
	sessions := make([]ControlSession, 0, len(result.Sessions))
	for _, session := range result.Sessions {
		if session == nil {
			return SearchResult{}, errors.New("TAE search returned a nil session")
		}
		converted, err := convertSDKSessionInfo(session)
		if err != nil {
			return SearchResult{}, err
		}
		sessions = append(sessions, converted)
	}
	return SearchResult{Sessions: sessions, Total: result.Total}, nil
}

func (control *SDKControlPlane) UpdateTTL(ctx context.Context, sessionID string, ttl time.Duration) error {
	seconds, err := wholeSeconds(ttl)
	if err != nil {
		return &RequestError{Code: "bad_request", Cause: err}
	}
	traced, wrote := traceRequest(ctx)
	if err := control.client.UpdateSession(traced, sessionID, seconds); err != nil {
		return controlError(err, wrote.Load())
	}
	return nil
}

func (control *SDKControlPlane) Delete(ctx context.Context, sessionID string) error {
	traced, wrote := traceRequest(ctx)
	if err := control.client.DeleteSession(traced, sessionID); err != nil {
		requestError := controlError(err, wrote.Load())
		if requestError.StatusCode == http.StatusNotFound {
			return ErrSessionNotFound
		}
		return requestError
	}
	return nil
}

func convertSDKSession(session *sandbox.Session) (ControlSession, error) {
	if session == nil || session.AdvancedInfo == nil {
		return ControlSession{}, errors.New("TAE SDK returned an incomplete session")
	}
	expiresAt, err := parseTAETime(session.ExpiresAt)
	if err != nil && !session.AdvancedInfo.Deleted {
		return ControlSession{}, err
	}
	return ControlSession{
		ID: session.SessionID, Status: session.AdvancedInfo.Status, ExpiresAt: expiresAt,
		Deleted: session.AdvancedInfo.Deleted, SandboxdEnabled: session.AdvancedInfo.SandboxdEnabled,
		Command: session.AdvancedInfo.Command, Metadata: cloneStrings(session.AdvancedInfo.Metadata),
	}, nil
}

func convertSDKSessionInfo(session *sandbox.SessionInfoResponseData) (ControlSession, error) {
	if session == nil {
		return ControlSession{}, errors.New("TAE SDK session info is nil")
	}
	expiresAt, err := parseTAETime(session.ExpiresAt)
	if err != nil && !session.Deleted {
		return ControlSession{}, err
	}
	return ControlSession{
		ID: session.SessionID, Status: session.Status, ExpiresAt: expiresAt, Deleted: session.Deleted,
		SandboxdEnabled: session.SandboxdEnabled, Command: session.Command, Metadata: cloneStrings(session.Metadata),
	}, nil
}

func parseTAETime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.New("TAE session expiry was not RFC3339")
	}
	return parsed.UTC(), nil
}

func wholeSeconds(duration time.Duration) (int, error) {
	if duration <= 0 || duration%time.Second != 0 || duration > 24*time.Hour {
		return 0, errors.New("TAE TTL must be whole seconds between one second and 24 hours")
	}
	return int(duration / time.Second), nil
}

func traceRequest(ctx context.Context) (context.Context, *atomic.Bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	wrote := &atomic.Bool{}
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wrote.Store(true) }}
	return httptrace.WithClientTrace(ctx, trace), wrote
}

func controlError(err error, wrote bool) *RequestError {
	requestError := &RequestError{WroteRequest: wrote, Code: "provider_unavailable", Cause: errors.New("TAE control-plane request failed")}
	var apiError *sandbox.APIError
	if errors.As(err, &apiError) && apiError != nil {
		requestError.StatusCode = apiError.StatusCode
		requestError.RequestID = safeProviderRequestID(apiError.LogID)
		requestError.Code = responseCode(apiError.StatusCode, nil, 0)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		requestError.Code = "request_timeout"
	}
	return requestError
}

func safeProviderRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 1024 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func SGDataplaneDomainSuffix() (string, error) {
	suffix, err := region.AIPaaSGatewayRegionI18nProd.GetSandboxdDomainSuffix()
	if err != nil {
		return "", err
	}
	if suffix != SGTAEDomainSuffix {
		return "", fmt.Errorf("TAE SDK I18N production domain suffix drifted from %s", SGTAEDomainSuffix)
	}
	return suffix, nil
}

var _ ControlPlane = (*SDKControlPlane)(nil)
