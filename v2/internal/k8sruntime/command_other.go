//go:build !linux

package k8sruntime

import (
	"errors"
	"os/exec"

	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

func sandboxCommand(Config, sandboxcontract.RunCommandRequest) (*exec.Cmd, error) {
	return nil, errors.New("Kubernetes sandbox runtime requires Linux")
}
