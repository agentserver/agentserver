package auth

import (
	"os"
	"testing"
)

func TestSafeNext(t *testing.T) {
	t.Setenv("AGENTSERVER_COOKIE_DOMAINS", "agent.cs.ac.cn,platform.agentserver.dev")

	const cnHost = "platform.agent.cs.ac.cn"

	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/", "/"},
		{"/codex-auth/codex/device", "/codex-auth/codex/device"},
		{"/path?q=1&r=2", "/path?q=1&r=2"},

		{"//evil.com/path", ""}, // protocol-relative
		{"https://evil.com/foo", ""},
		{"http://codex-auth.agent.cs.ac.cn/x", ""}, // wrong scheme
		{"javascript:alert(1)", ""},
		{"https://agent.cs.ac.cn.evil.com/x", ""}, // suffix-attack

		{"https://codex-auth.agent.cs.ac.cn/codex/device", "https://codex-auth.agent.cs.ac.cn/codex/device"},
		{"https://agent.cs.ac.cn/x", "https://agent.cs.ac.cn/x"},

		// Cross-tree bounce: CN host trying to send user to overseas domain
		// — both are configured cookie domains, but they live in different
		// trees, so this must be rejected.
		{"https://platform.agentserver.dev/x", ""},

		{"/x\r\nSet-Cookie: a=b", ""}, // CRLF injection
	}
	for _, c := range cases {
		if got := safeNext(cnHost, c.in); got != c.want {
			t.Errorf("safeNext(%q, %q) = %q, want %q", cnHost, c.in, got, c.want)
		}
	}

	// From the overseas host, the overseas absolute URL is allowed and CN is not.
	const overseasHost = "platform.agentserver.dev"
	if got := safeNext(overseasHost, "https://platform.agentserver.dev/x"); got != "https://platform.agentserver.dev/x" {
		t.Errorf("overseas same-host absolute should pass, got %q", got)
	}
	if got := safeNext(overseasHost, "https://codex-auth.agent.cs.ac.cn/x"); got != "" {
		t.Errorf("overseas → CN absolute must be rejected, got %q", got)
	}

	// No cookie domain → absolute URLs rejected even if scheme/host look ok.
	os.Unsetenv("AGENTSERVER_COOKIE_DOMAINS")
	if got := safeNext(cnHost, "https://codex-auth.agent.cs.ac.cn/x"); got != "" {
		t.Errorf("expected absolute URL rejected without cookieDomain, got %q", got)
	}
	if got := safeNext(cnHost, "/relative"); got != "/relative" {
		t.Errorf("relative path should still work without cookieDomain, got %q", got)
	}
}
