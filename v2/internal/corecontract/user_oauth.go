package corecontract

const (
	BrowserOAuthAudience        = "agentserver-api"
	BrowserOAuthOpenIDScope     = "openid"
	BrowserOAuthRunScope        = "runs:write"
	BrowserOAuthExecutorScope   = "executors:write"
	BrowserOAuthLLMGatewayScope = "llm-gateways:write"
)

// BrowserOAuthScopes returns the canonical ordered authority requested by the
// reference browser client and accepted by the Core login bridge. Returning a
// fresh slice prevents one component from mutating the shared contract.
func BrowserOAuthScopes() []string {
	return []string{BrowserOAuthOpenIDScope, BrowserOAuthRunScope, BrowserOAuthExecutorScope, BrowserOAuthLLMGatewayScope}
}
