package sandboxcontract

import "fmt"

const (
	EnsureSandboxPath = "/internal/v2/sandboxes:ensure"
	SandboxPathPrefix = "/internal/v2/sandboxes/"
)

func GetSandboxPath(sandboxID string) (string, error) {
	return sandboxPath(sandboxID, "")
}

func RenewSandboxActivityPath(sandboxID string) (string, error) {
	return sandboxPath(sandboxID, ":renew-activity")
}

func SetSandboxTimeoutPath(sandboxID string) (string, error) {
	return sandboxPath(sandboxID, ":set-timeout")
}

func ReleaseSandboxActivityPath(sandboxID string) (string, error) {
	return sandboxPath(sandboxID, ":release-activity")
}

func DeleteSandboxPath(sandboxID string) (string, error) {
	return sandboxPath(sandboxID, "")
}

func RunCommandPath(sandboxID string) (string, error) {
	return sandboxPath(sandboxID, "/commands:run")
}

func ReadFilePath(sandboxID string) (string, error) {
	return sandboxPath(sandboxID, "/files:read")
}

func SignalProcessPath(sandboxID, processID string) (string, error) {
	if err := validateID("sandbox ID", sandboxID); err != nil {
		return "", err
	}
	if err := validateID("process ID", processID); err != nil {
		return "", err
	}
	return SandboxPathPrefix + sandboxID + "/processes/" + processID + ":signal", nil
}

func sandboxPath(sandboxID, suffix string) (string, error) {
	if err := validateID("sandbox ID", sandboxID); err != nil {
		return "", fmt.Errorf("build sandbox contract path: %w", err)
	}
	return SandboxPathPrefix + sandboxID + suffix, nil
}
