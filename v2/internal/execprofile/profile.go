// Package execprofile defines the product-visible subset of the pinned stock
// exec-server protocol. It is deliberately smaller than the set of handlers
// implemented by stock Codex.
package execprofile

const Version = "process-v1"

const (
	MethodProcessStart     = "process/start"
	MethodProcessRead      = "process/read"
	MethodProcessWrite     = "process/write"
	MethodProcessTerminate = "process/terminate"

	NotificationProcessOutput = "process/output"
	NotificationProcessExited = "process/exited"
	NotificationProcessClosed = "process/closed"

	ReverseMethodNetworkPolicyRequest = "network/policyRequest"
)

var processMethods = [...]string{
	MethodProcessStart,
	MethodProcessRead,
	MethodProcessWrite,
	MethodProcessTerminate,
}

var processNotifications = [...]string{
	NotificationProcessOutput,
	NotificationProcessExited,
	NotificationProcessClosed,
}

// ProcessMethods returns the exact Phase 1 gateway-to-agentx process
// capability. process/signal is intentionally absent: the pinned stock result
// cannot prove whether a signal was delivered.
func ProcessMethods() []string {
	return append([]string(nil), processMethods[:]...)
}

func AllowsProcessMethod(method string) bool {
	for _, allowed := range processMethods {
		if method == allowed {
			return true
		}
	}
	return false
}

func ProcessNotifications() []string {
	return append([]string(nil), processNotifications[:]...)
}

func AllowsProcessNotification(method string) bool {
	for _, allowed := range processNotifications {
		if method == allowed {
			return true
		}
	}
	return false
}
