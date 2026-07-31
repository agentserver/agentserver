//go:build darwin

package harnesspool

import "syscall"

func localAttemptSysProcAttributes(bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
