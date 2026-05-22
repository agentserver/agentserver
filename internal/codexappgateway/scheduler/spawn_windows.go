//go:build !unix

package scheduler

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
