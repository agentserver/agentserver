//go:build linux

package networkguard

import (
	"errors"
	"fmt"
	"net/netip"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestInstallNormalizedReplacesBothFamiliesBeforeAtomicPublish(t *testing.T) {
	policies, err := normalize("agentserver_test", []UIDPolicy{{
		UID: 65531,
		AllowedEndpoints: []Endpoint{{
			Address: netip.MustParseAddr("127.0.0.1"),
			Port:    8443,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	call := 0
	err = installNormalized("agentserver_test", policies, func(commands []nftCommand) error {
		call++
		switch call {
		case 1:
			if len(commands) != 1 || commands[0].typeID != unix.NFT_MSG_DELTABLE || commands[0].family != unix.NFPROTO_IPV4 {
				t.Fatalf("first transaction = %+v", commands)
			}
			return fmt.Errorf("missing old table: %w", syscall.ENOENT)
		case 2:
			if len(commands) != 1 || commands[0].typeID != unix.NFT_MSG_DELTABLE || commands[0].family != unix.NFPROTO_IPV6 {
				t.Fatalf("second transaction = %+v", commands)
			}
			return nil
		case 3:
			if len(commands) < 4 || commands[0].typeID != unix.NFT_MSG_NEWTABLE || commands[0].family != unix.NFPROTO_IPV4 {
				t.Fatalf("publish transaction = %+v", commands)
			}
			return nil
		default:
			t.Fatalf("unexpected transaction %d", call)
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if call != 3 {
		t.Fatalf("transaction count = %d, want 3", call)
	}
}

func TestInstallNormalizedStopsBeforePublishWhenCleanupFails(t *testing.T) {
	call := 0
	want := syscall.EPERM
	err := installNormalized("agentserver_test", []UIDPolicy{{UID: 65531}}, func(commands []nftCommand) error {
		call++
		return fmt.Errorf("cleanup: %w", want)
	})
	if !errors.Is(err, want) || call != 1 {
		t.Fatalf("install error = %v, calls = %d", err, call)
	}
}
