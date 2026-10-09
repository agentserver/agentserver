package workspacerepository

import (
	"strings"
	"testing"
)

func TestNormalizeRepositoryURL(t *testing.T) {
	for _, raw := range []string{"https://code.byted.org/tce/rtm-aihub", "https://code.byted.org/tce/rtm-aihub.git"} {
		got, err := NormalizeRepositoryURL(raw)
		if err != nil || got != "https://code.byted.org/tce/rtm-aihub.git" {
			t.Fatalf("normalize %q = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{
		"http://code.byted.org/tce/rtm-aihub", "https://user:secret@code.byted.org/tce/rtm-aihub",
		"https://code.byted.org/tce/../private", "https://code.byted.org/tce/%2e%2e/private",
		"https://code.byted.org/tce/rtm-aihub?token=secret", "https://code.byted.org/tce/rtm-aihub#ref",
		"https://code.byted.org:8443/tce/rtm-aihub", "ssh://code.byted.org/tce/rtm-aihub",
		"https://127.0.0.1/tce/rtm-aihub", "https://other.example/tce/rtm-aihub", "https://code.byted.org/tce//rtm-aihub",
		"https://code.byted.org/tce/rtm-aihub/tree/main", "https://code.byted.org/tce/-option", "https://code.byted.org/tce/repo\n",
	} {
		if _, err := NormalizeRepositoryURL(raw); err == nil {
			t.Errorf("accepted unsafe/ambiguous URL %q", raw)
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatal("URL error disclosed credential")
		}
	}
}

func TestSourceValidation(t *testing.T) {
	valid := Source{URL: "https://code.byted.org/tce/rtm-aihub.git", WorkingDirectory: ".", CredentialBindingID: "90000000-0000-4000-8000-000000000009"}
	for _, ref := range []string{"", "HEAD", "main", "feature/workspace", "refs/tags/v1.2", strings.Repeat("a", 40)} {
		source := valid
		source.Ref = ref
		if err := source.Validate(); err != nil {
			t.Fatalf("valid ref %q: %v", ref, err)
		}
	}
	for _, ref := range []string{"-c", "main..branch", "main@{1}", "HEAD~1", "refs//main", "main.lock", "refs/heads/", "ref\nother", "refs/.hidden", "main:other"} {
		source := valid
		source.Ref = ref
		if source.Validate() == nil {
			t.Errorf("accepted ref %q", ref)
		}
	}
	for _, dir := range []string{"..", "/workspace/project", "src/../other", "src\\other", "https://code.byted.org/tce/rtm-aihub"} {
		source := valid
		source.WorkingDirectory = dir
		if source.Validate() == nil {
			t.Errorf("accepted directory %q", dir)
		}
	}
	valid.WorkingDirectory = "services/api"
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}
