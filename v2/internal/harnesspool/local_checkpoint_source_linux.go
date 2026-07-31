//go:build linux

package harnesspool

import (
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/agentserver/agentserver/v2/internal/checkpoint"
	"github.com/agentserver/agentserver/v2/internal/harnesslayout"
	"golang.org/x/sys/unix"
)

func openLocalCheckpointRollout(
	attemptAnchor *os.File,
	locator string,
	expectedApp LocalProcessCredential,
) (AttemptCheckpointRollout, error) {
	if attemptAnchor == nil {
		return AttemptCheckpointRollout{}, errors.New("local attempt runtime anchor is required")
	}
	relative := path.Join(
		harnesslayout.AppRuntimeDirectory,
		harnesslayout.CodexHomeDirectory,
		locator,
	)
	how := &unix.OpenHow{
		Flags: uint64(unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: uint64(
			unix.RESOLVE_BENEATH |
				unix.RESOLVE_NO_SYMLINKS,
		),
	}
	fd, err := unix.Openat2(int(attemptAnchor.Fd()), relative, how)
	if err != nil {
		return AttemptCheckpointRollout{}, fmt.Errorf("open local checkpoint rollout beneath attempt anchor: %w", err)
	}
	closeFD := true
	defer func() {
		if closeFD {
			_ = unix.Close(fd)
		}
	}()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return AttemptCheckpointRollout{}, fmt.Errorf("inspect opened local checkpoint rollout: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return AttemptCheckpointRollout{}, errors.New("local checkpoint rollout is not a regular file")
	}
	if stat.Uid != expectedApp.UID || stat.Gid != expectedApp.GID {
		return AttemptCheckpointRollout{}, fmt.Errorf(
			"local checkpoint rollout owner is %d:%d, want %d:%d",
			stat.Uid, stat.Gid, expectedApp.UID, expectedApp.GID,
		)
	}
	if stat.Mode&0o7777 != checkpoint.RolloutMode {
		return AttemptCheckpointRollout{}, fmt.Errorf(
			"local checkpoint rollout mode is %04o, want %04o",
			stat.Mode&0o7777, checkpoint.RolloutMode,
		)
	}
	if stat.Nlink != 1 {
		return AttemptCheckpointRollout{}, errors.New("local checkpoint rollout must have exactly one filesystem link")
	}
	if stat.Size < 1 || stat.Size > checkpoint.MaximumRolloutBytes {
		return AttemptCheckpointRollout{}, fmt.Errorf(
			"local checkpoint rollout size must be between 1 and %d bytes",
			checkpoint.MaximumRolloutBytes,
		)
	}
	file := os.NewFile(uintptr(fd), "checkpoint-rollout:"+locator)
	if file == nil {
		return AttemptCheckpointRollout{}, errors.New("wrap local checkpoint rollout descriptor")
	}
	closeFD = false
	return AttemptCheckpointRollout{Reader: file, SizeBytes: stat.Size}, nil
}
