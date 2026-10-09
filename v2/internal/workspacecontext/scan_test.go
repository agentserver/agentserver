package workspacecontext

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"
)

func put(t *testing.T, root, name, contents string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
func skill(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\nThis is a private skill body, not a preflight index.\n"
}

func TestScanRemoteProjectContext(t *testing.T) {
	root := t.TempDir()
	put(t, root, "AGENTS.md", "root instructions")
	put(t, root, "services/AGENTS.md", "masked")
	put(t, root, "services/AGENTS.override.md", "service instructions")
	put(t, root, "services/api/AGENTS.override.md", "  \n")
	put(t, root, "services/api/AGENTS.md", "api instructions")
	put(t, root, "services/other/AGENTS.md", "not an ancestor")
	put(t, root, ".agents/skills/review/SKILL.md", skill("review", "root review"))
	put(t, root, "services/.agents/skills/review/SKILL.md", skill("review", "service review"))
	put(t, root, "services/api/skills/test/SKILL.md", skill("test", "legacy test"))
	put(t, root, "services/api/.agents/skills/test/SKILL.md", skill("test", "canonical test"))
	put(t, root, ".agents/skills/group/nested/SKILL.md", skill("nested", "multi-level skill"))
	put(t, root, "services/other/.agents/skills/other/SKILL.md", skill("other", "out of scope"))
	put(t, root, "random/SKILL.md", skill("random", "not a skill root"))
	got, err := Scan(t.Context(), root, "services/api")
	if err != nil {
		t.Fatal(err)
	}
	want := []Instruction{{"AGENTS.md", "root instructions"}, {"services/AGENTS.override.md", "service instructions"}, {"services/api/AGENTS.md", "api instructions"}}
	if !reflect.DeepEqual(got.Instructions, want) {
		t.Fatalf("instructions=%+v", got.Instructions)
	}
	if len(got.Skills) != 3 || got.Skills[0].Name != "nested" || got.Skills[1].Description != "service review" || got.Skills[2].Description != "canonical test" {
		t.Fatalf("skills=%+v", got.Skills)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "private skill body") || strings.Contains(string(raw), root) || strings.Contains(string(raw), "not an ancestor") {
		t.Fatal("context leaked body, host path or unrelated data")
	}
	again, err := Scan(t.Context(), root, "services/api")
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("context is nondeterministic")
	}
	put(t, root, "AGENTS.md", "new instructions")
	fresh, err := Scan(t.Context(), root, "services/api")
	if err != nil || fresh.Instructions[0].Text != "new instructions" {
		t.Fatal("scan cached stale instructions")
	}
}

func TestContextDoesNotEscapeRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	put(t, outside, "private.md", "service credential must never be read")
	if err := os.Symlink(filepath.Join(outside, "private.md"), filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(t.Context(), root, "."); err == nil || strings.Contains(err.Error(), outside) {
		t.Fatalf("escaping instruction err=%v", err)
	}
	if err := os.Remove(filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	put(t, root, ".agents/skills/escape/placeholder", "")
	if err := os.Symlink(filepath.Join(outside, "private.md"), filepath.Join(root, ".agents/skills/escape/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Scan(t.Context(), root, ".")
	if err != nil || len(snapshot.Skills) != 0 || len(snapshot.Diagnostics) != 1 || snapshot.Diagnostics[0].Code != "skill_manifest_unavailable" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if err := os.Mkdir(filepath.Join(root, "actual"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("actual", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{"alias", "../escape", "/absolute", "missing"} {
		if _, err := Scan(t.Context(), root, cwd); err == nil {
			t.Fatalf("accepted cwd=%s", cwd)
		}
	}
}

func TestContextInstructionLimitAndUTF8(t *testing.T) {
	root := t.TempDir()
	put(t, root, "AGENTS.md", strings.Repeat("文", MaxInstructionBytes))
	put(t, root, "child/AGENTS.md", "child instructions")
	out, err := Scan(t.Context(), root, "child")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, i := range out.Instructions {
		total += len(i.Text)
		if !utf8.ValidString(i.Text) {
			t.Fatal("split UTF-8")
		}
	}
	if total > MaxInstructionBytes || total < MaxInstructionBytes-3 || len(out.Diagnostics) != 2 {
		t.Fatalf("bytes=%d warnings=%+v", total, out.Diagnostics)
	}
}

func TestScanCancellationAndSkillMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Scan(ctx, t.TempDir(), "."); err != context.Canceled {
		t.Fatalf("cancel=%v", err)
	}
	for _, raw := range []string{
		"no frontmatter", "---\nname: a\n---\n", "---\nname: a\nname: b\ndescription: test\n---\n",
		"---\nname: a\ndescription: '  '\n---\n",
		"---\nname: 123\ndescription: true\n---\n",
	} {
		if _, err := parseSkill([]byte(raw), "SKILL.md"); err == nil {
			t.Fatalf("accepted invalid skill %q", raw)
		}
	}
	out, err := parseSkill([]byte("---\nname: test\ndescription: >\n  first line\n  second line\nmetadata:\n  category: test\n---\nbody"), ".agents/skills/test/SKILL.md")
	if err != nil || out.Description != "first line second line" {
		t.Fatalf("folded YAML=%+v err=%v", out, err)
	}
}

func TestScanContainedSkillLinksAndCycles(t *testing.T) {
	root := t.TempDir()
	put(t, root, "shared/test/SKILL.md", skill("test", "linked skill"))
	put(t, root, "shared/test/templates/example/SKILL.md", skill("template", "must not be indexed"))
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../shared", filepath.Join(root, ".agents/skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", filepath.Join(root, "shared/loop")); err != nil {
		t.Fatal(err)
	}
	out, err := Scan(t.Context(), root, ".")
	if err != nil || len(out.Skills) != 1 || out.Skills[0].Path != ".agents/skills/test/SKILL.md" {
		t.Fatalf("links=%+v err=%v", out, err)
	}
	if len(out.Diagnostics) != 1 || out.Diagnostics[0].Code != "skill_symlink_cycle" {
		t.Fatalf("warnings=%+v", out.Diagnostics)
	}
}

func TestContextRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "AGENTS.md"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(t.Context(), root, "."); err == nil {
		t.Fatal("accepted FIFO instruction")
	}
}

// Opt-in read-only smoke test against a local checkout of a real project. It
// prints only the skill index, never instruction bodies or repository files.
func TestWorkspaceContextLocalProject(t *testing.T) {
	root := os.Getenv("AGENTSERVER_TEST_WORKSPACE_CONTEXT_ROOT")
	if root == "" {
		t.Skip("no local project fixture selected")
	}
	out, err := Scan(t.Context(), root, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Skills) == 0 {
		t.Fatal("project fixture has no discoverable skills")
	}
	for _, s := range out.Skills {
		t.Logf("skill %s at %s", s.Name, s.Path)
	}
	for _, d := range out.Diagnostics {
		t.Logf("diagnostic %s at %s", d.Code, d.Path)
	}
}
