//go:build linux

package harnessinit

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func readProjectedFile(root, name string, maximum int64) ([]byte, error) {
	rootFD, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open projected root: %w", err)
	}
	defer unix.Close(rootFD)
	fd, err := unix.Openat2(rootFD, name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV,
	})
	if err != nil {
		return nil, fmt.Errorf("open projected path beneath root: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("construct projected file descriptor")
	}
	return readBoundedProjectedRegular(file, maximum)
}

func readBoundedProjectedRegular(file *os.File, maximum int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() < 1 || info.Size() > maximum {
		_ = file.Close()
		return nil, errors.New("projected source must be a bounded regular file not writable by group or other")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, maximum+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil {
		clear(raw)
		return nil, errors.Join(readErr, statErr, closeErr)
	}
	if int64(len(raw)) != info.Size() || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		clear(raw)
		return nil, errors.New("projected source changed while it was being read")
	}
	return raw, nil
}
