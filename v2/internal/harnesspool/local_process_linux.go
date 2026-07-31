//go:build linux

package harnesspool

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func localAttemptSysProcAttributes(privilegedWorker bool) *syscall.SysProcAttr {
	attributes := &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if privilegedWorker {
		// The fixed-code worker needs exactly these two capabilities to create
		// app-owned attempt directories and fork the fixed app UID/GID. Go's
		// Linux fork path raises them in the inheritable and ambient sets after
		// applying the worker credential. The final-exec trampoline clears every
		// capability and no_new_privs-seals the app process before stock Codex.
		attributes.AmbientCaps = []uintptr{unix.CAP_SETGID, unix.CAP_SETUID}
	}
	return attributes
}
