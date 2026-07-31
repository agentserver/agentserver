//go:build linux

package harnesspool

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func requireLocalCleanupDACOverride() error {
	return requireEffectiveLocalProcessCapabilities([]localProcessCapability{{number: unix.CAP_DAC_OVERRIDE, name: "DAC_OVERRIDE"}})
}

func requireLocalProcessProductionCapabilities() error {
	return requireEffectiveLocalProcessCapabilities([]localProcessCapability{
		{number: unix.CAP_CHOWN, name: "CHOWN"},
		{number: unix.CAP_DAC_OVERRIDE, name: "DAC_OVERRIDE"},
		{number: unix.CAP_SETGID, name: "SETGID"},
		{number: unix.CAP_SETUID, name: "SETUID"},
	})
}

type localProcessCapability struct {
	number int
	name   string
}

func requireEffectiveLocalProcessCapabilities(required []localProcessCapability) error {
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	capabilities := [2]unix.CapUserData{}
	if err := unix.Capget(&header, &capabilities[0]); err != nil {
		return fmt.Errorf("read harness-pool capabilities for local process backend: %w", err)
	}
	for _, capability := range required {
		index := uint(capability.number) / 32
		mask := uint32(1) << (uint(capability.number) % 32)
		if index >= uint(len(capabilities)) || capabilities[index].Effective&mask == 0 {
			return fmt.Errorf("production local process backend requires effective CAP_%s", capability.name)
		}
	}
	return nil
}
