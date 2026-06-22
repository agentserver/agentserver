package imbridgesvc

import "testing"

func TestIsValidRoutingMode(t *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{"nanoclaw", true},
		{"codex", true},
		{"managed_cc", true},
		{"stateless_cc", false}, // removed in #135 purge
		{"", false},
		{"unknown", false},
		{"MANAGED_CC", false}, // case-sensitive
	}
	for _, tc := range cases {
		if got := isValidRoutingMode(tc.mode); got != tc.want {
			t.Errorf("isValidRoutingMode(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}
