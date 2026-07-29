//go:build !linux

package networkguard

import "errors"

func Install(tableName string, policies []UIDPolicy) error {
	if _, err := normalize(tableName, policies); err != nil {
		return err
	}
	return errors.New("nftables UID egress policy is only implemented on Linux")
}
