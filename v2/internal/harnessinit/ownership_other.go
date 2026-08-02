//go:build !unix

package harnessinit

import (
	"errors"
	"os"
)

func fileOwnership(os.FileInfo) (uint32, uint32, error) {
	return 0, 0, errors.New("worker material ownership is supported only on Unix")
}
