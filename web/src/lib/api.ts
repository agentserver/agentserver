import { apiFetch, ApiError } from './apiClient'
import type { components } from './api-generated/schema'

export type SandboxStatus = 'creating' | 'running' | 'pausing' | 'paused' | 'resuming' | 'offline'
export type WorkspaceRole = 'owner' | 'maintainer' | 'developer' | 'guest'

export type Workspace = components['schemas']['Workspace']
export type WorkspaceMember = components['schemas']['WorkspaceMember']
export type LLMModel = components['schemas']['LLMModel']
export type WorkspaceLLMConfig = components['schemas']['LLMConfigResponse']

export type WeixinBinding = components['schemas']['IMBinding']

export type IMBinding = components['schemas']['IMBinding']

// IM Channels — generated types from OpenAPI spec
export type IMChannel = components['schemas']['IMChannel']
export type IMChannelListResponse = components['schemas']['IMChannelListResponse']
export type IMChannelPatchRequest = components['schemas']['IMChannelPatchRequest']
export type IMWeixinQRStartResponse = components['schemas']['IMWeixinQRStartResponse']
export type IMWeixinQRWaitResponse = components['schemas']['IMWeixinQRWaitResponse']
export type IMTelegramConfigureRequest = components['schemas']['IMTelegramConfigureRequest']
export type IMTelegramConfigureResponse = components['schemas']['IMTelegramConfigureResponse']
export type IMMatrixConfigureRequest = components['schemas']['IMMatrixConfigureRequest']
export type IMMatrixConfigureResponse = components['schemas']['IMMatrixConfigureResponse']
export type IMSandboxBindRequest = components['schemas']['IMSandboxBindRequest']

// Codex Tokens — generated types from OpenAPI spec
export type CodexToken = components['schemas']['CodexTokenListItem']
export type MintCodexTokenRequest = components['schemas']['CodexTokenMintRequest']
export type MintCodexTokenResponse = components['schemas']['CodexTokenMintResponse']

// Codex Browser Sessions — generated types from OpenAPI spec
export type CodexBrowser = components['schemas']['CodexBrowserItem']

// Misc — generated types from OpenAPI spec
export type CredentialBinding = components['schemas']['CredentialBindingItem']
export type CredentialBindingCreateRequest = components['schemas']['CredentialBindingCreateRequest']
export type CredentialBindingCreateResponse = components['schemas']['CredentialBindingCreateResponse']
export type CredentialBindingPatchRequest = components['schemas']['CredentialBindingPatchRequest']
export type WorkspaceSandboxDefaults = components['schemas']['WorkspaceDefaultsResponse']
export type ModelserverStatus = components['schemas']['ModelServerStatusResponse']
export type TraceItem = components['schemas']['TraceRecord']
export type TracesResponse = components['schemas']['TraceListResponse']
export type ExecutorItem = components['schemas']['ExecutorItem']
export type ExecutorRegisterResponse = components['schemas']['ExecutorRegisterResponse']
export type AgentInteractionItem = components['schemas']['AgentInteractionItem']
export type OperationRecord = components['schemas']['OperationRecord']
export type WorkspaceOperationsResponse = components['schemas']['WorkspaceOperationsResponse']

// Admin — generated types from OpenAPI spec
export type AdminUser = components['schemas']['AdminUserItem']
export type AdminWorkspaceOwner = components['schemas']['AdminOwnerInfo']
export type AdminWorkspace = components['schemas']['AdminWorkspaceItem']
export type AdminSandbox = components['schemas']['AdminSandboxItem']
export type QuotaDefaults = components['schemas']['AdminQuotaDefaultsResponse']
export type UserQuotaResponse = components['schemas']['AdminUserQuotaResponse']
export type UserQuotaOverrides = components['schemas']['AdminUserQuotaOverrides']
export type WorkspaceQuotaResponse = components['schemas']['AdminWorkspaceQuotaResponse']
export type WorkspaceQuotaDefaults = components['schemas']['AdminWorkspaceQuotaDefaults']
export type WorkspaceQuotaOverrides = components['schemas']['AdminWorkspaceQuotaOverrides']

export interface TelegramConfigureResult {
  connected: boolean
  bot_id: string
  bot_name: string
}

export interface MatrixConfigureResult {
  connected: boolean
  bot_id: string
  user_id: string
}

export type Sandbox = components['schemas']['Sandbox']
export type SandboxCreateRequest = components['schemas']['SandboxCreateRequest']
export type SandboxUsage = components['schemas']['SandboxUsage']
export type SandboxUsageSummary = components['schemas']['SandboxUsageSummary']

export type AgentInfo = components['schemas']['AgentInfo']

export async function login(email: string, password: string): Promise<boolean> {
  try {
    await apiFetch<components['schemas']['AuthStatusResponse']>({
      method: 'POST',
      path: '/api/auth/login',
      body: { email, password } satisfies components['schemas']['AuthCredentials'],
    })
    return true
  } catch (err) {
    if (err instanceof ApiError) return false
    throw err
  }
}

export async function register(email: string, password: string): Promise<boolean> {
  try {
    await apiFetch<components['schemas']['AuthRegisterResponse']>({
      method: 'POST',
      path: '/api/auth/register',
      body: { email, password } satisfies components['schemas']['AuthCredentials'],
    })
    return true
  } catch (err) {
    if (err instanceof ApiError) return false
    throw err
  }
}

export async function checkAuth(): Promise<boolean> {
  try {
    await apiFetch<components['schemas']['AuthStatusResponse']>({
      method: 'GET',
      path: '/api/auth/check',
    })
    return true
  } catch (err) {
    if (err instanceof ApiError) return false
    throw err
  }
}

export async function getOIDCProviders(): Promise<{ providers: string[]; password_auth: boolean }> {
  const res = await fetch('/api/auth/oidc/providers')
  if (!res.ok) return { providers: [], password_auth: true }
  const data = await res.json()
  return {
    providers: data.providers || [],
    password_auth: data.password_auth !== false,
  }
}

export async function getMe(): Promise<{ id: string; email: string; name?: string | null; picture?: string | null; role: string }> {
  const data = await apiFetch<components['schemas']['AuthMeResponse']>({
    method: 'GET',
    path: '/api/auth/me',
  })
  return {
    id: data.id,
    email: data.email,
    name: data.name ?? null,
    picture: data.picture ?? null,
    role: data.role,
  }
}

export async function logout(): Promise<void> {
  try {
    await apiFetch<components['schemas']['AuthStatusResponse']>({
      method: 'POST',
      path: '/api/auth/logout',
    })
  } catch {
    // logout is idempotent — swallow any error so the SPA can always
    // navigate to the login screen.
  }
}

// Workspace API

export async function listWorkspaces(): Promise<Workspace[]> {
  return apiFetch<Workspace[]>({ method: 'GET', path: '/api/workspaces' })
}

export async function createWorkspace(name?: string): Promise<Workspace> {
  try {
    return await apiFetch<Workspace>({
      method: 'POST',
      path: '/api/workspaces',
      body: { name: name || 'New Workspace' } satisfies components['schemas']['WorkspaceCreateRequest'],
    })
  } catch (err) {
    if (err instanceof ApiError) {
      // Re-throw structured quota errors so callers can inspect .error / .message
      const body = err.body as { error?: string; message?: string } | null | undefined
      if (body?.error === 'quota_exceeded' || body?.error === 'resource_budget_exceeded') throw body
    }
    throw err
  }
}

export async function getWorkspace(id: string): Promise<Workspace> {
  return apiFetch<Workspace>({ method: 'GET', path: `/api/workspaces/${encodeURIComponent(id)}` })
}

export async function deleteWorkspace(id: string): Promise<void> {
  await apiFetch<void>({ method: 'DELETE', path: `/api/workspaces/${encodeURIComponent(id)}` })
}

export async function renameWorkspace(id: string, name: string): Promise<Workspace> {
  return apiFetch<Workspace>({
    method: 'PATCH',
    path: `/api/workspaces/${encodeURIComponent(id)}`,
    body: { name } satisfies components['schemas']['WorkspaceRenameRequest'],
  })
}

export async function getWorkspacesQuota(): Promise<components['schemas']['WorkspaceQuotaResponse']> {
  return apiFetch<components['schemas']['WorkspaceQuotaResponse']>({ method: 'GET', path: '/api/workspaces/quota' })
}

// Workspace member API

export async function listMembers(workspaceId: string): Promise<WorkspaceMember[]> {
  return apiFetch<WorkspaceMember[]>({
    method: 'GET',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/members`,
  })
}

export async function addMember(workspaceId: string, email: string, role?: string): Promise<WorkspaceMember> {
  return apiFetch<WorkspaceMember>({
    method: 'POST',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/members`,
    body: { email, role: role ?? 'developer' } satisfies components['schemas']['MemberAddRequest'],
  })
}

export async function updateMemberRole(workspaceId: string, userId: string, role: string): Promise<void> {
  await apiFetch<void>({
    method: 'PUT',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/members/${encodeURIComponent(userId)}`,
    body: { role } satisfies components['schemas']['MemberRoleUpdateRequest'],
  })
}

export async function removeMember(workspaceId: string, userId: string): Promise<void> {
  await apiFetch<void>({
    method: 'DELETE',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/members/${encodeURIComponent(userId)}`,
  })
}

// Sandbox API

export async function getWorkspaceDefaults(workspaceId: string): Promise<WorkspaceSandboxDefaults> {
  const res = await fetch(`/api/workspaces/${workspaceId}/defaults`)
  if (!res.ok) throw new Error('Failed to get workspace defaults')
  return res.json()
}

export type WorkspaceLLMQuota = components['schemas']['LLMQuotaResponse']

export async function getWorkspaceLLMQuota(workspaceId: string): Promise<WorkspaceLLMQuota> {
  return apiFetch<WorkspaceLLMQuota>({
    method: 'GET',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/llm-quota`,
  })
}

// Workspace BYOK LLM config

export async function getWorkspaceLLMConfig(workspaceId: string): Promise<WorkspaceLLMConfig> {
  return apiFetch<WorkspaceLLMConfig>({
    method: 'GET',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/llm-config`,
  })
}

export async function setWorkspaceLLMConfig(
  workspaceId: string,
  config: { base_url: string; api_key: string; models: LLMModel[] }
): Promise<void> {
  await apiFetch<components['schemas']['LLMConfigUpsertResponse']>({
    method: 'PUT',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/llm-config`,
    body: config satisfies components['schemas']['LLMConfigUpsertRequest'],
  })
}

export async function deleteWorkspaceLLMConfig(workspaceId: string): Promise<void> {
  await apiFetch<void>({
    method: 'DELETE',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/llm-config`,
  })
}

// ModelServer connection
export async function getModelserverStatus(workspaceId: string): Promise<ModelserverStatus> {
  const res = await fetch(`/api/workspaces/${workspaceId}/modelserver/status`)
  if (!res.ok) throw new Error('Failed to fetch modelserver status')
  return res.json()
}

export async function disconnectModelserver(workspaceId: string): Promise<void> {
  const res = await fetch(`/api/workspaces/${workspaceId}/modelserver/disconnect`, { method: 'DELETE' })
  if (!res.ok) throw new Error('Failed to disconnect')
}


export async function listSandboxes(workspaceId: string): Promise<Sandbox[]> {
  return apiFetch<Sandbox[]>({
    method: 'GET',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/sandboxes`,
  })
}

export async function createSandbox(
  workspaceId: string,
  name?: string,
  type?: 'opencode' | 'nanoclaw' | 'claudecode' | 'jupyter',
  cpu?: number,
  memory?: number,
  idleTimeout?: number,
  metadata?: Record<string, unknown>,
): Promise<Sandbox> {
  const body: SandboxCreateRequest = {
    name: name || 'New Sandbox',
    type: type || 'opencode',
    ...(cpu !== undefined && { cpu }),
    ...(memory !== undefined && { memory }),
    ...(idleTimeout !== undefined && { idle_timeout: idleTimeout }),
    ...(metadata !== undefined && { metadata }),
  }
  try {
    return await apiFetch<Sandbox>({
      method: 'POST',
      path: `/api/workspaces/${encodeURIComponent(workspaceId)}/sandboxes`,
      body,
    })
  } catch (err) {
    if (err instanceof ApiError && err.status === 403) {
      const errBody = err.body as Record<string, unknown> | null
      if (errBody?.error === 'quota_exceeded') throw errBody as unknown as QuotaExceededError
      if (errBody?.error === 'resource_budget_exceeded') throw errBody as unknown as ResourceBudgetExceededError
    }
    throw err
  }
}

export async function getSandbox(id: string): Promise<Sandbox> {
  return apiFetch<Sandbox>({
    method: 'GET',
    path: `/api/sandboxes/${encodeURIComponent(id)}`,
  })
}

export async function deleteSandbox(id: string): Promise<void> {
  await apiFetch<void>({
    method: 'DELETE',
    path: `/api/sandboxes/${encodeURIComponent(id)}`,
  })
}

export async function renameSandbox(id: string, name: string): Promise<Sandbox> {
  return apiFetch<Sandbox>({
    method: 'PATCH',
    path: `/api/sandboxes/${encodeURIComponent(id)}`,
    body: { name } satisfies components['schemas']['SandboxRenameRequest'],
  })
}

export async function pauseSandbox(id: string): Promise<components['schemas']['SandboxLifecycleStatusResponse']> {
  return apiFetch<components['schemas']['SandboxLifecycleStatusResponse']>({
    method: 'POST',
    path: `/api/sandboxes/${encodeURIComponent(id)}/pause`,
  })
}

export async function resumeSandbox(id: string): Promise<components['schemas']['SandboxLifecycleStatusResponse']> {
  return apiFetch<components['schemas']['SandboxLifecycleStatusResponse']>({
    method: 'POST',
    path: `/api/sandboxes/${encodeURIComponent(id)}/resume`,
  })
}

// WeChat QR Login API

export interface WeixinQRStartResult {
  qrcode_url: string
  message: string
}

export interface WeixinQRWaitResult {
  connected: boolean
  status: 'wait' | 'scaned' | 'confirmed' | 'expired'
  message: string
  qrcode_url?: string
  bot_id?: string
  user_id?: string
}

export async function weixinQRStart(sandboxId: string): Promise<WeixinQRStartResult> {
  const res = await fetch(`/api/sandboxes/${sandboxId}/im/weixin/qr-start`, { method: 'POST' })
  if (!res.ok) throw new Error('Failed to start WeChat login')
  return res.json()
}

export async function weixinQRWait(sandboxId: string): Promise<WeixinQRWaitResult> {
  const res = await fetch(`/api/sandboxes/${sandboxId}/im/weixin/qr-wait`, { method: 'POST' })
  if (!res.ok) throw new Error('Failed to poll WeChat login status')
  return res.json()
}

export async function telegramConfigure(sandboxId: string, botToken: string): Promise<TelegramConfigureResult> {
  const res = await fetch(`/api/sandboxes/${sandboxId}/im/telegram/configure`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ bot_token: botToken }),
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || 'Failed to configure Telegram bot')
  }
  return res.json()
}

export async function telegramDisconnect(sandboxId: string): Promise<void> {
  const res = await fetch(`/api/sandboxes/${sandboxId}/im/telegram`, { method: 'DELETE' })
  if (!res.ok) throw new Error('Failed to disconnect Telegram')
}

export async function matrixConfigure(sandboxId: string, homeserverUrl: string, accessToken: string, recoveryKey?: string): Promise<MatrixConfigureResult> {
  const res = await fetch(`/api/sandboxes/${sandboxId}/im/matrix/configure`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ homeserver_url: homeserverUrl, access_token: accessToken, recovery_key: recoveryKey || '' }),
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || 'Failed to configure Matrix bot')
  }
  return res.json()
}

export async function matrixDisconnect(sandboxId: string): Promise<void> {
  const res = await fetch(`/api/sandboxes/${sandboxId}/im/matrix`, { method: 'DELETE' })
  if (!res.ok) throw new Error('Failed to disconnect Matrix')
}

export async function listIMBindings(sandboxId: string): Promise<{ bindings: IMBinding[] }> {
  const res = await fetch(`/api/sandboxes/${sandboxId}/im/bindings`)
  if (!res.ok) throw new Error('Failed to list IM bindings')
  return res.json()
}

// Workspace IM Channel management API

export async function listWorkspaceIMChannels(workspaceId: string): Promise<IMChannelListResponse> {
  return apiFetch<IMChannelListResponse>({
    method: 'GET',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/im/channels`,
  })
}

export async function updateWorkspaceIMChannel(
  workspaceId: string,
  channelId: string,
  settings: IMChannelPatchRequest,
): Promise<void> {
  await apiFetch<void>({
    method: 'PATCH',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/im/channels/${encodeURIComponent(channelId)}`,
    body: settings,
  })
}

export async function deleteWorkspaceIMChannel(workspaceId: string, channelId: string): Promise<void> {
  await apiFetch<void>({
    method: 'DELETE',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/im/channels/${encodeURIComponent(channelId)}`,
  })
}

// Credential Bindings (kubeconfig / external API credentials)

export async function listCredentialBindings(workspaceId: string, kind: string): Promise<CredentialBinding[]> {
  const res = await fetch(`/api/workspaces/${workspaceId}/credentials/${kind}`)
  if (!res.ok) throw new Error('Failed to list credential bindings')
  return res.json()
}

// DeviceCodeResponse is kept as a strict subtype of CredentialBindingCreateResponse
// for callers that rely on user_code/verification_uri being non-optional.
export interface DeviceCodeResponse {
  id: string
  status: 'pending_device_code'
  verification_uri: string
  user_code: string
  expires_in: number
}

export async function createCredentialBinding(
  workspaceId: string,
  kind: string,
  displayName: string,
  config: string,
): Promise<CredentialBinding | DeviceCodeResponse> {
  const res = await fetch(`/api/workspaces/${workspaceId}/credentials/${kind}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ display_name: displayName, config }),
  })
  if (res.status === 202) {
    return res.json() as Promise<DeviceCodeResponse>
  }
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || 'Failed to create credential binding')
  }
  return res.json()
}

export async function deleteCredentialBinding(workspaceId: string, kind: string, bindingId: string): Promise<void> {
  const res = await fetch(`/api/workspaces/${workspaceId}/credentials/${kind}/${bindingId}`, { method: 'DELETE' })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || 'Failed to delete credential binding')
  }
}

export async function setDefaultCredentialBinding(workspaceId: string, kind: string, bindingId: string): Promise<void> {
  const res = await fetch(`/api/workspaces/${workspaceId}/credentials/${kind}/${bindingId}/set-default`, { method: 'POST' })
  if (!res.ok) throw new Error('Failed to set default credential binding')
}

export async function patchCredentialBinding(workspaceId: string, kind: string, bindingId: string, displayName: string): Promise<void> {
  const res = await fetch(`/api/workspaces/${workspaceId}/credentials/${kind}/${bindingId}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ display_name: displayName }),
  })
  if (!res.ok) throw new Error('Failed to update credential binding')
}

export async function pollDeviceCodeComplete(
  workspaceId: string,
  kind: string,
  bindingId: string,
  signal?: AbortSignal,
): Promise<CredentialBinding> {
  const res = await fetch(
    `/api/workspaces/${workspaceId}/credentials/${kind}/${bindingId}/device-complete`,
    { method: 'POST', signal },
  )
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || 'Device code authorization failed')
  }
  return res.json()
}

// Workspace-level WeChat QR login
export async function workspaceWeixinQRStart(workspaceId: string): Promise<IMWeixinQRStartResponse> {
  return apiFetch<IMWeixinQRStartResponse>({
    method: 'POST',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/im/weixin/qr-start`,
  })
}

export async function workspaceWeixinQRWait(workspaceId: string): Promise<IMWeixinQRWaitResponse> {
  return apiFetch<IMWeixinQRWaitResponse>({
    method: 'POST',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/im/weixin/qr-wait`,
  })
}

// Workspace-level Telegram configure
export async function workspaceTelegramConfigure(workspaceId: string, botToken: string): Promise<IMTelegramConfigureResponse> {
  return apiFetch<IMTelegramConfigureResponse>({
    method: 'POST',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/im/telegram/configure`,
    body: { bot_token: botToken } satisfies IMTelegramConfigureRequest,
  })
}

// Workspace-level Matrix configure
export async function workspaceMatrixConfigure(workspaceId: string, homeserverUrl: string, accessToken: string, recoveryKey?: string): Promise<IMMatrixConfigureResponse> {
  return apiFetch<IMMatrixConfigureResponse>({
    method: 'POST',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/im/matrix/configure`,
    body: { homeserver_url: homeserverUrl, access_token: accessToken, recovery_key: recoveryKey || '' } satisfies IMMatrixConfigureRequest,
  })
}

// Sandbox channel binding
export async function bindSandboxToChannel(sandboxId: string, channelId: string): Promise<void> {
  await apiFetch<void>({
    method: 'POST',
    path: `/api/sandboxes/${encodeURIComponent(sandboxId)}/im/bind`,
    body: { channel_id: channelId } satisfies IMSandboxBindRequest,
  })
}

export async function unbindSandboxFromChannel(sandboxId: string): Promise<void> {
  await apiFetch<void>({
    method: 'DELETE',
    path: `/api/sandboxes/${encodeURIComponent(sandboxId)}/im/bind`,
  })
}

// Usage & Traces API

/** @deprecated use SandboxUsageSummary (generated alias) */
export type UsageSummary = SandboxUsageSummary

/** @deprecated use SandboxUsage (generated alias) */
export type UsageResponse = SandboxUsage

export async function getSandboxUsage(id: string): Promise<SandboxUsage> {
  return apiFetch<SandboxUsage>({
    method: 'GET',
    path: `/api/sandboxes/${encodeURIComponent(id)}/usage`,
  })
}

export async function getSandboxTraces(id: string, limit: number, offset: number): Promise<TracesResponse> {
  const res = await fetch(`/api/sandboxes/${id}/traces?limit=${limit}&offset=${offset}`)
  if (!res.ok) throw new Error('Failed to get sandbox traces')
  return res.json()
}

export interface TokenUsageItem {
  id: string
  trace_id: string
  provider: string
  model: string
  message_id?: string
  input_tokens: number
  output_tokens: number
  cache_creation_input_tokens: number
  cache_read_input_tokens: number
  streaming: boolean
  duration: number
  ttft: number
  created_at: string
}

export interface TraceDetailResponse {
  trace: TraceItem
  requests: TokenUsageItem[]
}

export async function getTraceDetail(sandboxId: string, traceId: string): Promise<TraceDetailResponse> {
  const res = await fetch(`/api/sandboxes/${sandboxId}/traces/${traceId}`)
  if (!res.ok) throw new Error('Failed to get trace detail')
  return res.json()
}

export async function getWorkspaceTraces(workspaceId: string, limit: number, offset: number): Promise<TracesResponse> {
  const res = await fetch(`/api/workspaces/${workspaceId}/traces?limit=${limit}&offset=${offset}`)
  if (!res.ok) throw new Error('Failed to get workspace traces')
  return res.json()
}

export async function getWorkspaceTraceDetail(workspaceId: string, traceId: string): Promise<TraceDetailResponse> {
  const res = await fetch(`/api/workspaces/${workspaceId}/traces/${traceId}`)
  if (!res.ok) throw new Error('Failed to get trace detail')
  return res.json()
}

// Admin API

export async function adminListUsers(): Promise<AdminUser[]> {
  const res = await fetch('/api/admin/users')
  if (!res.ok) throw new Error('Failed to list users')
  return res.json()
}

export async function adminListWorkspaces(): Promise<AdminWorkspace[]> {
  const res = await fetch('/api/admin/workspaces')
  if (!res.ok) throw new Error('Failed to list workspaces')
  return res.json()
}

export async function adminListSandboxes(): Promise<AdminSandbox[]> {
  const res = await fetch('/api/admin/sandboxes')
  if (!res.ok) throw new Error('Failed to list sandboxes')
  return res.json()
}

export async function adminUpdateUserRole(userId: string, role: string): Promise<void> {
  const res = await fetch(`/api/admin/users/${userId}/role`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ role }),
  })
  if (!res.ok) throw new Error('Failed to update user role')
}

export interface QuotaExceededError {
  error: 'quota_exceeded'
  message: string
  quota: { current: number; max: number }
}

export interface ResourceBudgetExceededError {
  error: 'resource_budget_exceeded'
  message: string
}

// Admin quota API

export async function adminGetQuotaDefaults(): Promise<QuotaDefaults> {
  const res = await fetch('/api/admin/quotas/defaults')
  if (!res.ok) throw new Error('Failed to get quota defaults')
  return res.json()
}

export async function adminSetQuotaDefaults(defaults: Partial<QuotaDefaults>): Promise<QuotaDefaults> {
  const res = await fetch('/api/admin/quotas/defaults', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(defaults),
  })
  if (!res.ok) throw new Error('Failed to set quota defaults')
  return res.json()
}

export async function adminGetUserQuota(userId: string): Promise<UserQuotaResponse> {
  const res = await fetch(`/api/admin/users/${userId}/quota`)
  if (!res.ok) throw new Error('Failed to get user quota')
  return res.json()
}

export async function adminSetUserQuota(
  userId: string,
  overrides: {
    max_workspaces?: number
  }
): Promise<void> {
  const res = await fetch(`/api/admin/users/${userId}/quota`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(overrides),
  })
  if (!res.ok) throw new Error('Failed to set user quota')
}

export async function adminDeleteUserQuota(userId: string): Promise<void> {
  const res = await fetch(`/api/admin/users/${userId}/quota`, { method: 'DELETE' })
  if (!res.ok) throw new Error('Failed to delete user quota')
}

// Workspace quota API

export async function adminGetWorkspaceQuota(workspaceId: string): Promise<WorkspaceQuotaResponse> {
  const res = await fetch(`/api/admin/workspaces/${workspaceId}/quota`)
  if (!res.ok) throw new Error('Failed to get workspace quota')
  return res.json()
}

export async function adminSetWorkspaceQuota(
  workspaceId: string,
  overrides: {
    max_sandboxes?: number
    max_sandbox_cpu?: number
    max_sandbox_memory?: number
    max_idle_timeout?: number
    max_total_cpu?: number
    max_total_memory?: number
    max_drive_size?: number
  }
): Promise<void> {
  const res = await fetch(`/api/admin/workspaces/${workspaceId}/quota`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(overrides),
  })
  if (!res.ok) throw new Error('Failed to set workspace quota')
}

export async function adminDeleteWorkspaceQuota(workspaceId: string): Promise<void> {
  const res = await fetch(`/api/admin/workspaces/${workspaceId}/quota`, { method: 'DELETE' })
  if (!res.ok) throw new Error('Failed to delete workspace quota')
}

// LLM Quota management (proxied to llmproxy)
export type LLMQuotaResponse = components['schemas']['LLMQuotaResponse']

export async function adminGetWorkspaceLLMQuota(workspaceId: string): Promise<LLMQuotaResponse> {
  const res = await fetch(`/api/admin/workspaces/${workspaceId}/llm-quota`)
  if (!res.ok) throw new Error('Failed to get LLM quota')
  return res.json()
}

export async function adminSetWorkspaceLLMQuota(workspaceId: string, maxRpd: number): Promise<void> {
  const res = await fetch(`/api/admin/workspaces/${workspaceId}/llm-quota`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ max_rpd: maxRpd }),
  })
  if (!res.ok) throw new Error('Failed to set LLM quota')
}

export async function adminDeleteWorkspaceLLMQuota(workspaceId: string): Promise<void> {
  const res = await fetch(`/api/admin/workspaces/${workspaceId}/llm-quota`, { method: 'DELETE' })
  if (!res.ok) throw new Error('Failed to delete LLM quota')
}

// --- OAuth Device Flow ---

export async function listMyWorkspaces(): Promise<Workspace[]> {
  const res = await fetch('/api/workspaces', { credentials: 'include' })
  if (!res.ok) throw new Error('Failed to list workspaces')
  return res.json()
}

export async function submitOAuthLogin(loginChallenge: string): Promise<{ redirect_to: string }> {
  const res = await fetch(`/api/oauth2/login?login_challenge=${encodeURIComponent(loginChallenge)}`, {
    method: 'POST',
    credentials: 'include',
  })
  if (!res.ok) throw new Error('Failed to submit login')
  return res.json()
}

export async function submitOAuthConsent(
  consentChallenge: string,
  workspaceId: string,
  action: 'accept' | 'deny'
): Promise<{ redirect_to: string }> {
  const res = await fetch(`/api/oauth2/consent?consent_challenge=${encodeURIComponent(consentChallenge)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify({ workspace_id: workspaceId, action }),
  })
  if (!res.ok) throw new Error('Failed to submit consent')
  return res.json()
}

// Codex Token API
// Types are exported at the top of this file as generated aliases.

export async function listCodexTokens(workspaceId: string): Promise<CodexToken[]> {
  return apiFetch<CodexToken[]>({
    method: 'GET',
    path: `/api/codex/tokens?workspace_id=${encodeURIComponent(workspaceId)}`,
  })
}

export async function mintCodexToken(req: MintCodexTokenRequest): Promise<MintCodexTokenResponse> {
  return apiFetch<MintCodexTokenResponse>({
    method: 'POST',
    path: '/api/codex/tokens',
    body: req satisfies components['schemas']['CodexTokenMintRequest'],
  })
}

export async function revokeCodexToken(id: string): Promise<void> {
  await apiFetch<void>({
    method: 'DELETE',
    path: `/api/codex/tokens/${encodeURIComponent(id)}`,
  })
}

// Remote Executor API

export interface RemoteExecutor {
  exe_id: string
  name: string
  description: string
  is_default: boolean
  last_seen_at?: string
  // Live online state from the gateway's in-memory registry. The old
  // client-side `last_seen_at < 90s` heuristic showed freshly-disconnected
  // executors as online for 90s; this is the authoritative replacement.
  is_online: boolean
  client_ip?: string
  client_ua?: string
  codex_version?: string
  os?: string
  connected_at?: string
  disconnected_at?: string
}

export async function listCodexBrowsers(workspaceId: string): Promise<CodexBrowser[]> {
  return apiFetch<CodexBrowser[]>({
    method: 'GET',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/browsers`,
  })
}

export interface RegisterExecutorRequest {
  // Workspace-unique name shown to the LLM (env_id parameter).
  name: string
  description?: string
}

export interface ConnectCommands {
  agent_identity?: string
}

export interface RegisterExecutorResponse {
  exe_id: string
  // Same string as connect_commands.agent_identity, kept for older
  // clients that read the single-string field.
  connect_command?: string
  // The Agent Identity JWT minted for this executor.
  agent_identity_jwt?: string
  // Single-variant Agent-Identity command bundle. Present only when
  // codexAuth is enabled.
  connect_commands?: ConnectCommands
}

export async function listRemoteExecutors(workspaceId: string): Promise<RemoteExecutor[]> {
  const res = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/executors`)
  if (!res.ok) throw new Error('Failed to list remote executors')
  return res.json()
}

export async function registerRemoteExecutor(workspaceId: string, req: RegisterExecutorRequest): Promise<RegisterExecutorResponse> {
  const res = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/executors`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!res.ok) {
    const t = await res.text()
    throw new Error(t || 'Failed to register executor')
  }
  return res.json()
}

export async function unbindRemoteExecutor(workspaceId: string, exeId: string): Promise<void> {
  const res = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/executors/${encodeURIComponent(exeId)}`, { method: 'DELETE' })
  if (!res.ok) throw new Error('Failed to unbind executor')
}

// === Operations (Plan 3c) ===

export interface ListOperationsFilters {
  env_id?: string
  tool?: string
  source?: string
  is_error?: boolean
  since?: string  // RFC3339Nano
  limit?: number  // default 100, max 1000
}

/**
 * List operations for a workspace, server-side filtered.
 */
export async function listOperations(
  workspaceId: string,
  filters: ListOperationsFilters = {},
): Promise<OperationRecord[]> {
  const params = new URLSearchParams()
  if (filters.env_id) params.set('env_id', filters.env_id)
  if (filters.tool) params.set('tool', filters.tool)
  if (filters.source) params.set('source', filters.source)
  if (filters.is_error !== undefined) params.set('is_error', String(filters.is_error))
  if (filters.since) params.set('since', filters.since)
  if (filters.limit) params.set('limit', String(filters.limit))

  const qs = params.toString()
  const path = `/api/workspaces/${encodeURIComponent(workspaceId)}/operations${qs ? `?${qs}` : ''}`
  const data = await apiFetch<WorkspaceOperationsResponse>({ method: 'GET', path })
  return data.operations ?? []
}
