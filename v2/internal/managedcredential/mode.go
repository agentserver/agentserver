// Package managedcredential defines the closed workspace credential-delivery
// modes shared by Core, the execution gateway, and the egress authorizer.
package managedcredential

const (
	ModeWebhookSwap = "webhook_swap"
	ModeProcessEnv  = "process_env"

	LarkAgentTraceEnvironment = "LARKSUITE_CLI_AGENT_TRACE"
	LarkAgentTraceHeader      = "X-Agent-Trace"
	LarkSanitizedAgentTrace   = "agentserver-managed"
)

func ValidMode(mode string) bool {
	return mode == ModeWebhookSwap || mode == ModeProcessEnv
}
