//go:build !linux && !darwin

package harnesspool

import (
	"errors"
	"os/exec"
	"time"
)

func configureLocalAttemptCommand(*exec.Cmd, *LocalProcessCredential) error {
	return errors.New("local harness process launcher is only implemented on Unix")
}

func signalLocalAttemptGroup(int, bool) error {
	return errors.New("local harness process launcher is only implemented on Unix")
}

func forceAndWaitLocalAttemptGroup(int, time.Duration) error {
	return errors.New("local harness process launcher is only implemented on Unix")
}
