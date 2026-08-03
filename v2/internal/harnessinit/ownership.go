package harnessinit

import (
	"fmt"
	"os"
)

func ensureOwnership(path string, uid, gid uint32) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	actualUID, actualGID, err := fileOwnership(info)
	if err != nil {
		return err
	}
	if actualUID == uid && actualGID == gid {
		return nil
	}
	return os.Chown(path, int(uid), int(gid))
}

func verifyOwnership(info os.FileInfo, uid, gid uint32) error {
	actualUID, actualGID, err := fileOwnership(info)
	if err != nil {
		return err
	}
	if actualUID != uid || actualGID != gid {
		return fmt.Errorf("ownership is %d:%d, want %d:%d", actualUID, actualGID, uid, gid)
	}
	return nil
}
