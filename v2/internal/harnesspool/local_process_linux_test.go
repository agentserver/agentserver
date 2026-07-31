//go:build linux

package harnesspool

import (
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLocalAttemptSysProcAttributesDelegateOnlyIdentityCapabilities(t *testing.T) {
	development := localAttemptSysProcAttributes(false)
	if len(development.AmbientCaps) != 0 {
		t.Fatalf("unprivileged development ambient capabilities = %v", development.AmbientCaps)
	}
	privileged := localAttemptSysProcAttributes(true)
	want := []uintptr{unix.CAP_SETGID, unix.CAP_SETUID}
	if !slices.Equal(privileged.AmbientCaps, want) {
		t.Fatalf("privileged worker ambient capabilities = %v, want %v", privileged.AmbientCaps, want)
	}
}
