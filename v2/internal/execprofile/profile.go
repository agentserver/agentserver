// Package execprofile defines the product-visible subset of the pinned stock
// exec-server protocol. It is deliberately smaller than the set of handlers
// implemented by stock Codex.
package execprofile

const Version = "process-v1"

// FilesystemReadVersion is an additive environment capability. A process-only
// environment continues to advertise Version; an environment that has also
// passed the bounded, fs-only lane gate advertises FilesystemReadVersion.
const FilesystemReadVersion = "process-v1+filesystem-read-v1"

const (
	MaxFilesystemReadLength uint64 = 1024 * 1024
	MaxFilesystemReadOffset uint64 = 9_007_199_254_740_991
)

const (
	MethodProcessStart     = "process/start"
	MethodProcessRead      = "process/read"
	MethodProcessWrite     = "process/write"
	MethodProcessTerminate = "process/terminate"

	NotificationProcessOutput = "process/output"
	NotificationProcessExited = "process/exited"
	NotificationProcessClosed = "process/closed"

	ReverseMethodNetworkPolicyRequest = "network/policyRequest"

	// MethodFilesystemReadFileBlock is an agentx composition, not a stock
	// exec-server method. One outer mutation maps to fs/open, one bounded
	// fs/readBlock, and fs/close on a disposable fs-only stock instance.
	MethodFilesystemReadFileBlock = "agentx/fs/readFileBlock"
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

var filesystemReadMethods = [...]string{
	MethodFilesystemReadFileBlock,
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

// AllowsEnvironmentProfile keeps process-only enrollment valid while making
// bounded filesystem support an explicit, independently gated capability.
func AllowsEnvironmentProfile(version string) bool {
	return version == Version || version == FilesystemReadVersion
}

func SupportsFilesystemRead(version string) bool {
	return version == FilesystemReadVersion
}

func FilesystemReadMethods() []string {
	return append([]string(nil), filesystemReadMethods[:]...)
}

func AllowsFilesystemReadMethod(method string) bool {
	for _, allowed := range filesystemReadMethods {
		if method == allowed {
			return true
		}
	}
	return false
}
