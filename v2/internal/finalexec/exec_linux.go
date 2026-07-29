//go:build linux

package finalexec

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Execute validates the already-selected target, proves the requested
// inheritance traps reached the trampoline, removes privilege-gain paths,
// closes every descriptor above stderr with close_range, and execs the target.
// It returns only on failure.
func Execute(config Config) error {
	if err := validate(config); err != nil {
		return err
	}
	if err := SealIdentity(config.ExpectedUID, config.ExpectedGID); err != nil {
		return err
	}
	for _, descriptor := range config.RequiredOpenFDs {
		if _, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0); err != nil {
			return fmt.Errorf("required inherited descriptor %d was not open before close-all: %w", descriptor, err)
		}
	}
	if err := os.Chdir(config.Directory); err != nil {
		return fmt.Errorf("enter final exec directory: %w", err)
	}
	syscall.Umask(0o077)
	if err := unix.CloseRange(3, ^uint(0), 0); err != nil {
		return fmt.Errorf("close inherited descriptors with close_range: %w", err)
	}
	for _, descriptor := range config.RequiredOpenFDs {
		if _, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
			return fmt.Errorf("descriptor %d survived close-all: %v", descriptor, err)
		}
	}
	arguments := make([]string, 1, len(config.Arguments)+1)
	arguments[0] = config.Program
	arguments = append(arguments, config.Arguments...)
	return unix.Exec(config.Program, arguments, config.Environment)
}

// SealIdentity must run before any child-isolation preflight. A trusted worker
// may retain only SETUID/SETGID so it can create the fixed app identity; those
// capabilities can survive a nonzero-to-nonzero UID transition. This function
// removes them from every Go runtime OS thread, sets no_new_privs on every
// thread, and verifies the app boundary is inert.
func SealIdentity(expectedUID, expectedGID uint32) error {
	if expectedUID == 0 || expectedGID == 0 || expectedUID == ^uint32(0) || expectedGID == ^uint32(0) {
		return fmt.Errorf("final exec identity must be valid and unprivileged: uid=%d gid=%d", expectedUID, expectedGID)
	}
	realUID, effectiveUID, savedUID := unix.Getresuid()
	if uint32(realUID) != expectedUID || uint32(effectiveUID) != expectedUID || uint32(savedUID) != expectedUID {
		return fmt.Errorf("final exec uid = real %d effective %d saved %d, want %d", realUID, effectiveUID, savedUID, expectedUID)
	}
	realGID, effectiveGID, savedGID := unix.Getresgid()
	if uint32(realGID) != expectedGID || uint32(effectiveGID) != expectedGID || uint32(savedGID) != expectedGID {
		return fmt.Errorf("final exec gid = real %d effective %d saved %d, want %d", realGID, effectiveGID, savedGID, expectedGID)
	}
	groups, err := os.Getgroups()
	if err != nil {
		return fmt.Errorf("read final exec supplementary groups: %w", err)
	}
	if len(groups) != 0 {
		return fmt.Errorf("final exec inherited supplementary groups %v", groups)
	}
	if err := allThreadsPrctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return fmt.Errorf("clear ambient capabilities: %w", err)
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	capabilities := [2]unix.CapUserData{}
	_, _, errno := syscall.AllThreadsSyscall(
		syscall.SYS_CAPSET,
		uintptr(unsafe.Pointer(&header)),
		uintptr(unsafe.Pointer(&capabilities[0])),
		0,
	)
	runtime.KeepAlive(header)
	runtime.KeepAlive(capabilities)
	if errno != 0 {
		return fmt.Errorf("clear process capabilities: %w", errno)
	}
	if err := allThreadsPrctl(unix.PR_SET_KEEPCAPS, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("clear keep-capabilities mode: %w", err)
	}
	if err := allThreadsPrctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no-new-privileges: %w", err)
	}
	if err := allThreadsPrctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("disable process dumpability: %w", err)
	}
	if err := requireNoProcessCapabilities(); err != nil {
		return err
	}
	noNewPrivileges, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil || noNewPrivileges != 1 {
		return fmt.Errorf("verify no-new-privileges = %d: %w", noNewPrivileges, err)
	}
	dumpable, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	if err != nil || dumpable != 0 {
		return fmt.Errorf("verify process dumpability = %d: %w", dumpable, err)
	}
	return nil
}

func allThreadsPrctl(option int, argument2, argument3, argument4, argument5 uintptr) error {
	_, _, errno := syscall.AllThreadsSyscall6(
		syscall.SYS_PRCTL,
		uintptr(option),
		argument2,
		argument3,
		argument4,
		argument5,
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func requireNoProcessCapabilities() error {
	contents, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return fmt.Errorf("read final exec process status: %w", err)
	}
	if len(contents) > 128*1024 {
		return errors.New("final exec process status exceeds 128 KiB")
	}
	wanted := map[string]bool{
		"CapInh": false,
		"CapPrm": false,
		"CapEff": false,
		"CapAmb": false,
	}
	for _, line := range strings.Split(string(contents), "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		if _, tracked := wanted[name]; !tracked {
			continue
		}
		parsed, parseErr := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if parseErr != nil {
			return fmt.Errorf("parse %s from /proc/self/status: %w", name, parseErr)
		}
		if parsed != 0 {
			return fmt.Errorf("final exec process retains %s=%x", name, parsed)
		}
		wanted[name] = true
	}
	for name, found := range wanted {
		if !found {
			return fmt.Errorf("final exec process status omits %s", name)
		}
	}
	return nil
}
