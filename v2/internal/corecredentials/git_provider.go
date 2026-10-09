package corecredentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"

	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

const (
	GitUsernameEnvironment = "AGENTSERVER_GIT_USERNAME"
	GitTokenEnvironment    = "AGENTSERVER_GIT_TOKEN"
)

type GitProvider struct{}
type gitSecret struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

func NewGitProvider() GitProvider            { return GitProvider{} }
func (GitProvider) Kind() string             { return "git" }
func (GitProvider) AllowedHeaders() []string { return []string{} }
func (GitProvider) AllowedEnvironment() []string {
	return []string{GitUsernameEnvironment, GitTokenEnvironment}
}
func (p GitProvider) Schema() ProviderSchema {
	return ProviderSchema{Kind: p.Kind(), DisplayName: "Git / Codebase", AuthTypes: []string{"https-token"}, AllowedHosts: []string{workspacerepository.GitHost}, AllowedHeaders: []string{}, SecretFormat: "json-host-username-token", AuthorizationMethods: []string{AuthorizationMethodManual}}
}
func (GitProvider) ValidateUpload(authType string, raw []byte) (UploadResult, error) {
	if authType != "https-token" {
		return UploadResult{}, errors.New("Git credentials require https-token authentication")
	}
	s, err := parseGitSecret(raw)
	if err != nil {
		return UploadResult{}, err
	}
	public, _ := json.Marshal(map[string]string{"host": s.Host, "username": s.Username})
	return UploadResult{AuthType: authType, PublicMetadata: public, Secret: append([]byte(nil), raw...)}, nil
}
func (p GitProvider) Materialize(_ context.Context, b Binding, raw []byte, r UseRequest) (HeaderMutation, error) {
	if b.Kind != p.Kind() || b.AuthType != "https-token" || r.Method != "PROCESS_ENV" {
		return HeaderMutation{}, errors.New("Git credentials are available only to repository preparation")
	}
	s, err := parseGitSecret(raw)
	if err != nil {
		return HeaderMutation{}, err
	}
	if r.Host != s.Host {
		return HeaderMutation{}, errors.New("Git credential host does not match repository")
	}
	return HeaderMutation{Environment: map[string]string{GitUsernameEnvironment: s.Username, GitTokenEnvironment: s.Token}}, nil
}
func parseGitSecret(raw []byte) (gitSecret, error) {
	var s gitSecret
	invalid := errors.New("invalid Git credential envelope")
	if len(raw) == 0 || len(raw) > 16*1024 {
		return s, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil {
		return gitSecret{}, invalid
	}
	var trailing any
	if !errors.Is(d.Decode(&trailing), io.EOF) {
		return gitSecret{}, invalid
	}
	if s.Host != workspacerepository.GitHost || s.Username == "" || len(s.Username) > 256 || strings.ContainsRune(s.Username, ':') || strings.IndexFunc(s.Username, unicode.IsControl) >= 0 || strings.TrimSpace(s.Username) != s.Username || !validOpaqueToken(s.Token, 8192) {
		return gitSecret{}, invalid
	}
	return s, nil
}
