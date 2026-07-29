//go:build !darwin && !linux

package codexprocess

import (
	"errors"
	"os/exec"
)

func configureProcessIdentity(_ *exec.Cmd, identity *Identity) error {
	if identity != nil {
		return errors.New("explicit Codex child identity is unsupported on this platform")
	}
	return nil
}
