//go:build linux

package harnesspool

import "syscall"

func localAttemptSysProcAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
}
