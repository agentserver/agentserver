package agent

import (
	"golang.org/x/sys/windows"
)

func flockExclusive(fd uintptr) error {
	// LockFileEx with LOCKFILE_EXCLUSIVE_LOCK provides the same semantics as
	// flock(LOCK_EX) on Unix: a blocking exclusive lock on the file.
	ol := new(windows.Overlapped)
	return windows.LockFileEx(
		windows.Handle(fd),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,           // reserved
		1, 0,        // lock 1 byte
		ol,
	)
}
