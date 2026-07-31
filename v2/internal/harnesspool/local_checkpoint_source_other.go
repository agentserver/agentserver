//go:build !linux

package harnesspool

import (
	"errors"
	"os"
)

func openLocalCheckpointRollout(
	*os.File,
	string,
	LocalProcessCredential,
) (AttemptCheckpointRollout, error) {
	return AttemptCheckpointRollout{}, errors.New("trusted local checkpoint rollout access is only implemented on Linux")
}
