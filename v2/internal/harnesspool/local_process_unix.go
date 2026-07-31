//go:build linux || darwin

package harnesspool

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func configureLocalAttemptCommand(command *exec.Cmd, credential *LocalProcessCredential) error {
	attributes := localAttemptSysProcAttributes()
	if credential != nil {
		attributes.Credential = &syscall.Credential{
			Uid: credential.UID, Gid: credential.GID, Groups: []uint32{},
		}
	}
	command.SysProcAttr = attributes
	return nil
}

func signalLocalAttemptGroup(processGroupID int, force bool) error {
	if processGroupID < 1 {
		return errors.New("local attempt process group ID is invalid")
	}
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	err := syscall.Kill(-processGroupID, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("signal local attempt process group: %w", err)
	}
	return nil
}

func forceAndWaitLocalAttemptGroup(processGroupID int, timeout time.Duration) error {
	if err := signalLocalAttemptGroup(processGroupID, true); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(-processGroupID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("inspect local attempt process group: %w", err)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("local attempt process group remained alive after %s", timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
