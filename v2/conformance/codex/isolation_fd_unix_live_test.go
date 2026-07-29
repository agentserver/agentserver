//go:build darwin || linux

package codex_test

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// TestAppServerA12HostLaunchReliesOnCloseOnExecForDescriptorIsolation is a
// negative characterization of the current Go exec boundary. It deliberately
// clears CLOEXEC on a parent-owned control pipe. The stock child keeps that
// writer open, proving a production runner must close/allowlist descriptors in
// the exec child and verify worker credential/control descriptors are CLOEXEC;
// merely omitting ExtraFiles is insufficient.
func TestAppServerA12HostLaunchReliesOnCloseOnExecForDescriptorIsolation(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	reader, workerControlFD, err := os.Pipe()
	if err != nil {
		t.Fatalf("create A12 worker control FD probe: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = workerControlFD.Close()
	})
	readerFD := int(reader.Fd())
	workerControlFDNumber := workerControlFD.Fd()
	if _, _, errno := syscall.Syscall(
		syscall.SYS_FCNTL,
		workerControlFDNumber,
		uintptr(syscall.F_SETFD),
		0,
	); errno != 0 {
		t.Fatalf("clear close-on-exec from A12 worker control FD: %v", errno)
	}
	if err := syscall.SetNonblock(readerFD, true); err != nil {
		t.Fatalf("make A12 inheritance probe nonblocking: %v", err)
	}

	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	if err := workerControlFD.Close(); err != nil {
		t.Fatalf("close parent A12 worker control FD: %v", err)
	}
	buffer := make([]byte, 1)
	bytesRead, _, readErrno := syscall.Syscall(
		syscall.SYS_READ,
		uintptr(readerFD),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
	)
	if !errors.Is(readErrno, syscall.EAGAIN) && !errors.Is(readErrno, syscall.EWOULDBLOCK) {
		if readErrno == 0 && bytesRead == 0 {
			t.Fatal("A12 launch stopped inheriting non-CLOEXEC descriptors; replace this negative characterization with a close-all positive gate")
		}
		t.Fatalf("read A12 inheritance probe: bytes=%d error=%v", bytesRead, readErrno)
	}
	t.Log("A12 remains open: an unlisted non-CLOEXEC worker descriptor reached stock app-server")

	initializeAppServer(t, process)
	closeAndWait(t, process)
}
