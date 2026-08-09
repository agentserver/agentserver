package egressgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
)

const defaultDecisionTimeout = 350 * time.Millisecond

type Handler struct {
	service DecisionService
	timeout time.Duration
}

// DecisionService is the provider-neutral policy decision boundary. Both the
// legacy Lark service and ProviderService implement it; keeping the handler
// dependent on this small interface prevents the HTTP/Webhook layer from
// selecting a credential implementation.
type DecisionService interface {
	Authorize(context.Context, OriginalRequest, string) Decision
}

func NewHandler(service DecisionService, timeout time.Duration) (*Handler, error) {
	if service == nil {
		return nil, errors.New("egress gateway service is required")
	}
	if timeout == 0 {
		timeout = defaultDecisionTimeout
	}
	if timeout < 10*time.Millisecond || timeout > 450*time.Millisecond {
		return nil, errors.New("egress decision timeout must be between 10 and 450 milliseconds")
	}
	return &Handler{service: service, timeout: timeout}, nil
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	decision := Decision{ReasonCode: "invalid_webhook_request"}
	if handler != nil && handler.service != nil && request != nil && request.URL != nil &&
		request.Method == http.MethodPost && request.URL.Path == PolicyPath && request.URL.RawQuery == "" && !request.URL.ForceQuery && request.URL.Fragment == "" && request.URL.RawPath == "" &&
		exactHTTPHeader(request.Header, "version") == ProtocolVersion {
		if mediaType, parameters, err := mime.ParseMediaType(exactHTTPHeader(request.Header, "Content-Type")); err == nil &&
			mediaType == "application/json" && len(parameters) == 0 {
			if command, ok := decodeWebhookRequest(response, request); ok {
				outerZTI := exactHTTPHeader(request.Header, ZTIHeader)
				decision = handler.authorizeWithinDeadline(request.Context(), command.Request, outerZTI)
			}
		}
	}
	if err := decision.validate(); err != nil {
		decision = Decision{ReasonCode: "internal_denied"}
	}
	writeWebhookResponse(response, decision)
}

func (handler *Handler) authorizeWithinDeadline(parent context.Context, original OriginalRequest, outerZTI string) Decision {
	ctx, cancel := context.WithTimeout(parent, handler.timeout)
	defer cancel()
	result := make(chan Decision, 1)
	go func() {
		result <- handler.service.Authorize(ctx, original, outerZTI)
	}()
	select {
	case decision := <-result:
		return decision
	case <-ctx.Done():
		return Decision{ReasonCode: "decision_timeout"}
	}
}

func decodeWebhookRequest(response http.ResponseWriter, request *http.Request) (webhookRequestEnvelope, bool) {
	request.Body = http.MaxBytesReader(response, request.Body, maximumWebhookBodyBytes)
	raw, err := io.ReadAll(request.Body)
	if err != nil || len(raw) == 0 || len(raw) > maximumWebhookBodyBytes {
		return webhookRequestEnvelope{}, false
	}
	limits := braincatalog.DefaultLimits()
	limits.MaxJSONValues = 4096
	limits.MaxJSONDepth = 8
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, maximumWebhookBodyBytes, limits); err != nil {
		return webhookRequestEnvelope{}, false
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil || top["request"] == nil {
		return webhookRequestEnvelope{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(top["request"]))
	decoder.DisallowUnknownFields()
	var original OriginalRequest
	if err := decoder.Decode(&original); err != nil {
		return webhookRequestEnvelope{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return webhookRequestEnvelope{}, false
	}
	return webhookRequestEnvelope{Request: original}, true
}

func exactHTTPHeader(headers http.Header, wanted string) string {
	value := ""
	found := false
	for name, values := range headers {
		if !strings.EqualFold(name, wanted) {
			continue
		}
		if found || len(values) != 1 || values[0] == "" || strings.TrimSpace(values[0]) != values[0] {
			return ""
		}
		found = true
		value = values[0]
	}
	if !found {
		return ""
	}
	return value
}

func writeWebhookResponse(response http.ResponseWriter, decision Decision) {
	result := "deny"
	applicationInfo := decision.ReasonCode
	var headers map[string]string
	if decision.Allow {
		result = "allow"
		applicationInfo = ""
		headers = decision.Headers
	}
	payload := WebhookResponse{
		Code: 0, Version: ProtocolVersion,
		Data: WebhookResponseData{Result: result, ApplicationInfo: applicationInfo, Header: headers},
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(response)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
}
