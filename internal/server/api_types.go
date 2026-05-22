package server

import "time"

// This file holds package-level request/response types for the
// public REST API. swaggo annotations on handler funcs reference
// these by name; inline `var req struct {...}` shapes can't be
// referenced from annotations, which is why we extract them here.
//
// Group additions by API tag (Auth, Workspaces, …). Add new types
// alphabetically within each group so PRs from different tags don't
// trip over each other.
//
// IMPORTANT: required JSON fields need `validate:"required"` so swag
// emits them in the OpenAPI schema's `required` array. Without it the
// frontend codegen treats every field as `T | undefined`.

// --- Auth ---

// AuthCredentials is the email+password body for POST /api/auth/login
// and POST /api/auth/register.
type AuthCredentials struct {
	Email    string `json:"email" example:"alice@example.com" validate:"required"`
	Password string `json:"password" example:"hunter2" validate:"required"`
} //@name AuthCredentials

// AuthStatusResponse is the {"status":"ok"} envelope returned by
// /api/auth/login, /api/auth/logout, and /api/auth/check on success.
type AuthStatusResponse struct {
	Status string `json:"status" example:"ok" validate:"required"`
} //@name AuthStatusResponse

// AuthRegisterResponse is what POST /api/auth/register returns on
// success: the new user's id and the email it was registered with.
type AuthRegisterResponse struct {
	ID    string `json:"id"    example:"7e7a4f6c-..." validate:"required"`
	Email string `json:"email" example:"alice@example.com" validate:"required"`
} //@name AuthRegisterResponse

// AuthMeResponse is the current user payload returned by GET /api/auth/me.
// Name and Picture are populated from OIDC profile data when present
// (login via password leaves both empty). Both fields are always present
// in the JSON response — nil pointers serialize as null (not omitted).
type AuthMeResponse struct {
	ID      string  `json:"id" validate:"required"`
	Email   string  `json:"email" validate:"required"`
	Name    *string `json:"name" extensions:"x-nullable=true"`
	Picture *string `json:"picture" extensions:"x-nullable=true"`
	Role    string  `json:"role" example:"developer" validate:"required"`
} //@name AuthMeResponse

// --- Workspaces ---

// WorkspaceCreateRequest is the body for POST /api/workspaces.
type WorkspaceCreateRequest struct {
	Name string `json:"name" validate:"required" example:"My Workspace"`
} // @name WorkspaceCreateRequest

// WorkspaceRenameRequest is the body for PATCH /api/workspaces/{id}.
type WorkspaceRenameRequest struct {
	Name string `json:"name" validate:"required" example:"Renamed Workspace"`
} // @name WorkspaceRenameRequest

// WorkspaceQuotaResponse is the {"current": int, "max": int} envelope
// returned by GET /api/workspaces/quota.
type WorkspaceQuotaResponse struct {
	Current int `json:"current" validate:"required"`
	Max     int `json:"max" validate:"required"`
} // @name WorkspaceQuotaResponse

// MemberAddRequest is the body for POST /api/workspaces/{id}/members.
type MemberAddRequest struct {
	Email string `json:"email" validate:"required" example:"alice@example.com"`
	Role  string `json:"role" example:"developer"` // optional; defaults to "developer"
} // @name MemberAddRequest

// MemberRoleUpdateRequest is the body for PUT /api/workspaces/{id}/members/{userId}.
type MemberRoleUpdateRequest struct {
	Role string `json:"role" validate:"required" example:"maintainer"`
} // @name MemberRoleUpdateRequest

// LLMWorkspaceQuotaPart is the per-workspace quota override stored in the
// LLM proxy DB. max_rpd is null when no custom limit is configured.
type LLMWorkspaceQuotaPart struct {
	WorkspaceID string  `json:"workspace_id" validate:"required"`
	MaxRPD      *int    `json:"max_rpd" extensions:"x-nullable=true"`
	UpdatedAt   string  `json:"updated_at" validate:"required"`
} // @name LLMWorkspaceQuotaPart

// LLMQuotaResponse mirrors the body the LLM proxy returns from its
// /internal/quotas/{workspaceId} endpoint, forwarded verbatim by
// GET /api/workspaces/{id}/llm-quota.
// workspace_quota is null when no per-workspace override is set.
type LLMQuotaResponse struct {
	DefaultMaxRPD     int                    `json:"default_max_rpd" validate:"required"`
	TodayRequestCount int                    `json:"today_request_count" validate:"required"`
	WorkspaceQuota    *LLMWorkspaceQuotaPart `json:"workspace_quota" extensions:"x-nullable=true"`
} // @name LLMQuotaResponse

// LLMModel is one entry in a workspace's per-model LLM config.
// id is the model identifier used in API calls; name is the human-readable label.
type LLMModel struct {
	ID   string `json:"id" validate:"required" example:"claude-opus-4-7"`
	Name string `json:"name" validate:"required" example:"Claude Opus 4.7"`
} // @name LLMModel

// LLMConfigResponse is the body returned by GET /api/workspaces/{id}/llm-config.
// api_key is masked (first 3 + "..." + last 4 chars) and is empty
// when no config exists.
type LLMConfigResponse struct {
	Configured bool       `json:"configured" validate:"required"`
	BaseURL    string     `json:"base_url"`
	APIKey     string     `json:"api_key"`
	Models     []LLMModel `json:"models,omitempty"`
	UpdatedAt  *string    `json:"updated_at" extensions:"x-nullable=true"`
} // @name LLMConfigResponse

// LLMConfigUpsertRequest is the body for PUT /api/workspaces/{id}/llm-config.
// All three fields are required for a fresh config; for an update,
// omitting api_key retains the existing key.
type LLMConfigUpsertRequest struct {
	BaseURL string     `json:"base_url" validate:"required" example:"https://api.anthropic.com"`
	APIKey  string     `json:"api_key" example:"sk-ant-..."` // optional on update
	Models  []LLMModel `json:"models" validate:"required"`
} // @name LLMConfigUpsertRequest

// LLMConfigUpsertResponse is the body returned by the upsert endpoint.
type LLMConfigUpsertResponse struct {
	OK bool `json:"ok" validate:"required"`
} // @name LLMConfigUpsertResponse

// --- Sandboxes ---

// SandboxCreateRequest is the body for POST /api/workspaces/{wid}/sandboxes.
// All fields except name are optional and fall back to workspace/server defaults.
type SandboxCreateRequest struct {
	Name        string                 `json:"name" validate:"required" example:"my-sandbox"`
	Type        string                 `json:"type" example:"opencode"`    // optional; default "opencode"
	CPU         *int                   `json:"cpu"`                        // optional; millicores, e.g. 500 or 2000
	Memory      *int64                 `json:"memory"`                     // optional; bytes, e.g. 536870912 (512Mi)
	IdleTimeout *int                   `json:"idle_timeout"`               // optional; seconds
	Metadata    map[string]interface{} `json:"metadata"`                   // optional; arbitrary key-value metadata
} // @name SandboxCreateRequest

// SandboxRenameRequest is the body for PATCH /api/sandboxes/{id}.
type SandboxRenameRequest struct {
	Name string `json:"name" validate:"required" example:"renamed-sandbox"`
} // @name SandboxRenameRequest

// SandboxLifecycleStatusResponse is the {"status": "pausing"} envelope returned
// by POST /api/sandboxes/{id}/pause and /resume. The status reflects the
// transition initiated, not the final state (those are async).
type SandboxLifecycleStatusResponse struct {
	Status string `json:"status" validate:"required" example:"pausing"`
} // @name SandboxLifecycleStatusResponse

// SandboxUsageSummary is one row in the per-provider/model breakdown returned
// by GET /api/sandboxes/{id}/usage. It mirrors llmproxy.UsageSummary.
type SandboxUsageSummary struct {
	Provider                 string `json:"provider" validate:"required" example:"anthropic"`
	Model                    string `json:"model" validate:"required" example:"claude-sonnet-4-6"`
	InputTokens              int64  `json:"input_tokens" validate:"required"`
	OutputTokens             int64  `json:"output_tokens" validate:"required"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens" validate:"required"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens" validate:"required"`
	RequestCount             int64  `json:"request_count" validate:"required"`
} // @name SandboxUsageSummary

// SandboxUsageResponse mirrors the body the LLM proxy returns from its
// /internal/usage?sandbox_id={id} endpoint, forwarded verbatim by
// GET /api/sandboxes/{id}/usage. usage is the per-provider/model breakdown;
// since is set only when the caller supplied a ?since= query param.
type SandboxUsageResponse struct {
	Usage []SandboxUsageSummary `json:"usage" validate:"required"`
	Since *string               `json:"since,omitempty" extensions:"x-nullable=true" example:"2026-01-01T00:00:00Z"`
} // @name SandboxUsage

// --- IM Channels ---

// IMChannelResponse mirrors workspace_im_channels rows returned by
// GET /api/workspaces/{id}/im/channels via the imbridge service.
// user_id is omitted when not set (weixin sets it; telegram/matrix do not).
type IMChannelResponse struct {
	ID             string `json:"id" validate:"required"`
	WorkspaceID    string `json:"workspace_id" validate:"required"`
	Provider       string `json:"provider" validate:"required" example:"weixin"`
	BotID          string `json:"bot_id" validate:"required"`
	UserID         string `json:"user_id,omitempty"`
	RequireMention bool   `json:"require_mention"`
	RoutingMode    string `json:"routing_mode" validate:"required" example:"codex"`
	BoundAt        string `json:"bound_at" validate:"required"`
} // @name IMChannel

// IMChannelListResponse is the {"channels": [...]} envelope returned by
// GET /api/workspaces/{id}/im/channels.
type IMChannelListResponse struct {
	Channels []IMChannelResponse `json:"channels" validate:"required"`
} // @name IMChannelListResponse

// IMChannelPatchRequest is the body for PATCH /api/workspaces/{id}/im/channels/{channelId}.
// Both fields are optional — only the supplied keys are applied.
// routing_mode must be "nanoclaw" or "codex".
type IMChannelPatchRequest struct {
	RequireMention *bool   `json:"require_mention" extensions:"x-nullable=true"`
	RoutingMode    *string `json:"routing_mode" extensions:"x-nullable=true" example:"codex"`
} // @name IMChannelPatchRequest

// IMChannelPatchResponse is the body returned on success by
// PATCH /api/workspaces/{id}/im/channels/{channelId}.
type IMChannelPatchResponse struct {
	Status string `json:"status" validate:"required" example:"updated"`
} // @name IMChannelPatchResponse

// IMWeixinQRStartResponse is returned by POST .../im/weixin/qr-start.
type IMWeixinQRStartResponse struct {
	QRCodeURL string `json:"qrcode_url" validate:"required"`
	Message   string `json:"message" validate:"required"`
} // @name IMWeixinQRStartResponse

// IMWeixinQRWaitResponse is the polymorphic response from POST .../im/weixin/qr-wait.
// status values: "wait", "scaned", "confirmed", "expired", "binded_redirect",
// "verify_code_blocked", "need_verifycode".
// qrcode_url is only present when status is "expired" (new QR code generated).
// bot_id and user_id are only present when status is "confirmed".
type IMWeixinQRWaitResponse struct {
	Connected  bool    `json:"connected" validate:"required"`
	Status     string  `json:"status" validate:"required" example:"wait"`
	Message    *string `json:"message,omitempty" extensions:"x-nullable=true"`
	QRCodeURL  *string `json:"qrcode_url,omitempty" extensions:"x-nullable=true"`
	BotID      *string `json:"bot_id,omitempty" extensions:"x-nullable=true"`
	UserID     *string `json:"user_id,omitempty" extensions:"x-nullable=true"`
} // @name IMWeixinQRWaitResponse

// IMTelegramConfigureRequest is the body for POST .../im/telegram/configure.
type IMTelegramConfigureRequest struct {
	BotToken string `json:"bot_token" validate:"required" example:"123456:ABC-DEF..."`
} // @name IMTelegramConfigureRequest

// IMTelegramConfigureResponse is returned by POST .../im/telegram/configure.
type IMTelegramConfigureResponse struct {
	Connected bool   `json:"connected" validate:"required"`
	BotID     string `json:"bot_id" validate:"required"`
} // @name IMTelegramConfigureResponse

// IMMatrixConfigureRequest is the body for POST .../im/matrix/configure.
// recovery_key is optional and only used to enable E2EE.
type IMMatrixConfigureRequest struct {
	HomeserverURL string `json:"homeserver_url" validate:"required" example:"https://matrix.example.com"`
	AccessToken   string `json:"access_token" validate:"required"`
	RecoveryKey   string `json:"recovery_key"` // optional, for E2EE
} // @name IMMatrixConfigureRequest

// IMMatrixConfigureResponse is returned by POST .../im/matrix/configure.
type IMMatrixConfigureResponse struct {
	Connected bool   `json:"connected" validate:"required"`
	BotID     string `json:"bot_id" validate:"required"`
} // @name IMMatrixConfigureResponse

// IMSandboxBindRequest is the body for POST /api/sandboxes/{id}/im/bind.
type IMSandboxBindRequest struct {
	ChannelID string `json:"channel_id" validate:"required"`
} // @name IMSandboxBindRequest

// IMSandboxBindResponse is returned on success by POST /api/sandboxes/{id}/im/bind.
type IMSandboxBindResponse struct {
	Status string `json:"status" validate:"required" example:"bound"`
} // @name IMSandboxBindResponse

// IMSandboxUnbindResponse is returned on success by DELETE /api/sandboxes/{id}/im/bind.
type IMSandboxUnbindResponse struct {
	Status string `json:"status" validate:"required" example:"unbound"`
} // @name IMSandboxUnbindResponse

// --- Codex Tokens ---

// CodexTokenMintRequest is the body for POST /api/codex/tokens.
// ttl_days is optional; defaults to 90 (range: 1–365).
type CodexTokenMintRequest struct {
	WorkspaceID string `json:"workspace_id" validate:"required"`
	Name        string `json:"name" validate:"required" example:"my mac"`
	TTLDays     int    `json:"ttl_days,omitempty" example:"90"`
} // @name CodexTokenMintRequest

// CodexTokenMintResponse is returned (201) by POST /api/codex/tokens.
// token is the full bearer value and is shown only once — store it securely.
type CodexTokenMintResponse struct {
	ID          string    `json:"id" validate:"required"`
	Token       string    `json:"token" validate:"required"`
	Name        string    `json:"name" validate:"required"`
	WorkspaceID string    `json:"workspace_id" validate:"required"`
	ExpiresAt   time.Time `json:"expires_at" validate:"required"`
	CreatedAt   time.Time `json:"created_at" validate:"required"`
} // @name CodexTokenMintResponse

// CodexTokenListItem is one entry returned by GET /api/codex/tokens.
// The raw token value is never included. last_used_at and revoked_at
// are null when not applicable.
type CodexTokenListItem struct {
	ID          string     `json:"id" validate:"required"`
	Name        string     `json:"name" validate:"required"`
	WorkspaceID string     `json:"workspace_id" validate:"required"`
	CreatedAt   time.Time  `json:"created_at" validate:"required"`
	ExpiresAt   time.Time  `json:"expires_at" validate:"required"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty" extensions:"x-nullable=true"`
	Revoked     bool       `json:"revoked"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty" extensions:"x-nullable=true"`
} // @name CodexTokenListItem

// --- Agent Discovery ---

// AgentRegisterRequest is the body for POST /api/agent/register.
// type defaults to "opencode" when omitted; valid values: opencode, claudecode, custom.
type AgentRegisterRequest struct {
	Name string `json:"name" example:"Local Agent"`
	Type string `json:"type" example:"opencode"` // optional; defaults to "opencode"
} // @name AgentRegisterRequest

// AgentRegisterResponse is returned (201) by POST /api/agent/register.
// proxy_token is the Bearer token used for subsequent agent-facing API calls.
// tunnel_token is used by the tunnel transport (if applicable).
type AgentRegisterResponse struct {
	SandboxID   string `json:"sandbox_id" validate:"required"`
	TunnelToken string `json:"tunnel_token" validate:"required"`
	ProxyToken  string `json:"proxy_token" validate:"required"`
	WorkspaceID string `json:"workspace_id" validate:"required"`
	ShortID     string `json:"short_id" validate:"required"`
} // @name AgentRegisterResponse

// AgentCardRegisterRequest is the body for POST /api/agent/discovery/cards.
// card is an arbitrary JSON capability descriptor; defaults to {} when omitted.
type AgentCardRegisterRequest struct {
	DisplayName string      `json:"display_name" example:"My Agent"`
	Description string      `json:"description" example:"A helpful coding agent"`
	AgentType   string      `json:"agent_type" example:"claudecode"` // optional; defaults to "claudecode"
	Card        interface{} `json:"card"`                            // optional; arbitrary JSON
} // @name AgentCardRegisterRequest

// AgentCardRegisterResponse is the {"status":"ok"} body returned (200) by
// POST /api/agent/discovery/cards.
type AgentCardRegisterResponse struct {
	Status string `json:"status" validate:"required" example:"ok"`
} // @name AgentCardRegisterResponse

// AgentCardItem is one entry in the agent card list returned by
// GET /api/workspaces/{wid}/agents and GET /api/agent/discovery/agents.
// card is the raw capability descriptor JSON submitted at registration time.
type AgentCardItem struct {
	AgentID     string      `json:"agent_id" validate:"required"`
	DisplayName string      `json:"display_name" validate:"required"`
	Description string      `json:"description"`
	AgentType   string      `json:"agent_type" validate:"required" example:"claudecode"`
	Status      string      `json:"status" validate:"required" example:"available"`
	Card        interface{} `json:"card" validate:"required"`
	Version     int         `json:"version" validate:"required"`
} // @name AgentCardItem

// --- Agent Tasks ---

// AgentTaskCreateRequest is the body for POST /api/workspaces/{wid}/tasks
// and POST /api/agent/tasks.
// timeout_seconds defaults to 300 when omitted.
type AgentTaskCreateRequest struct {
	TargetID        string   `json:"target_id" validate:"required" example:"sandbox-uuid"`
	Prompt          string   `json:"prompt" validate:"required" example:"Run the test suite"`
	Skill           string   `json:"skill,omitempty" example:"testing"`
	SystemContext   string   `json:"system_context,omitempty"`
	MaxTurns        int      `json:"max_turns,omitempty" example:"50"`
	MaxBudgetUSD    float64  `json:"max_budget_usd,omitempty" example:"1.0"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty" example:"300"`
	DelegationChain []string `json:"delegation_chain,omitempty"`
	RequesterID     string   `json:"requester_id,omitempty"`
} // @name AgentTaskCreateRequest

// AgentTaskCreateResponse is returned (201) by the task creation endpoints.
// session_id is empty in the current implementation (bridge was removed).
type AgentTaskCreateResponse struct {
	TaskID    string `json:"task_id" validate:"required"`
	SessionID string `json:"session_id"`
	Status    string `json:"status" validate:"required" example:"pending"`
} // @name AgentTaskCreateResponse

// AgentTaskItem is one entry in the task list returned by
// GET /api/workspaces/{wid}/tasks.
// completed_at and total_cost_usd are null when not yet set.
type AgentTaskItem struct {
	TaskID      string   `json:"task_id" validate:"required"`
	TargetID    string   `json:"target_id" validate:"required"`
	RequesterID string   `json:"requester_id"`
	Skill       string   `json:"skill,omitempty"`
	Status      string   `json:"status" validate:"required" example:"pending"`
	Prompt      string   `json:"prompt" validate:"required"`
	NumTurns    int      `json:"num_turns"`
	TotalCost   *float64 `json:"total_cost_usd,omitempty" extensions:"x-nullable=true"`
	CreatedAt   string   `json:"created_at" validate:"required"`
	CompletedAt *string  `json:"completed_at,omitempty" extensions:"x-nullable=true"`
} // @name AgentTaskItem

// AgentTaskDetail is the full task payload returned by GET /api/tasks/{id}
// and GET /api/agent/tasks/{id}.
// Optional fields are absent when not applicable.
type AgentTaskDetail struct {
	TaskID        string      `json:"task_id" validate:"required"`
	WorkspaceID   string      `json:"workspace_id" validate:"required"`
	RequesterID   string      `json:"requester_id"`
	TargetID      string      `json:"target_id" validate:"required"`
	Prompt        string      `json:"prompt" validate:"required"`
	Status        string      `json:"status" validate:"required" example:"pending"`
	NumTurns      int         `json:"num_turns"`
	CreatedAt     string      `json:"created_at" validate:"required"`
	SessionID     *string     `json:"session_id,omitempty" extensions:"x-nullable=true"`
	Skill         *string     `json:"skill,omitempty" extensions:"x-nullable=true"`
	TotalCostUSD  *float64    `json:"total_cost_usd,omitempty" extensions:"x-nullable=true"`
	Result        interface{} `json:"result,omitempty" extensions:"x-nullable=true"`
	FailureReason *string     `json:"failure_reason,omitempty" extensions:"x-nullable=true"`
	CompletedAt   *string     `json:"completed_at,omitempty" extensions:"x-nullable=true"`
} // @name AgentTaskDetail

// AgentTaskStatusRequest is the body for PUT /api/agent/tasks/{id}/status.
// Valid status values: running, completed, failed, cancelled.
// failure_reason is required when status is "failed".
// result and total_cost_usd are used when status is "completed".
type AgentTaskStatusRequest struct {
	Status        string      `json:"status" validate:"required" example:"completed"`
	FailureReason string      `json:"failure_reason,omitempty"`
	Result        interface{} `json:"result,omitempty"`
	TotalCostUSD  *float64    `json:"total_cost_usd,omitempty" extensions:"x-nullable=true"`
	NumTurns      int         `json:"num_turns,omitempty"`
} // @name AgentTaskStatusRequest

// AgentTaskCancelResponse is the {"status":"cancelled"} body returned (200)
// by POST /api/tasks/{id}/cancel.
type AgentTaskCancelResponse struct {
	Status string `json:"status" validate:"required" example:"cancelled"`
} // @name AgentTaskCancelResponse

// AgentTaskPollItem is one entry in the list returned by
// GET /api/agent/tasks/poll. Only the fields needed for immediate execution
// are included (no cost/status/audit fields).
type AgentTaskPollItem struct {
	TaskID        string  `json:"task_id" validate:"required"`
	Prompt        string  `json:"prompt" validate:"required"`
	SystemContext string  `json:"system_context,omitempty"`
	MaxTurns      int     `json:"max_turns,omitempty"`
	MaxBudgetUSD  float64 `json:"max_budget_usd,omitempty"`
	SessionID     string  `json:"session_id,omitempty"`
} // @name AgentTaskPollItem

// --- Agent Mailbox ---

// AgentMailboxSendRequest is the body for POST /api/agent/mailbox/send.
// msg_type defaults to "text" when omitted.
type AgentMailboxSendRequest struct {
	To      string `json:"to" validate:"required" example:"sandbox-uuid-of-target"`
	Text    string `json:"text" validate:"required" example:"Hello from another agent"`
	MsgType string `json:"msg_type,omitempty" example:"text"`
} // @name AgentMailboxSendRequest

// AgentMailboxSendResponse is returned (201) by POST /api/agent/mailbox/send.
type AgentMailboxSendResponse struct {
	MessageID string `json:"message_id" validate:"required"`
	Status    string `json:"status" validate:"required" example:"sent"`
} // @name AgentMailboxSendResponse

// AgentMailboxMessage is one message in the inbox returned by
// GET /api/agent/mailbox/inbox.
type AgentMailboxMessage struct {
	ID        string `json:"id" validate:"required"`
	From      string `json:"from" validate:"required"`
	Text      string `json:"text" validate:"required"`
	MsgType   string `json:"msg_type" validate:"required" example:"text"`
	CreatedAt string `json:"created_at" validate:"required"`
} // @name AgentMailboxMessage

// --- Codex Browser Sessions ---

// CodexBrowserItem is one entry returned by GET /api/workspaces/{wid}/browsers.
// It mirrors RemoteExecutor so DeviceListPanel can render both without per-type
// branches. is_online is true when at least one open browser session exists for
// the underlying codex token. All session fields come from the latest session
// row (open if any, otherwise the most recent historical row).
type CodexBrowserItem struct {
	ID             string     `json:"id" validate:"required"`
	Name           string     `json:"name" validate:"required"`
	WorkspaceID    string     `json:"workspace_id" validate:"required"`
	CreatedAt      time.Time  `json:"created_at" validate:"required"`
	ExpiresAt      time.Time  `json:"expires_at" validate:"required"`
	IsOnline       bool       `json:"is_online" validate:"required"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	ClientIP       string     `json:"client_ip,omitempty"`
	ClientUA       string     `json:"client_ua,omitempty"`
	CodexVersion   string     `json:"codex_version,omitempty"`
	OS             string     `json:"os,omitempty"`
	ConnectedAt    *time.Time `json:"connected_at,omitempty"`
	DisconnectedAt *time.Time `json:"disconnected_at,omitempty"`
} // @name CodexBrowserItem

// --- Misc ---

// CredentialBindingItem is one entry in the list returned by
// GET /api/workspaces/{id}/credentials/{kind}.
// public_meta is an arbitrary JSON object whose shape depends on the provider kind.
type CredentialBindingItem struct {
	ID          string          `json:"id" validate:"required"`
	DisplayName string          `json:"display_name" validate:"required"`
	ServerURL   string          `json:"server_url"`
	AuthType    string          `json:"auth_type" validate:"required" example:"static"`
	PublicMeta  interface{}     `json:"public_meta"`
	IsDefault   bool            `json:"is_default"`
	CreatedAt   string          `json:"created_at" validate:"required"`
} // @name CredentialBindingItem

// CredentialBindingCreateRequest is the body for POST /api/workspaces/{id}/credentials/{kind}.
// config is a provider-specific JSON or YAML credential blob.
type CredentialBindingCreateRequest struct {
	DisplayName string `json:"display_name" validate:"required" example:"My K8s Cluster"`
	Config      string `json:"config" validate:"required" example:"{\"server\": \"https://...\"}"`
} // @name CredentialBindingCreateRequest

// CredentialBindingCreateResponse is returned (201) by the create endpoint, or
// (202) when the provider initiates an OIDC device code flow.
// When status is "pending_device_code", verification_uri, user_code and expires_in
// are set; the caller must long-poll the device-complete endpoint.
type CredentialBindingCreateResponse struct {
	ID              string `json:"id" validate:"required"`
	DisplayName     string `json:"display_name,omitempty"`
	ServerURL       string `json:"server_url,omitempty"`
	AuthType        string `json:"auth_type,omitempty"`
	IsDefault       bool   `json:"is_default"`
	Status          string `json:"status,omitempty" example:"pending_device_code"`
	VerificationURI string `json:"verification_uri,omitempty"`
	UserCode        string `json:"user_code,omitempty"`
	ExpiresIn       int    `json:"expires_in,omitempty"`
} // @name CredentialBindingCreateResponse

// CredentialBindingPatchRequest is the body for PATCH /api/workspaces/{id}/credentials/{kind}/{bindingId}.
// Only display_name is patchable at present.
type CredentialBindingPatchRequest struct {
	DisplayName *string `json:"display_name" extensions:"x-nullable=true" example:"Renamed Cluster"`
} // @name CredentialBindingPatchRequest

// WorkspaceOperationsResponse is returned by GET /api/workspaces/{id}/operations.
type WorkspaceOperationsResponse struct {
	Operations []OperationRecord `json:"operations" validate:"required"`
} // @name WorkspaceOperationsResponse

// OperationRecord is a single entry in the operations log.
// arguments, arguments_meta and result_meta are arbitrary JSON objects.
type OperationRecord struct {
	ID            string      `json:"id" validate:"required"`
	WorkspaceID   string      `json:"workspace_id" validate:"required"`
	UserID        *string     `json:"user_id,omitempty" extensions:"x-nullable=true"`
	Source        string      `json:"source" validate:"required" example:"codex"`
	ThreadID      *string     `json:"thread_id,omitempty" extensions:"x-nullable=true"`
	RequestID     *string     `json:"request_id,omitempty" extensions:"x-nullable=true"`
	EnvID         string      `json:"env_id" validate:"required"`
	Tool          string      `json:"tool" validate:"required" example:"shell"`
	Arguments     interface{} `json:"arguments,omitempty"`
	ArgumentsMeta interface{} `json:"arguments_meta,omitempty"`
	IsError       bool        `json:"is_error"`
	ResultSummary *string     `json:"result_summary,omitempty" extensions:"x-nullable=true"`
	ResultMeta    interface{} `json:"result_meta,omitempty"`
	StartedAt     string      `json:"started_at" validate:"required"`
	CompletedAt   string      `json:"completed_at" validate:"required"`
	DurationMs    int32       `json:"duration_ms" validate:"required"`
	NotebookPath  *string     `json:"notebook_path,omitempty" extensions:"x-nullable=true"`
	CellID        *string     `json:"cell_id,omitempty" extensions:"x-nullable=true"`
} // @name OperationRecord

// WorkspaceDefaultsResponse is returned by GET /api/workspaces/{wid}/defaults.
// It provides the effective quota limits for the workspace and the current sandbox count.
type WorkspaceDefaultsResponse struct {
	MaxSandboxCPU    int   `json:"max_sandbox_cpu" validate:"required"`
	MaxSandboxMemory int64 `json:"max_sandbox_memory" validate:"required"`
	MaxIdleTimeout   int   `json:"max_idle_timeout" validate:"required"`
	MaxSandboxes     int   `json:"max_sandboxes" validate:"required"`
	CurrentSandboxes int   `json:"current_sandboxes" validate:"required"`
} // @name WorkspaceDefaultsResponse

// TraceRecord is one entry in the LLM trace list returned by the traces endpoints.
// All token/request count fields come from the llmproxy aggregation.
type TraceRecord struct {
	ID                       string `json:"id" validate:"required"`
	SandboxID                string `json:"sandbox_id" validate:"required"`
	WorkspaceID              string `json:"workspace_id" validate:"required"`
	Source                   string `json:"source" validate:"required" example:"codex"`
	CreatedAt                string `json:"created_at" validate:"required"`
	UpdatedAt                string `json:"updated_at" validate:"required"`
	RequestCount             int64  `json:"request_count" validate:"required"`
	TotalInputTokens         int64  `json:"total_input_tokens" validate:"required"`
	TotalOutputTokens        int64  `json:"total_output_tokens" validate:"required"`
	TotalCacheReadTokens     int64  `json:"total_cache_read_tokens" validate:"required"`
	TotalCacheCreationTokens int64  `json:"total_cache_creation_tokens" validate:"required"`
	Models                   string `json:"models,omitempty"`
} // @name TraceRecord

// TraceListResponse wraps a list of trace records.
// total is the total count for pagination (set by llmproxy).
type TraceListResponse struct {
	Traces []TraceRecord `json:"traces" validate:"required"`
	Total  int64         `json:"total"`
} // @name TraceListResponse

// TraceRequest is one row in the requests array of TraceDetailResponse.
// Fields mirror what the LLM proxy emits for a single API call (TokenUsage).
type TraceRequest struct {
	ID                       string `json:"id" validate:"required"`
	TraceID                  string `json:"trace_id,omitempty"`
	SandboxID                string `json:"sandbox_id" validate:"required"`
	WorkspaceID              string `json:"workspace_id" validate:"required"`
	Provider                 string `json:"provider" validate:"required"`
	Model                    string `json:"model" validate:"required"`
	MessageID                string `json:"message_id,omitempty"`
	InputTokens              int64  `json:"input_tokens" validate:"required"`
	OutputTokens             int64  `json:"output_tokens" validate:"required"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens" validate:"required"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens" validate:"required"`
	Streaming                bool   `json:"streaming"`
	Duration                 int64  `json:"duration" validate:"required"`
	TTFT                     int64  `json:"ttft"`
	CreatedAt                string `json:"created_at" validate:"required"`
} // @name TraceRequest

// TraceDetailResponse is the body returned by /traces/{traceId} endpoints.
// It pairs the trace metadata with the list of per-request records.
type TraceDetailResponse struct {
	Trace    TraceRecord    `json:"trace" validate:"required"`
	Requests []TraceRequest `json:"requests" validate:"required"`
} // @name TraceDetailResponse

// ExecutorItem is one entry returned by GET /api/workspaces/{wid}/executors.
// All session fields (client_ip, client_ua, codex_version, os,
// connected_at, disconnected_at) come from the last seen connection;
// they are omitted when the executor has never connected.
type ExecutorItem struct {
	ExeID          string     `json:"exe_id" validate:"required"`
	Name           string     `json:"name" validate:"required"`
	Description    string     `json:"description,omitempty"`
	IsDefault      bool       `json:"is_default"`
	IsOnline       bool       `json:"is_online"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty" extensions:"x-nullable=true"`
	ClientIP       string     `json:"client_ip,omitempty"`
	ClientUA       string     `json:"client_ua,omitempty"`
	CodexVersion   string     `json:"codex_version,omitempty"`
	OS             string     `json:"os,omitempty"`
	ConnectedAt    *time.Time `json:"connected_at,omitempty" extensions:"x-nullable=true"`
	DisconnectedAt *time.Time `json:"disconnected_at,omitempty" extensions:"x-nullable=true"`
} // @name ExecutorItem

// ExecutorRegisterRequest is the body for POST /api/workspaces/{wid}/executors.
type ExecutorRegisterRequest struct {
	Name        string `json:"name" validate:"required" example:"my-dev-machine"`
	Description string `json:"description,omitempty" example:"Main development laptop"`
} // @name ExecutorRegisterRequest

// ExecutorConnectCommands holds the connect-command variants returned by the
// register endpoint. At present only the agent_identity variant is surfaced.
type ExecutorConnectCommands struct {
	AgentIdentity string `json:"agent_identity" validate:"required"`
} // @name ExecutorConnectCommands

// ExecutorRegisterResponse is returned (201) by POST /api/workspaces/{wid}/executors.
// agent_identity_jwt and connect_commands are only set when the codex auth shim is configured.
// connect_command is a convenience alias for connect_commands.agent_identity.
type ExecutorRegisterResponse struct {
	ExeID            string                  `json:"exe_id" validate:"required"`
	ConnectCommand   string                  `json:"connect_command,omitempty"`
	AgentIdentityJWT string                  `json:"agent_identity_jwt,omitempty"`
	ConnectCommands  ExecutorConnectCommands  `json:"connect_commands,omitempty"`
} // @name ExecutorRegisterResponse

// ModelServerStatusResponse is returned by GET /api/workspaces/{id}/modelserver/status.
// When connected is false all other fields are absent.
// models entries have the same {id, name} shape as LLMModel.
type ModelServerStatusResponse struct {
	Connected   bool       `json:"connected" validate:"required"`
	ProjectID   string     `json:"project_id,omitempty"`
	ProjectName string     `json:"project_name,omitempty"`
	Models      []LLMModel `json:"models,omitempty"`
	ConnectedAt string     `json:"connected_at,omitempty"`
} // @name ModelServerStatusResponse

// AgentInteractionItem is one entry in the audit trail returned by
// GET /api/workspaces/{wid}/agent-interactions.
// detail is an arbitrary JSON object; it is absent when empty.
type AgentInteractionItem struct {
	ID         int64       `json:"id" validate:"required"`
	ActorID    *string     `json:"actor_id" extensions:"x-nullable=true"`
	Action     string      `json:"action" validate:"required" example:"task.created"`
	TargetID   string      `json:"target_id" validate:"required"`
	TargetType string      `json:"target_type" validate:"required" example:"task"`
	Detail     interface{} `json:"detail,omitempty"`
	CreatedAt  string      `json:"created_at" validate:"required"`
} // @name AgentInteractionItem

// --- Admin ---

// AdminUserItem is one entry returned by GET /api/admin/users.
type AdminUserItem struct {
	ID        string  `json:"id" validate:"required"`
	Email     string  `json:"email" validate:"required"`
	Name      *string `json:"name" extensions:"x-nullable=true"`
	Role      string  `json:"role" validate:"required" example:"user"`
	CreatedAt string  `json:"created_at" validate:"required"`
} // @name AdminUserItem

// AdminOwnerInfo is the owner summary embedded in AdminWorkspaceItem.
type AdminOwnerInfo struct {
	ID      string  `json:"id" validate:"required"`
	Email   string  `json:"email" validate:"required"`
	Name    *string `json:"name" extensions:"x-nullable=true"`
	Picture *string `json:"picture" extensions:"x-nullable=true"`
} // @name AdminOwnerInfo

// AdminWorkspaceItem is one entry returned by GET /api/admin/workspaces.
type AdminWorkspaceItem struct {
	ID           string          `json:"id" validate:"required"`
	Name         string          `json:"name" validate:"required"`
	CreatedAt    string          `json:"created_at" validate:"required"`
	UpdatedAt    string          `json:"updated_at" validate:"required"`
	Owner        *AdminOwnerInfo `json:"owner" extensions:"x-nullable=true"`
	SandboxCount int             `json:"sandbox_count"`
	MaxSandboxes int             `json:"max_sandboxes"`
} // @name AdminWorkspaceItem

// AdminSandboxItem is one entry returned by GET /api/admin/sandboxes.
type AdminSandboxItem struct {
	ID             string  `json:"id" validate:"required"`
	Name           string  `json:"name" validate:"required"`
	WorkspaceID    string  `json:"workspace_id" validate:"required"`
	Type           string  `json:"type" validate:"required" example:"opencode"`
	Status         string  `json:"status" validate:"required" example:"running"`
	CreatedAt      string  `json:"created_at" validate:"required"`
	LastActivityAt *string `json:"last_activity_at,omitempty" extensions:"x-nullable=true"`
	IsLocal        bool    `json:"is_local"`
} // @name AdminSandboxItem

// AdminUpdateUserRoleRequest is the body for PUT /api/admin/users/{id}/role.
type AdminUpdateUserRoleRequest struct {
	Role string `json:"role" validate:"required" example:"admin"`
} // @name AdminUpdateUserRoleRequest

// AdminQuotaDefaultsResponse is returned by GET /api/admin/quotas/defaults
// and PUT /api/admin/quotas/defaults.
type AdminQuotaDefaultsResponse struct {
	MaxWorkspacesPerUser     int   `json:"max_workspaces_per_user" validate:"required"`
	MaxSandboxesPerWorkspace int   `json:"max_sandboxes_per_workspace" validate:"required"`
	MaxWorkspaceDriveSize    int64 `json:"max_workspace_drive_size" validate:"required"`
	MaxSandboxCPU            int   `json:"max_sandbox_cpu" validate:"required"`
	MaxSandboxMemory         int64 `json:"max_sandbox_memory" validate:"required"`
	MaxIdleTimeout           int   `json:"max_idle_timeout" validate:"required"`
	WsMaxTotalCPU            int   `json:"ws_max_total_cpu" validate:"required"`
	WsMaxTotalMemory         int64 `json:"ws_max_total_memory" validate:"required"`
	WsMaxIdleTimeout         int   `json:"ws_max_idle_timeout" validate:"required"`
} // @name AdminQuotaDefaultsResponse

// AdminQuotaDefaultsUpdateRequest is the body for PUT /api/admin/quotas/defaults.
// All fields are optional; only supplied keys are applied.
type AdminQuotaDefaultsUpdateRequest struct {
	MaxWorkspacesPerUser     *int   `json:"max_workspaces_per_user,omitempty"`
	MaxSandboxesPerWorkspace *int   `json:"max_sandboxes_per_workspace,omitempty"`
	MaxWorkspaceDriveSize    *int64 `json:"max_workspace_drive_size,omitempty"`
	MaxSandboxCPU            *int   `json:"max_sandbox_cpu,omitempty"`
	MaxSandboxMemory         *int64 `json:"max_sandbox_memory,omitempty"`
	MaxIdleTimeout           *int   `json:"max_idle_timeout,omitempty"`
	WsMaxTotalCPU            *int   `json:"ws_max_total_cpu,omitempty"`
	WsMaxTotalMemory         *int64 `json:"ws_max_total_memory,omitempty"`
	WsMaxIdleTimeout         *int   `json:"ws_max_idle_timeout,omitempty"`
} // @name AdminQuotaDefaultsUpdateRequest

// AdminUserQuotaDefaults is the system-default quota sub-object embedded in
// AdminUserQuotaResponse.
type AdminUserQuotaDefaults struct {
	MaxWorkspacesPerUser int `json:"max_workspaces_per_user" validate:"required"`
} // @name AdminUserQuotaDefaults

// AdminUserQuotaOverrides is the per-user override sub-object embedded in
// AdminUserQuotaResponse. null when no override is set.
type AdminUserQuotaOverrides struct {
	MaxWorkspaces *int   `json:"max_workspaces" extensions:"x-nullable=true"`
	UpdatedAt     string `json:"updated_at" validate:"required"`
} // @name AdminUserQuotaOverrides

// AdminUserQuotaResponse is returned by GET /api/admin/users/{id}/quota.
type AdminUserQuotaResponse struct {
	Defaults  AdminUserQuotaDefaults   `json:"defaults" validate:"required"`
	Overrides *AdminUserQuotaOverrides `json:"overrides" extensions:"x-nullable=true"`
} // @name AdminUserQuotaResponse

// AdminSetUserQuotaRequest is the body for PUT /api/admin/users/{id}/quota.
type AdminSetUserQuotaRequest struct {
	MaxWorkspaces *int `json:"max_workspaces" extensions:"x-nullable=true"`
} // @name AdminSetUserQuotaRequest

// AdminWorkspaceQuotaDefaults is the system-default quota sub-object embedded in
// AdminWorkspaceQuotaResponse.
type AdminWorkspaceQuotaDefaults struct {
	MaxSandboxes     int   `json:"max_sandboxes" validate:"required"`
	MaxSandboxCPU    int   `json:"max_sandbox_cpu" validate:"required"`
	MaxSandboxMemory int64 `json:"max_sandbox_memory" validate:"required"`
	MaxIdleTimeout   int   `json:"max_idle_timeout" validate:"required"`
	MaxTotalCPU      int   `json:"max_total_cpu" validate:"required"`
	MaxTotalMemory   int64 `json:"max_total_memory" validate:"required"`
	MaxDriveSize     int64 `json:"max_drive_size" validate:"required"`
} // @name AdminWorkspaceQuotaDefaults

// AdminWorkspaceQuotaOverrides is the per-workspace override sub-object embedded in
// AdminWorkspaceQuotaResponse. null when no override is set.
type AdminWorkspaceQuotaOverrides struct {
	MaxSandboxes     *int   `json:"max_sandboxes" extensions:"x-nullable=true"`
	MaxSandboxCPU    *int   `json:"max_sandbox_cpu" extensions:"x-nullable=true"`
	MaxSandboxMemory *int64 `json:"max_sandbox_memory" extensions:"x-nullable=true"`
	MaxIdleTimeout   *int   `json:"max_idle_timeout" extensions:"x-nullable=true"`
	MaxTotalCPU      *int   `json:"max_total_cpu" extensions:"x-nullable=true"`
	MaxTotalMemory   *int64 `json:"max_total_memory" extensions:"x-nullable=true"`
	MaxDriveSize     *int64 `json:"max_drive_size" extensions:"x-nullable=true"`
	UpdatedAt        string `json:"updated_at" validate:"required"`
} // @name AdminWorkspaceQuotaOverrides

// AdminWorkspaceQuotaResponse is returned by GET /api/admin/workspaces/{id}/quota.
type AdminWorkspaceQuotaResponse struct {
	Defaults  AdminWorkspaceQuotaDefaults   `json:"defaults" validate:"required"`
	Overrides *AdminWorkspaceQuotaOverrides `json:"overrides" extensions:"x-nullable=true"`
} // @name AdminWorkspaceQuotaResponse

// AdminSetWorkspaceQuotaRequest is the body for PUT /api/admin/workspaces/{id}/quota.
// All fields are optional; only supplied keys are applied (merged with existing).
type AdminSetWorkspaceQuotaRequest struct {
	MaxSandboxes     *int   `json:"max_sandboxes,omitempty" extensions:"x-nullable=true"`
	MaxSandboxCPU    *int   `json:"max_sandbox_cpu,omitempty" extensions:"x-nullable=true"`
	MaxSandboxMemory *int64 `json:"max_sandbox_memory,omitempty" extensions:"x-nullable=true"`
	MaxIdleTimeout   *int   `json:"max_idle_timeout,omitempty" extensions:"x-nullable=true"`
	MaxTotalCPU      *int   `json:"max_total_cpu,omitempty" extensions:"x-nullable=true"`
	MaxTotalMemory   *int64 `json:"max_total_memory,omitempty" extensions:"x-nullable=true"`
	MaxDriveSize     *int64 `json:"max_drive_size,omitempty" extensions:"x-nullable=true"`
} // @name AdminSetWorkspaceQuotaRequest
