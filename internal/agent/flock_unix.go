//go:build !windows

package agent

import "syscall"

func flockExclusive(fd uintptr) error {
	return syscall.Flock(int(fd), syscall.LOCK_EX)
}
