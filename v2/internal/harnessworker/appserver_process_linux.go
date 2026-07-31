//go:build linux

package harnessworker

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureAppServerFinalExecCommand(command *exec.Cmd, uid, gid uint32) error {
	if command == nil {
		return errors.New("app-server final-exec command is required")
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}},
		Pdeathsig:  syscall.SIGKILL,
	}
	return nil
}
