package repositorycheckout

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

func testRequest() Request {
	return Request{CheckoutID: "50000000-0000-4000-8000-000000000005", Source: workspacerepository.Source{URL: "https://code.byted.org/tce/rtm-aihub.git", Ref: "main", WorkingDirectory: "src", CredentialBindingID: "60000000-0000-4000-8000-000000000006"}}
}

func TestCheckoutResumesDirtySessionWithoutGitOrCredentials(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git not installed")
	}
	remote := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = remote
		cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git %v: %v %s", args, err, out)
		}
	}
	run("init", "--quiet", "--initial-branch=main", "--template=")
	if err := os.Mkdir(filepath.Join(remote, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "src/file.txt"), []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "src/file.txt")
	run("commit", "--quiet", "-m", "fixture")
	storage := t.TempDir()
	m, err := New(storage, git)
	if err != nil {
		t.Fatal(err)
	}
	var operations []string
	m.run = func(ctx context.Context, dir string, args, env []string) ([]byte, error) {
		operations = append(operations, args[0])
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "test-token") || strings.Contains(joined, "Authorization") {
			t.Fatal("secret in argv")
		}
		hasCredential := false
		for _, v := range env {
			if strings.HasPrefix(v, "GIT_CONFIG_VALUE_0=") {
				hasCredential = true
			}
		}
		if hasCredential != (args[0] == "fetch") {
			t.Fatalf("credentials attached to %s", args[0])
		}
		// Production validates Codebase HTTPS. Redirect only the test fetch to
		// a local fixture, retaining the same real init/fetch/checkout behavior.
		copy := append([]string(nil), args...)
		if args[0] == "fetch" {
			for i, v := range copy {
				if v == testRequest().Source.URL {
					copy[i] = "file://" + filepath.ToSlash(remote)
				}
			}
		}
		return m.runGit(ctx, dir, append([]string{"-c", "protocol.file.allow=always"}, copy...), env)
	}
	request := testRequest()
	created, err := m.Prepare(t.Context(), request, &Credential{Username: "git-user", Token: "test-token"})
	if err != nil || !created.Created || !commitID.MatchString(created.Commit) {
		t.Fatalf("prepare=%+v err=%v", created, err)
	}
	if !reflect.DeepEqual(operations, []string{"init", "fetch", "rev-parse", "checkout", "remote"}) {
		t.Fatalf("operations=%v", operations)
	}
	config, err := os.ReadFile(filepath.Join(created.Tree, ".git/config"))
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("git-user:test-token"))
	if strings.Contains(string(config), "test-token") || strings.Contains(string(config), encoded) || strings.Contains(string(config), "extraHeader") {
		t.Fatal("credential persisted in Git config")
	}
	file := filepath.Join(created.Tree, "src/file.txt")
	if err := os.WriteFile(file, []byte("user changes"), 0600); err != nil {
		t.Fatal(err)
	}
	// An existing repository may contain an arbitrary user-edited git config.
	// Resume must not execute that configuration, with or without a token.
	if err := os.WriteFile(filepath.Join(created.Tree, ".git/config"), []byte("deliberately invalid git config"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(storage, git)
	if err != nil {
		t.Fatal(err)
	}
	restarted.run = func(context.Context, string, []string, []string) ([]byte, error) {
		t.Fatal("resume ran Git")
		return nil, nil
	}
	resumed, err := restarted.Prepare(t.Context(), request, nil)
	if err != nil || resumed.Created || resumed.Tree != created.Tree || resumed.Commit != created.Commit {
		t.Fatalf("resume=%+v err=%v", resumed, err)
	}
	contents, _ := os.ReadFile(file)
	if string(contents) != "user changes" {
		t.Fatal("dirty session changes lost")
	}
	request.Source.Ref = "other"
	if _, err := restarted.Prepare(t.Context(), request, nil); err == nil {
		t.Fatal("changed ref silently reused existing tree")
	}
}

func TestCheckoutFailureIsSanitizedAndDoesNotPublish(t *testing.T) {
	m, err := New(t.TempDir(), "/usr/bin/git")
	if err != nil {
		t.Fatal(err)
	}
	m.run = func(context.Context, string, []string, []string) ([]byte, error) {
		return nil, errors.New("server echoed private-test-token")
	}
	_, err = m.Prepare(t.Context(), testRequest(), &Credential{Username: "user", Token: "private-test-token"})
	if err == nil || strings.Contains(err.Error(), "private-test-token") {
		t.Fatalf("unsafe error=%v", err)
	}
	entries, err := os.ReadDir(m.storage)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed checkout left files: %v %v", entries, err)
	}
	r := testRequest()
	r.CheckoutID = "../escape"
	if _, err := m.Prepare(t.Context(), r, nil); err == nil {
		t.Fatal("accepted traversal")
	}
}

func TestCheckoutRejectsUnboundCredentials(t *testing.T) {
	m, err := New(t.TempDir(), "/usr/bin/git")
	if err != nil {
		t.Fatal(err)
	}
	m.run = func(context.Context, string, []string, []string) ([]byte, error) {
		t.Fatal("invalid authority launched Git")
		return nil, nil
	}
	r := testRequest()
	if _, err := m.Prepare(t.Context(), r, nil); err == nil {
		t.Fatal("private clone without binding")
	}
	r.Source.CredentialBindingID = ""
	if _, err := m.Prepare(t.Context(), r, &Credential{Username: "user", Token: "secret"}); err == nil {
		t.Fatal("secret attached to unauthenticated repository")
	}
}
