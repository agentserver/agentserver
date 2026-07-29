package networkguard

import (
	"net/netip"
	"strings"
	"testing"
)

func TestNormalizeRejectsUnsafePoliciesAndCanonicalizesEndpoints(t *testing.T) {
	valid := []UIDPolicy{{
		UID: 65532,
		AllowedEndpoints: []Endpoint{
			{Address: netip.MustParseAddr("127.0.0.2"), Port: 8443},
			{Address: netip.MustParseAddr("127.0.0.1"), Port: 8080},
			{Address: netip.MustParseAddr("127.0.0.1"), Port: 8080},
		},
	}}
	normalized, err := normalize("agentserver_test", valid)
	if err != nil {
		t.Fatal(err)
	}
	want := []Endpoint{
		{Address: netip.MustParseAddr("127.0.0.1"), Port: 8080},
		{Address: netip.MustParseAddr("127.0.0.2"), Port: 8443},
	}
	if len(normalized) != 1 || len(normalized[0].AllowedEndpoints) != len(want) {
		t.Fatalf("normalized policies = %+v, want endpoints %+v", normalized, want)
	}
	for index := range want {
		if normalized[0].AllowedEndpoints[index] != want[index] {
			t.Fatalf("normalized endpoint %d = %+v, want %+v", index, normalized[0].AllowedEndpoints[index], want[index])
		}
	}

	tests := []struct {
		name     string
		table    string
		policies []UIDPolicy
		want     string
	}{
		{name: "table", table: "Bad-Table", policies: valid, want: "table name"},
		{name: "root", table: "safe", policies: []UIDPolicy{{UID: 0}}, want: "privileged"},
		{name: "reserved uid", table: "safe", policies: []UIDPolicy{{UID: ^uint32(0)}}, want: "invalid"},
		{name: "duplicate uid", table: "safe", policies: []UIDPolicy{{UID: 9}, {UID: 9}}, want: "duplicate"},
		{name: "IPv6", table: "safe", policies: []UIDPolicy{{UID: 9, AllowedEndpoints: []Endpoint{{Address: netip.IPv6Loopback(), Port: 80}}}}, want: "IPv4"},
		{name: "port", table: "safe", policies: []UIDPolicy{{UID: 9, AllowedEndpoints: []Endpoint{{Address: netip.MustParseAddr("127.0.0.1")}}}}, want: "port zero"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalize(test.table, test.policies); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("normalize() error = %v, want %q", err, test.want)
			}
		})
	}
}
