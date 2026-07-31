//go:build !linux

package harnessworker

import (
	"errors"
	"os/exec"
)

func configureAppServerFinalExecCommand(command *exec.Cmd, uid, gid uint32) error {
	return errors.New("production app-server final exec is only implemented on Linux")
}
