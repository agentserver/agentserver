//go:build unix

package harnessinit

import (
	"errors"
	"os"
	"syscall"
)

func fileOwnership(info os.FileInfo) (uint32, uint32, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, 0, errors.New("file ownership metadata is unavailable")
	}
	return stat.Uid, stat.Gid, nil
}
