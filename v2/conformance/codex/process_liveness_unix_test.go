//go:build darwin || linux

package codex_test

import (
	"errors"
	"syscall"
)

const processLivenessProbeSupported = true

func processIsAlive(pid int) (bool, error) {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	case errors.Is(err, syscall.EPERM):
		return true, nil
	default:
		return false, err
	}
}
