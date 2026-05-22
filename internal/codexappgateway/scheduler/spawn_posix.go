//go:build unix

package scheduler

import (
	"os/exec"
	"syscall"
)

// setProcessGroup makes the child the leader of a new process group so we
// can signal the whole tree (bash + its grandchildren) with one syscall.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup signals the entire pgroup. Safe to call after Wait.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
