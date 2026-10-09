package workspacecontext

import (
	"strings"
	"testing"
)

func TestProjectContextEnvelopeScopedAndComplete(t *testing.T) {
	s := Snapshot{Version: Version, WorkingDirectory: "src", Instructions: []Instruction{{Path: "AGENTS.md", Text: "Project guidance"}}, Skills: []Skill{{Name: "check", Description: "Check project", Path: ".agents/skills/check/SKILL.md"}}, Diagnostics: []Diagnostic{{Code: "skill_metadata_invalid", Path: "skills/invalid/SKILL.md"}}}
	raw, err := Encode("session-1", s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(raw, "session-1", "src")
	if err != nil || len(got.Instructions) != 1 || len(got.Skills) != 1 || len(got.Diagnostics) != 1 {
		t.Fatal("incomplete envelope", err)
	}
	for _, bad := range []string{strings.Replace(raw, "session-1", "session-2", 1), strings.Replace(raw, `"workingDirectory":"src"`, `"workingDirectory":"other"`, 1), raw + `{}`, strings.Replace(raw, "AGENTS.md", "../secret/AGENTS.md", 1)} {
		if _, err := Decode(bad, "session-1", "src"); err == nil {
			t.Fatal("unscoped context accepted")
		}
	}
}
