package browsergateway

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
)

const llmGatewayCallbackScript = `(function () {
  'use strict';
  const targetOrigin = window.location.origin;
  const opener = window.opener;
  const parameters = new URLSearchParams(window.location.search);
  const allowed = new Set(['code', 'state', 'error', 'error_description', 'error_uri', 'iss', 'session_state']);
  let payload;
  try {
    for (const [name, value] of parameters) {
      if (!allowed.has(name) || parameters.getAll(name).length !== 1 || value.length < 1 || value.length > 8192 || /[\0\r\n]/u.test(value)) {
        throw new Error('invalid_callback');
      }
    }
    const state = parameters.get('state') || '';
    const code = parameters.get('code') || '';
    const providerError = parameters.get('error') || '';
    if (!state || Boolean(code) === Boolean(providerError)) throw new Error('invalid_callback');
    payload = Object.freeze({
      type: 'agentserver-v2.llm-gateway-oidc-callback',
      version: 1,
      state,
      code,
      providerError,
      providerErrorDescription: parameters.get('error_description') || '',
    });
  } catch (_) {
    payload = Object.freeze({
      type: 'agentserver-v2.llm-gateway-oidc-callback',
      version: 1,
      protocolError: 'invalid_callback',
    });
  }
  history.replaceState(null, '', window.location.pathname);
  if (opener && !opener.closed) {
    opener.postMessage(payload, targetOrigin);
    window.close();
    window.setTimeout(function () { document.body.textContent = 'Authorization completed. You may close this window.'; }, 250);
  } else {
    document.body.textContent = 'The authorization window is no longer connected. Return to AgentServer and try again.';
  }
}());`

type LLMGatewayCallbackHandler struct {
	body []byte
	csp  string
}

func NewLLMGatewayCallbackHandler() *LLMGatewayCallbackHandler {
	digest := sha256.Sum256([]byte(llmGatewayCallbackScript))
	body := []byte("<!doctype html><html><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width\"><title>AgentServer Gateway authorization</title></head><body>Completing authorization…<script>" + llmGatewayCallbackScript + "</script></body></html>\n")
	return &LLMGatewayCallbackHandler{
		body: body,
		csp:  "default-src 'none'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
	}
}

func (handler *LLMGatewayCallbackHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
	response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	if handler == nil || len(handler.body) == 0 || handler.csp == "" {
		http.Error(response, "workspace LLM gateway callback unavailable", http.StatusServiceUnavailable)
		return
	}
	response.Header().Set("Content-Security-Policy", handler.csp)
	if request.Method != http.MethodGet || request.ContentLength != 0 || len(request.TransferEncoding) != 0 ||
		request.Header.Get("Authorization") != "" {
		http.Error(response, "invalid workspace LLM gateway callback request", http.StatusBadRequest)
		return
	}
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(handler.body)
}
