package sessiontitle

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTitleBoundsAndUntrustedInput(t *testing.T) {
	for _, text := range []string{"你好呀，你都能做啥", strings.Repeat("中文", 1000), "\n\t hello \u202e world!\x00"} {
		fallback := Fallback(text)
		if err := fallback.Validate(); err != nil {
			t.Fatal(err, fallback)
		}
		prompt := Prompt(text)
		if len(prompt) > MaxPromptBytes || !utf8.ValidString(prompt) {
			t.Fatal("invalid bounded prompt")
		}
	}
	for _, raw := range []string{`{"title":""}`, `{"title":null}`, `{"title":"valid","command":"run shell"}`, `not json`, strings.Repeat("x", 9000)} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("accepted invalid response %q", raw)
		}
	}
	p, err := Parse(`{"title":"  \"检查权限切换！\" "}`)
	if err != nil || p.Title != "检查权限切换！" || p.Source != "generated" {
		t.Fatalf("parse: %+v %v", p, err)
	}
}
