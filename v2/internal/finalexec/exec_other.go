//go:build !linux

package finalexec

import "errors"

func Execute(config Config) error {
	if err := validate(config); err != nil {
		return err
	}
	return errors.New("final exec close-all boundary is only implemented on Linux")
}

func SealIdentity(expectedUID, expectedGID uint32) error {
	if expectedUID == 0 || expectedGID == 0 || expectedUID == ^uint32(0) || expectedGID == ^uint32(0) {
		return errors.New("final exec identity must be valid and unprivileged")
	}
	return errors.New("final exec identity sealing is only implemented on Linux")
}
