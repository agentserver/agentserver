package coreserver

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/agentserver/agentserver/v2/internal/coredb"
)

var staticPolicyToolPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

// StaticUserRunPolicyResolver is a bootstrap policy source. The configured
// list is server-owned (never accepted from AG-UI) and its context digest is
// bound to the authorized actor/session. A database/policy service can replace
// this interface without changing CreateRun.
type StaticUserRunPolicyResolver struct {
	version string
	tools   []string
}

func NewStaticUserRunPolicyResolver(version string, allowedTools []string) (*StaticUserRunPolicyResolver, error) {
	if version == "" || len(version) > 128 || strings.TrimSpace(version) != version || strings.ContainsAny(version, "\x00\r\n") {
		return nil, errors.New("static user run policy version must be bounded canonical text")
	}
	tools := append([]string(nil), allowedTools...)
	slices.Sort(tools)
	for index, tool := range tools {
		if !staticPolicyToolPattern.MatchString(tool) || (index > 0 && tools[index-1] == tool) {
			return nil, errors.New("static user run policy tools must be unique canonical names")
		}
	}
	if len(tools) > coredb.MaxRunLaunchAllowedTools {
		return nil, errors.New("static user run policy contains too many tools")
	}
	return &StaticUserRunPolicyResolver{version: version, tools: tools}, nil
}

func (resolver *StaticUserRunPolicyResolver) ResolveUserRunPolicy(ctx context.Context, session coredb.AuthorizedSession) (coredb.RunExecutorPolicy, error) {
	if resolver == nil || resolver.version == "" {
		return coredb.RunExecutorPolicy{}, errors.New("static user run policy resolver is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return coredb.RunExecutorPolicy{}, err
	}
	hasher := sha256.New()
	_, _ = io.WriteString(hasher, "agentserver-v2/static-user-run-policy/v1\x00")
	for _, value := range []string{resolver.version, session.WorkspaceID, session.SessionID, session.ActorID} {
		_, _ = io.WriteString(hasher, value)
		_, _ = hasher.Write([]byte{0})
	}
	for _, tool := range resolver.tools {
		_, _ = io.WriteString(hasher, tool)
		_, _ = hasher.Write([]byte{0})
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	return coredb.RunExecutorPolicy{
		Version: resolver.version, ContextDigest: digest, AllowedTools: append([]string(nil), resolver.tools...),
	}, nil
}
