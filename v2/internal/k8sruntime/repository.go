package k8sruntime

import (
	"context"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/v2/internal/repositorycheckout"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"github.com/agentserver/agentserver/v2/internal/workspacecontext"
	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

const (
	PrepareRepositoryPath = "/internal/runtime/repository/prepare"
	RepositoryContextPath = "/internal/runtime/repository/context"
)

// These are gateway-owned preflight requests, never model tool calls. The
// gateway must resolve them from the signed run and live Core authority before
// contacting this mTLS-only endpoint. No fake Codex turn/call IDs are required.
type PrepareRepositoryRequest struct {
	Session    sandboxcontract.SessionIdentity `json:"session"`
	Ref        sandboxcontract.SandboxRef      `json:"ref"`
	CheckoutID string                          `json:"checkoutId"`
	Source     workspacerepository.Source      `json:"source"`
	Credential *repositorycheckout.Credential  `json:"credential,omitempty"`
}
type RepositoryContextRequest struct {
	Session          sandboxcontract.SessionIdentity `json:"session"`
	Ref              sandboxcontract.SandboxRef      `json:"ref"`
	CheckoutID       string                          `json:"checkoutId"`
	WorkingDirectory string                          `json:"workingDirectory"`
}
type RepositoryState struct {
	CheckoutID string                    `json:"checkoutId"`
	Commit     string                    `json:"commit"`
	Created    bool                      `json:"created"`
	Context    workspacecontext.Snapshot `json:"context"`
}

func (s *Server) workspaceSource() string {
	if s.projectTree != "" {
		return s.projectTree
	}
	return s.config.Workspace
}

func (s *Server) repositoryBound(session sandboxcontract.SessionIdentity, ref sandboxcontract.SandboxRef) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.ready && s.bound != nil && s.bound.Session == session && s.bound.Ref == ref
}

func (s *Server) prepareRepository(w http.ResponseWriter, r *http.Request) {
	var input PrepareRepositoryRequest
	if !decode(w, r, &input) {
		return
	}
	defer func() {
		if input.Credential != nil {
			*input.Credential = repositorycheckout.Credential{}
		}
	}()
	if !s.repositoryBound(input.Session, input.Ref) {
		reject(w, "repository_fenced", 409)
		return
	}
	if s.checkout == nil {
		reject(w, "repository_storage_unavailable", 503)
		return
	}
	if !s.projectMu.TryLock() {
		reject(w, "repository_busy", 409)
		return
	}
	defer s.projectMu.Unlock()
	if !s.repositoryBound(input.Session, input.Ref) {
		reject(w, "repository_fenced", 409)
		return
	}
	// A runtime incarnation stays attached to one immutable checkout. Selecting
	// another repository needs a new runtime, leaving the prior files intact.
	if s.projectID != "" && s.projectID != input.CheckoutID {
		reject(w, "repository_binding_conflict", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		reject(w, "repository_fenced", 409)
		return
	}
	s.projectCancel = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.projectCancel = nil; s.mu.Unlock() }()
	result, err := s.checkout.Prepare(ctx, repositorycheckout.Request{CheckoutID: input.CheckoutID, Source: input.Source}, input.Credential)
	if err != nil {
		reject(w, "repository_preparation_failed", 422)
		return
	}
	snapshot, err := workspacecontext.Scan(ctx, result.Tree, input.Source.WorkingDirectory)
	if err != nil {
		reject(w, "repository_context_unavailable", 422)
		return
	}
	if !s.repositoryBound(input.Session, input.Ref) {
		reject(w, "repository_fenced", 409)
		return
	}
	s.projectID, s.projectTree, s.projectCommit = input.CheckoutID, result.Tree, result.Commit
	writeJSON(w, RepositoryState{CheckoutID: s.projectID, Commit: s.projectCommit, Created: result.Created, Context: snapshot})
}

func (s *Server) repositoryContext(w http.ResponseWriter, r *http.Request) {
	var input RepositoryContextRequest
	if !decode(w, r, &input) {
		return
	}
	if !s.repositoryBound(input.Session, input.Ref) {
		reject(w, "repository_fenced", 409)
		return
	}
	// Context is a point-in-time preflight read: no tool may mutate the files
	// while it is being assembled for the next turn.
	if !s.projectMu.TryLock() {
		reject(w, "repository_busy", 409)
		return
	}
	defer s.projectMu.Unlock()
	if s.projectID == "" || s.projectID != input.CheckoutID {
		reject(w, "repository_not_prepared", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	snapshot, err := workspacecontext.Scan(ctx, s.projectTree, input.WorkingDirectory)
	if err != nil {
		reject(w, "repository_context_unavailable", 422)
		return
	}
	if !s.repositoryBound(input.Session, input.Ref) {
		reject(w, "repository_fenced", 409)
		return
	}
	writeJSON(w, RepositoryState{CheckoutID: s.projectID, Commit: s.projectCommit, Context: snapshot})
}
