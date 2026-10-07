//go:build linux

// Generate the SG amd64, capability-free containerd profile with only the
// additional namespace/mount operations needed by unprivileged bubblewrap.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"
)

func main() {
	output := flag.String("output", "", "generated JSON file; stdout when empty")
	flag.Parse()
	if runtime.GOARCH != "amd64" {
		panic("SG profile generation requires Linux amd64")
	}
	profile := seccomp.DefaultProfile(&specs.Spec{Process: &specs.Process{Capabilities: &specs.LinuxCapabilities{}}})
	// These debugging/compatibility operations are permitted by containerd's
	// default on recent kernels but are not needed by this CLI runtime. Keep
	// them denied in the dedicated profile rather than broadening that surface.
	for i := range profile.Syscalls {
		names := []string{}
		for _, name := range profile.Syscalls[i].Names {
			switch name {
			case "ptrace", "process_vm_readv", "process_vm_writev", "modify_ldt":
				continue
			}
			names = append(names, name)
		}
		profile.Syscalls[i].Names = names
	}
	filtered := []specs.LinuxSyscall{}
	for _, rule := range profile.Syscalls {
		if len(rule.Names) > 0 {
			filtered = append(filtered, rule)
		}
	}
	profile.Syscalls = filtered
	profile.Architectures = []specs.Arch{specs.ArchX86_64}
	profile.Syscalls = append(profile.Syscalls,
		specs.LinuxSyscall{Names: []string{"mount", "umount2", "pivot_root"}, Action: specs.ActAllow},
		specs.LinuxSyscall{Names: []string{"unshare"}, Action: specs.ActAllow, Args: []specs.LinuxSeccompArg{{Index: 0, Op: specs.OpMaskedEqual, Value: uint64(^uint32(unix.CLONE_NEWUSER | unix.CLONE_NEWNS | unix.CLONE_NEWPID | unix.CLONE_NEWIPC | unix.CLONE_NEWUTS)), ValueTwo: 0}}},
		specs.LinuxSyscall{Names: []string{"clone"}, Action: specs.ActAllow, Args: []specs.LinuxSeccompArg{{Index: 0, Op: specs.OpMaskedEqual, Value: unix.CLONE_NEWNET | unix.CLONE_NEWCGROUP | unix.CLONE_NEWTIME, ValueTwo: 0}}},
	)
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		panic(err)
	}
	data = append(data, '\n')
	if *output == "" {
		fmt.Print(string(data))
		return
	}
	if err := os.WriteFile(*output, data, 0644); err != nil {
		panic(err)
	}
}
