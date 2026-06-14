package codexappgateway

import "testing"

// TestIsLoopbackRemote pins the IP guard used by the remaining
// loopback handler (handleInternalScheduledTask). The
// /internal/connected handler was removed 2026-06-14 when env-mcp
// started calling codex-exec-gateway directly with the workspace
// cap-token; the connected-handler-specific tests went with it.
func TestIsLoopbackRemote(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:54321", true},
		{"127.5.5.5:1", true},
		{"[::1]:1234", true},
		{"10.0.0.1:8080", false},
		{"192.168.0.1:80", false},
		{"[fe80::1]:80", false},
		{"garbage", false},
	}
	for _, c := range cases {
		if got := isLoopbackRemote(c.addr); got != c.want {
			t.Errorf("isLoopbackRemote(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}
