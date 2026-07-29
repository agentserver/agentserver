//go:build linux

package codexprocess

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func configureProcessIdentity(command *exec.Cmd, identity *Identity) error {
	if identity == nil {
		return nil
	}
	if command == nil {
		return errors.New("Codex command is required")
	}
	if identity.UID == 0 || identity.GID == 0 || identity.UID == ^uint32(0) || identity.GID == ^uint32(0) {
		return fmt.Errorf("Codex child identity must be valid and unprivileged: uid=%d gid=%d", identity.UID, identity.GID)
	}
	attributes := &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid:    identity.UID,
			Gid:    identity.GID,
			Groups: []uint32{},
		},
		Pdeathsig: syscall.SIGKILL,
	}
	if identity.AllowSetID {
		attributes.AmbientCaps = []uintptr{unix.CAP_SETGID, unix.CAP_SETUID}
	}
	command.SysProcAttr = attributes
	return nil
}
