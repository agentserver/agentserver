//go:build darwin

package harnesspool

import "syscall"

func localAttemptSysProcAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
