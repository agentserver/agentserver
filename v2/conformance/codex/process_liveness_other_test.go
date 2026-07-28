//go:build !darwin && !linux

package codex_test

const processLivenessProbeSupported = false

func processIsAlive(int) (bool, error) {
	return false, nil
}
