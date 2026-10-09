package workspacecontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/workspaceauthority"
)

const InstructionsPrefix = "agentserver.repository-context/v1\n"
const MaxEnvelopeBytes = 1024 * 1024

type Envelope struct {
	CheckoutID string   `json:"checkoutId"`
	Context    Snapshot `json:"context"`
}

func (s Snapshot) Validate(cwd string) error {
	bad := errors.New("invalid project context snapshot")
	if s.Version != Version || s.WorkingDirectory != cwd || workspaceauthority.ValidateWorkingDirectory(cwd) != nil || len(s.Instructions) > maxAncestors || len(s.Skills) > MaxSkills || len(s.Diagnostics) > MaxSkills {
		return bad
	}
	bytes := 0
	seen := map[string]bool{}
	for _, i := range s.Instructions {
		dir := path.Dir(i.Path)
		if workspaceauthority.ValidateWorkingDirectory(i.Path) != nil || (path.Base(i.Path) != "AGENTS.md" && path.Base(i.Path) != "AGENTS.override.md") || !(dir == "." || dir == cwd || strings.HasPrefix(cwd, dir+"/")) || seen[dir] || !utf8.ValidString(i.Text) {
			return bad
		}
		seen[dir] = true
		bytes += len(i.Text)
	}
	if bytes > MaxInstructionBytes {
		return bad
	}
	seen = map[string]bool{}
	for _, s := range s.Skills {
		if s.Name == "" || len(s.Name) > 64 || s.Description == "" || len(s.Description) > 4096 || workspaceauthority.ValidateWorkingDirectory(s.Path) != nil || path.Base(s.Path) != "SKILL.md" || seen[s.Name] {
			return bad
		}
		seen[s.Name] = true
	}
	return nil
}

func Encode(checkoutID string, s Snapshot) (string, error) {
	if err := s.Validate(s.WorkingDirectory); err != nil {
		return "", err
	}
	raw, err := json.Marshal(Envelope{CheckoutID: checkoutID, Context: s})
	if err != nil || len(raw) > MaxEnvelopeBytes {
		return "", errors.New("project context exceeds envelope limit")
	}
	return InstructionsPrefix + string(raw), nil
}

func Decode(raw, checkoutID, cwd string) (Snapshot, error) {
	if len(raw) > MaxEnvelopeBytes+len(InstructionsPrefix) || !strings.HasPrefix(raw, InstructionsPrefix) {
		return Snapshot{}, errors.New("repository context preflight is missing or invalid")
	}
	d := json.NewDecoder(bytes.NewBufferString(strings.TrimPrefix(raw, InstructionsPrefix)))
	d.DisallowUnknownFields()
	var e Envelope
	if d.Decode(&e) != nil || e.CheckoutID != checkoutID {
		return Snapshot{}, errors.New("repository context identity mismatch")
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return Snapshot{}, errors.New("invalid repository context envelope")
	}
	if err := e.Context.Validate(cwd); err != nil {
		return Snapshot{}, err
	}
	return e.Context, nil
}
