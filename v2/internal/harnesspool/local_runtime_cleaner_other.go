//go:build !linux

package harnesspool

import "errors"

func requireLocalCleanupDACOverride() error {
	return errors.New("production local runtime cleanup is only supported on Linux")
}

func requireLocalProcessProductionCapabilities() error {
	return errors.New("production local process backend is only supported on Linux")
}
