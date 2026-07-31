package coreserver

import (
	"slices"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestStaticUserRunPolicyIsServerOwnedSortedAndScopeBound(t *testing.T) {
	resolver, err := NewStaticUserRunPolicyResolver("executor-policy/bootstrap-v1", []string{"shell", "read_file"})
	if err != nil {
		t.Fatal(err)
	}
	session := coredb.AuthorizedSession{WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID}
	first, err := resolver.ResolveUserRunPolicy(t.Context(), session)
	if err != nil || !slices.Equal(first.AllowedTools, []string{"read_file", "shell"}) || first.ContextDigest == ([32]byte{}) {
		t.Fatalf("policy = %+v, %v", first, err)
	}
	second, _ := resolver.ResolveUserRunPolicy(t.Context(), session)
	if second.ContextDigest != first.ContextDigest {
		t.Fatal("same policy scope produced a different digest")
	}
	session.ActorID = "40000000-0000-4000-8000-000000000099"
	other, _ := resolver.ResolveUserRunPolicy(t.Context(), session)
	if other.ContextDigest == first.ContextDigest {
		t.Fatal("different actor produced the same policy context digest")
	}
	first.AllowedTools[0] = "changed"
	again, _ := resolver.ResolveUserRunPolicy(t.Context(), coredb.AuthorizedSession{WorkspaceID: userRunWorkspaceID, SessionID: userRunSessionID, ActorID: userRunActorID})
	if again.AllowedTools[0] != "read_file" {
		t.Fatal("returned policy mutated resolver state")
	}
}

func TestStaticUserRunPolicyRejectsDuplicateOrNoncanonicalTools(t *testing.T) {
	for _, tools := range [][]string{{"shell", "shell"}, {"executor.shell"}, {"Shell"}} {
		if _, err := NewStaticUserRunPolicyResolver("policy/1", tools); err == nil {
			t.Fatalf("unsafe tools %q were accepted", tools)
		}
	}
}
