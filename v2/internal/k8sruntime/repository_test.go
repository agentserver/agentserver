package k8sruntime

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/repositorycheckout"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

func repositoryFixture(t *testing.T) (*Server, PrepareRepositoryRequest, string) {
	t.Helper()
	s := testServer(t)
	storage := t.TempDir()
	var err error
	s.checkout, err = repositorycheckout.New(storage, "/usr/bin/git")
	if err != nil {
		t.Fatal(err)
	}
	request := PrepareRepositoryRequest{Session: s.bound.Session, Ref: s.bound.Ref, CheckoutID: "50000000-0000-4000-8000-000000000005", Source: workspacerepository.Source{URL: "https://code.byted.org/tce/rtm-aihub.git", Ref: "main", WorkingDirectory: "."}}
	dir := filepath.Join(storage, request.CheckoutID)
	tree := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(tree, ".agents/skills/test"), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"version": 1, "checkoutId": request.CheckoutID, "url": request.Source.URL, "ref": "main", "commit": strings.Repeat("a", 40)})
	for name, data := range map[string][]byte{
		filepath.Join(dir, "metadata.json"):                 raw,
		filepath.Join(tree, "AGENTS.md"):                    []byte("project guidance"),
		filepath.Join(tree, ".agents/skills/test/SKILL.md"): []byte("---\nname: test\ndescription: Test project\n---\nfull body read on demand"),
	} {
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return s, request, tree
}

func TestRuntimeRepositoryPreflightAndFileProjection(t *testing.T) {
	s, request, tree := repositoryFixture(t)
	w := call(t, s, http.MethodPost, PrepareRepositoryPath, request, true)
	if w.Code != 200 {
		t.Fatalf("prepare=%d %s", w.Code, w.Body.String())
	}
	var result RepositoryState
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Created || result.CheckoutID != request.CheckoutID || len(result.Context.Instructions) != 1 || len(result.Context.Skills) != 1 || s.projectTree != tree {
		t.Fatalf("result=%+v", result)
	}
	if strings.Contains(w.Body.String(), tree) || strings.Contains(w.Body.String(), "full body") {
		t.Fatal("preflight leaked physical path or skill body")
	}
	contextRequest := RepositoryContextRequest{Session: request.Session, Ref: request.Ref, CheckoutID: request.CheckoutID, WorkingDirectory: "."}
	if err := os.WriteFile(filepath.Join(tree, "AGENTS.md"), []byte("updated project guidance"), 0600); err != nil {
		t.Fatal(err)
	}
	w = call(t, s, http.MethodPost, RepositoryContextPath, contextRequest, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "updated project guidance") {
		t.Fatal("stale context", w.Body.String())
	}
	command := testCommand(s)
	read := sandboxcontract.ReadFileRequest{Profile: sandboxcontract.ProfileV1, RequestID: "read-skill", Identity: command.Identity, Ref: command.Ref, Path: filepath.Join(s.config.Workspace, ".agents/skills/test/SKILL.md"), Limit: 4096}
	readPath, _ := sandboxcontract.ReadFilePath(read.Ref.SandboxID)
	fs := frames(t, call(t, s, http.MethodPost, readPath, read, true))
	var content strings.Builder
	for _, frame := range fs {
		if frame.Event != nil && frame.Event.Kind == executionbackend.EventFileBytes {
			content.Write(frame.Event.Data)
		}
	}
	if !strings.Contains(content.String(), "full body read on demand") {
		t.Fatal("executor read did not use prepared checkout")
	}
}

func TestRuntimeRepositoryFencesOtherSessionsAndConcurrentTools(t *testing.T) {
	s, request, _ := repositoryFixture(t)
	if w := call(t, s, http.MethodPost, PrepareRepositoryPath, request, false); w.Code != 403 {
		t.Fatal("unauthenticated preparation accepted")
	}
	other := request
	other.Session.SessionID = "other-session"
	if w := call(t, s, http.MethodPost, PrepareRepositoryPath, other, true); w.Code != 409 {
		t.Fatal("cross-session preparation accepted")
	}
	s.projectMu.RLock()
	w := call(t, s, http.MethodPost, PrepareRepositoryPath, request, true)
	s.projectMu.RUnlock()
	if w.Code != 409 || !strings.Contains(w.Body.String(), "repository_busy") {
		t.Fatal("prepared while tool was active")
	}
	if w := call(t, s, http.MethodPost, PrepareRepositoryPath, request, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	other = request
	other.CheckoutID = "60000000-0000-4000-8000-000000000006"
	if w := call(t, s, http.MethodPost, PrepareRepositoryPath, other, true); w.Code != 409 {
		t.Fatal("replaced an incarnation's checkout")
	}
	s.projectMu.Lock()
	command := testCommand(s)
	commandPath, _ := sandboxcontract.RunCommandPath(command.Ref.SandboxID)
	w = call(t, s, http.MethodPost, commandPath, command, true)
	s.projectMu.Unlock()
	if w.Code != 409 || !strings.Contains(w.Body.String(), "repository_busy") {
		t.Fatal("tool ran during project preparation")
	}
	// Rejection occurs before operation acceptance, so a later dispatch with
	// the same operation ID is not incorrectly marked as a replay.
	if _, exists := s.ops[command.Identity.OperationID]; exists {
		t.Fatal("busy preflight consumed tool operation")
	}
}
