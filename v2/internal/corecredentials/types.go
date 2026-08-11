// Package corecredentials contains the provider-neutral credential materializer
// embedded in v2 Core. It is an internal library, not a deployed process or an
// HTTP data-plane service. Callers present an operation-bound use request and
// receive only a closed-world header mutation.
package corecredentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

const (
	StatusActive         = "active"
	StatusReauthRequired = "reauth_required"
	StatusRevoked        = "revoked"
	StatusDisabled       = "disabled"

	OwnerScopeWorkspace = "workspace"
	OwnerScopeUser      = "user"

	maximumKindBytes        = 128
	maximumBindingIDBytes   = 256
	maximumWorkspaceIDBytes = 256
	maximumHostBytes        = 512
	maximumPathBytes        = 4096
	maximumHeaderValueBytes = 32 * 1024
)

var (
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
	digestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Binding is the provider-neutral representation read by corecredentials.
// SealedSecret is ciphertext and must never be serialized in an API response.
type Binding struct {
	ID                string
	WorkspaceID       string
	Kind              string
	DisplayName       string
	OwnerScope        string
	OwnerUserID       string
	PublicMetadata    json.RawMessage
	AuthType          string
	SealedSecret      []byte
	AuthorityVersion  int64
	CredentialVersion int64
	Status            string
	IsDefault         bool
	AccessExpiresAt   time.Time
	RefreshExpiresAt  *time.Time
}

// BindingMetadata is safe to return to Platform/UI clients.
type BindingMetadata struct {
	ID                string          `json:"id"`
	WorkspaceID       string          `json:"workspaceId"`
	Kind              string          `json:"kind"`
	DisplayName       string          `json:"displayName"`
	OwnerScope        string          `json:"ownerScope"`
	OwnerUserID       string          `json:"ownerUserId,omitempty"`
	PublicMetadata    json.RawMessage `json:"publicMetadata"`
	AuthType          string          `json:"authType"`
	AuthorityVersion  int64           `json:"authorityVersion"`
	CredentialVersion int64           `json:"credentialVersion"`
	Status            string          `json:"status"`
	IsDefault         bool            `json:"isDefault"`
	AccessExpiresAt   *time.Time      `json:"accessExpiresAt,omitempty"`
	RefreshExpiresAt  *time.Time      `json:"refreshExpiresAt,omitempty"`
}

func (binding Binding) Metadata() BindingMetadata {
	var access, refresh *time.Time
	if !binding.AccessExpiresAt.IsZero() {
		value := binding.AccessExpiresAt.UTC()
		access = &value
	}
	if binding.RefreshExpiresAt != nil && !binding.RefreshExpiresAt.IsZero() {
		value := binding.RefreshExpiresAt.UTC()
		refresh = &value
	}
	return BindingMetadata{
		ID: binding.ID, WorkspaceID: binding.WorkspaceID, Kind: binding.Kind,
		DisplayName: binding.DisplayName, OwnerScope: binding.OwnerScope, OwnerUserID: binding.OwnerUserID,
		PublicMetadata: cloneJSON(binding.PublicMetadata), AuthType: binding.AuthType,
		AuthorityVersion: binding.AuthorityVersion, CredentialVersion: binding.CredentialVersion,
		Status: binding.Status, IsDefault: binding.IsDefault, AccessExpiresAt: access, RefreshExpiresAt: refresh,
	}
}

// UseRequest is the only input accepted by the Core materialization frontend.
// Placeholder is opaque to the provider and is never a real upstream secret.
type UseRequest struct {
	Placeholder               string            `json:"placeholder"`
	CapabilityID              string            `json:"-"`
	WorkspaceID               string            `json:"workspaceId"`
	SessionID                 string            `json:"sessionId"`
	ActorID                   string            `json:"actorId"`
	EnvironmentID             string            `json:"environmentId"`
	RunID                     string            `json:"runId"`
	RunAttemptID              string            `json:"runAttemptId"`
	RunAttemptGeneration      int64             `json:"runAttemptGeneration"`
	ExecutionID               string            `json:"executionId"`
	OperationID               string            `json:"operationId"`
	SandboxID                 string            `json:"sandboxId"`
	TargetGeneration          int64             `json:"targetGeneration"`
	ProviderKind              string            `json:"providerKind"`
	BindingID                 string            `json:"bindingId"`
	AuthorityVersion          int64             `json:"authorityVersion"`
	ExpectedCredentialVersion int64             `json:"-"`
	CredentialMode            string            `json:"-"`
	PolicySHA256              string            `json:"policySha256"`
	TAEPSM                    string            `json:"taePsm"`
	Host                      string            `json:"host"`
	Path                      string            `json:"path"`
	Method                    string            `json:"method"`
	Headers                   map[string]string `json:"headers,omitempty"`
	ApprovalProof             string            `json:"approvalProof,omitempty"`
}

// AuthorityRequest is the pre-placeholder form used by execution gateway to
// select a workspace binding. It intentionally has no binding ID or secret;
// Core chooses the active default and returns only a versioned reference.
type AuthorityRequest struct {
	WorkspaceID          string
	SessionID            string
	ActorID              string
	EnvironmentID        string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	ExecutionID          string
	OperationID          string
	SandboxID            string
	TargetGeneration     int64
	ProviderKind         string
	PolicySHA256         string
}

func (request AuthorityRequest) Validate() error {
	for name, value := range map[string]string{
		"workspaceId": request.WorkspaceID, "sessionId": request.SessionID, "actorId": request.ActorID,
		"environmentId": request.EnvironmentID, "runId": request.RunID, "runAttemptId": request.RunAttemptID,
		"executionId": request.ExecutionID, "operationId": request.OperationID, "sandboxId": request.SandboxID,
		"providerKind": request.ProviderKind, "policySha256": request.PolicySHA256,
	} {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("credential authority %s is required", name)
		}
	}
	if !identifierPattern.MatchString(request.ProviderKind) || !digestPattern.MatchString(request.PolicySHA256) {
		return errors.New("credential authority provider or policy is invalid")
	}
	if request.RunAttemptGeneration < 1 || request.TargetGeneration < 1 {
		return errors.New("credential authority generations must be positive")
	}
	return nil
}

// HeaderMutation is deliberately closed-world. Providers may only return
// headers declared by their adapter; arbitrary request rewrites are rejected.
type HeaderMutation struct {
	Headers map[string]string `json:"headers"`
}

type UploadResult struct {
	DisplayName      string
	AuthType         string
	PublicMetadata   json.RawMessage
	Secret           []byte
	AccessExpiresAt  *time.Time
	RefreshExpiresAt *time.Time
}

// Provider is implemented by each supported credential kind.
type Provider interface {
	Kind() string
	// ValidateUpload validates the provider-specific auth type and secret
	// envelope. authType is supplied by Platform and must be one of the
	// provider's declared schema values; it is never accepted blindly.
	ValidateUpload(authType string, raw []byte) (UploadResult, error)
	Materialize(context.Context, Binding, []byte, UseRequest) (HeaderMutation, error)
	AllowedHeaders() []string
}

// ProviderSchema is safe to expose to Platform. It describes the upload
// contract and the closed-world egress surface; it never contains a credential
// instance or a provider access token.
type ProviderSchema struct {
	Kind                 string   `json:"kind"`
	DisplayName          string   `json:"displayName"`
	AuthTypes            []string `json:"authTypes"`
	AllowedHosts         []string `json:"allowedHosts"`
	AllowedHeaders       []string `json:"allowedHeaders"`
	SecretFormat         string   `json:"secretFormat"`
	AuthorizationMethods []string `json:"authorizationMethods"`
}

type SchemaProvider interface {
	Provider
	Schema() ProviderSchema
}

type ProviderRegistry struct {
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) (*ProviderRegistry, error) {
	registry := &ProviderRegistry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if provider == nil || !identifierPattern.MatchString(provider.Kind()) {
			return nil, errors.New("credential provider kind is invalid")
		}
		if _, exists := registry.providers[provider.Kind()]; exists {
			return nil, fmt.Errorf("duplicate credential provider kind %q", provider.Kind())
		}
		registry.providers[provider.Kind()] = provider
	}
	return registry, nil
}

func (registry *ProviderRegistry) Lookup(kind string) (Provider, bool) {
	if registry == nil || registry.providers == nil {
		return nil, false
	}
	provider, ok := registry.providers[kind]
	return provider, ok
}

func (registry *ProviderRegistry) Kinds() []string {
	if registry == nil {
		return nil
	}
	result := make([]string, 0, len(registry.providers))
	for kind := range registry.providers {
		result = append(result, kind)
	}
	// Kinds are only used for deterministic schema output; avoid importing a
	// sorting policy into provider implementations.
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j] < result[j-1]; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}

func (registry *ProviderRegistry) Schemas() []ProviderSchema {
	if registry == nil {
		return nil
	}
	result := make([]ProviderSchema, 0, len(registry.providers))
	for _, provider := range registry.providers {
		if schemaProvider, ok := provider.(SchemaProvider); ok {
			schema := schemaProvider.Schema()
			schema.AuthTypes = append([]string(nil), schema.AuthTypes...)
			schema.AllowedHosts = append([]string(nil), schema.AllowedHosts...)
			schema.AllowedHeaders = append([]string(nil), schema.AllowedHeaders...)
			schema.AuthorizationMethods = append([]string(nil), schema.AuthorizationMethods...)
			result = append(result, schema)
			continue
		}
		result = append(result, ProviderSchema{
			Kind: provider.Kind(), DisplayName: provider.Kind(),
			AllowedHeaders: append([]string(nil), provider.AllowedHeaders()...),
		})
	}
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j].Kind < result[j-1].Kind; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}

type BindingStore interface {
	Get(context.Context, string, string, string) (Binding, error)
	List(context.Context, string, string) ([]BindingMetadata, error)
}

// LiveAuthorizer verifies all run/attempt/operation and binding authority
// state. It returns the exact binding reference selected at launch; it does
// not return secret material.
type LiveAuthorizer interface {
	AuthorizeCredentialUse(context.Context, UseRequest) (BindingReference, error)
}

type BindingReference struct {
	WorkspaceID       string
	Kind              string
	BindingID         string
	AuthorityVersion  int64
	CredentialVersion int64
	CredentialMode    string
}

type PlaceholderVerifier interface {
	Verify(string, time.Time) (PlaceholderClaims, error)
}

// PlaceholderClaims is intentionally provider-neutral. The concrete egress
// capability verifier adapts into this shape at the boundary.
type PlaceholderClaims struct {
	CapabilityID         string
	WorkspaceID          string
	SessionID            string
	ActorID              string
	EnvironmentID        string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	ExecutionID          string
	OperationID          string
	SandboxID            string
	TargetGeneration     int64
	ProviderKind         string
	BindingID            string
	AuthorityVersion     int64
	PolicySHA256         string
	ExpiresAt            time.Time
}

func (request UseRequest) Validate() error {
	if request.Placeholder == "" || strings.TrimSpace(request.Placeholder) != request.Placeholder {
		return errors.New("credential use placeholder is required")
	}
	if err := request.ValidateLiveAuthorityScope(); err != nil {
		return err
	}
	if len(request.Headers) > 128 {
		return errors.New("credential use headers exceed limit")
	}
	seenHeaders := make(map[string]struct{}, len(request.Headers))
	for name, value := range request.Headers {
		if name == "" || len(name) > 256 || !validHeaderName(name) || len(value) > maximumHeaderValueBytes || strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return errors.New("credential use header is invalid")
		}
		canonical := strings.ToLower(name)
		if _, duplicate := seenHeaders[canonical]; duplicate || forbiddenUseHeader(canonical) {
			return errors.New("credential use header is ambiguous or unsafe")
		}
		seenHeaders[canonical] = struct{}{}
	}
	authorization, ok := exactHeader(request.Headers, "authorization")
	if !ok || authorization != "Bearer "+request.Placeholder {
		return errors.New("credential use authorization placeholder is invalid")
	}
	return nil
}

// ValidateLiveAuthorityScope validates the operation, provider, binding and
// target tuple consumed by the database live-authority check. It deliberately
// excludes the transport-specific placeholder and request headers so Core can
// use the same exact operation check for its workload-authenticated
// process-environment delivery mode.
func (request UseRequest) ValidateLiveAuthorityScope() error {
	for name, value := range map[string]string{
		"workspaceId": request.WorkspaceID, "sessionId": request.SessionID, "actorId": request.ActorID,
		"environmentId": request.EnvironmentID, "runId": request.RunID,
		"runAttemptId": request.RunAttemptID, "executionId": request.ExecutionID,
		"operationId": request.OperationID, "sandboxId": request.SandboxID,
		"providerKind": request.ProviderKind, "bindingId": request.BindingID,
		"taePsm": request.TAEPSM,
		"host":   request.Host, "path": request.Path, "method": request.Method,
	} {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("credential use %s is required", name)
		}
	}
	if !identifierPattern.MatchString(request.ProviderKind) || !identifierPattern.MatchString(request.BindingID) {
		return errors.New("credential use provider or binding ID is invalid")
	}
	if !identifierPattern.MatchString(request.TAEPSM) || !digestPattern.MatchString(request.PolicySHA256) {
		return errors.New("credential use TAE identity or policy digest is invalid")
	}
	if !managedcredential.ValidMode(request.CredentialMode) {
		return errors.New("credential use delivery mode is invalid")
	}
	if request.RunAttemptGeneration < 1 || request.TargetGeneration < 1 || request.AuthorityVersion < 1 {
		return errors.New("credential use generations and authority version must be positive")
	}
	if request.ExpectedCredentialVersion < 0 ||
		(request.CredentialMode == managedcredential.ModeProcessEnv && request.ExpectedCredentialVersion < 1) {
		return errors.New("credential use expected credential version is invalid")
	}
	if len(request.Host) > maximumHostBytes || request.Host != strings.ToLower(request.Host) ||
		net.ParseIP(request.Host) != nil || strings.ContainsAny(request.Host, "/:@[]") ||
		strings.HasPrefix(request.Host, ".") || strings.HasSuffix(request.Host, ".") || strings.Contains(request.Host, "..") {
		return errors.New("credential use host is not a canonical provider DNS name")
	}
	if len(request.Path) > maximumPathBytes || !strings.HasPrefix(request.Path, "/") ||
		strings.ContainsAny(request.Path, "\\%?#\x00") || strings.Contains(request.Path, "//") ||
		path.Clean(request.Path) != request.Path || (len(request.Path) > 1 && strings.HasSuffix(request.Path, "/")) {
		return errors.New("credential use path is not canonical")
	}
	if len(request.Method) > 16 || strings.ToUpper(request.Method) != request.Method {
		return errors.New("credential use request target is invalid")
	}
	return nil
}

func forbiddenUseHeader(name string) bool {
	switch name {
	case "host", "content-length", "connection", "cookie", "forwarded", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "proxy-connection", "set-cookie", "te", "trailer", "transfer-encoding", "upgrade",
		"x-zti-token":
		return true
	default:
		return strings.HasPrefix(name, "x-forwarded-")
	}
}

func exactHeader(headers map[string]string, wanted string) (string, bool) {
	var value string
	found := false
	for name, candidate := range headers {
		if strings.EqualFold(name, wanted) {
			if found {
				return "", false
			}
			value, found = candidate, true
		}
	}
	return value, found
}

func (mutation HeaderMutation) Validate(provider Provider) error {
	if provider == nil || len(mutation.Headers) == 0 || len(mutation.Headers) > 8 {
		return errors.New("credential header mutation is empty or excessive")
	}
	allowed := make(map[string]struct{})
	for _, name := range provider.AllowedHeaders() {
		allowed[strings.ToLower(name)] = struct{}{}
	}
	for name, value := range mutation.Headers {
		if _, ok := allowed[strings.ToLower(name)]; !ok || value == "" || len(value) > maximumHeaderValueBytes || strings.ContainsAny(value, "\r\n") {
			return errors.New("credential provider returned a forbidden header mutation")
		}
	}
	return nil
}

// ValidateClosedHeaderMutation performs the transport-side checks available
// without a provider instance. Core already checked the provider's exact
// allowlist; egress-authorizer repeats these generic hop-by-hop and
// injection bounds before forwarding the mutation to TAE.
func ValidateClosedHeaderMutation(mutation HeaderMutation) error {
	if len(mutation.Headers) == 0 || len(mutation.Headers) > 8 {
		return errors.New("credential header mutation is empty or excessive")
	}
	seen := make(map[string]struct{}, len(mutation.Headers))
	for name, value := range mutation.Headers {
		canonical := strings.ToLower(name)
		if name == "" || len(name) > 256 || !validHeaderName(name) || value == "" ||
			len(value) > maximumHeaderValueBytes || strings.ContainsAny(value, "\r\n") {
			return errors.New("credential header mutation is invalid")
		}
		if _, duplicate := seen[canonical]; duplicate || forbiddenMutationHeader(canonical) {
			return errors.New("credential header mutation contains an ambiguous or forbidden header")
		}
		seen[canonical] = struct{}{}
	}
	return nil
}

func forbiddenMutationHeader(name string) bool {
	switch name {
	case "connection", "content-length", "cookie", "forwarded", "host", "keep-alive",
		"proxy-authenticate", "proxy-authorization", "proxy-connection", "set-cookie",
		"te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return strings.HasPrefix(name, "x-forwarded-")
	}
}

func validHeaderName(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') {
			continue
		}
		switch character {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return value != ""
}

func cloneJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), raw...)
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	result := make(map[string]string, len(headers))
	for key, value := range headers {
		result[key] = value
	}
	return result
}
