//go:build !linux

package harnessworker

import (
	"context"
	"errors"
)

func validateLocalWorkerIdentity(uint32, uint32, uint32, uint32) error {
	return errors.New("local worker/app identity preparation is only implemented on Linux")
}

func installLocalAppRuntime(
	context.Context,
	string,
	[]byte,
	*RestoredCheckpoint,
	uint32,
	uint32,
) (localAppRuntimePaths, string, error) {
	return localAppRuntimePaths{}, "", errors.New("local app runtime installation is only implemented on Linux")
}
