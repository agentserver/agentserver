import createClient from "openapi-fetch"
import type { paths as PublicPaths, components as PublicComponents } from "./generated/public"
import type { paths as EdgePaths, components as EdgeComponents } from "./generated/web-edge"
import type { paths as OAuthPaths, components as OAuthComponents } from "./generated/oauth-public"
import { canonicalID } from "./utils"

export type Workspace = PublicComponents["schemas"]["WorkspaceState"]
export type WorkspaceMember = PublicComponents["schemas"]["WorkspaceMemberState"]
export type Executor = PublicComponents["schemas"]["ExecutorResourceState"]
export type EnrollmentToken = PublicComponents["schemas"]["IssueExecutorEnrollmentTokenResponse"]
export type LLMGateway = PublicComponents["schemas"]["WorkspaceLLMGatewayState"]
export type CreateLLMGateway = PublicComponents["schemas"]["CreateWorkspaceLLMGatewayRequest"]
export type UpdateLLMGateway = PublicComponents["schemas"]["UpdateWorkspaceLLMGatewayRequest"]
export type CredentialProvider = PublicComponents["schemas"]["WorkspaceCredentialProviderSchema"]
export type WorkspaceCredential = PublicComponents["schemas"]["WorkspaceCredentialMetadata"]
export type CredentialAuthorization = PublicComponents["schemas"]["WorkspaceCredentialAuthorization"]
export type BeginCredentialAuthorization = PublicComponents["schemas"]["BeginWorkspaceCredentialAuthorizationRequest"]
export type UserSession = PublicComponents["schemas"]["UserSessionState"]
export type SessionTranscript = PublicComponents["schemas"]["GetUserSessionTranscriptResponse"]
export type SessionTrajectory = PublicComponents["schemas"]["GetUserSessionTrajectoryResponse"]
export type SessionTrajectoryRecord = PublicComponents["schemas"]["UserSessionTrajectoryRecord"]
export type SessionTrajectoryFailure = PublicComponents["schemas"]["UserSessionTrajectoryFailure"]
export type Approval = PublicComponents["schemas"]["ApprovalState"]
export type ApprovalDecision = PublicComponents["schemas"]["DecideUserApprovalRequest"]
export type AuthorizationConfig = EdgeComponents["schemas"]["PlatformAuthorizationConfig"] | EdgeComponents["schemas"]["BrowserAuthorizationConfig"]
export type BrowserAuthorizationConfig = EdgeComponents["schemas"]["BrowserAuthorizationConfig"]
export type AGUIRunRequest = EdgeComponents["schemas"]["AGUIRunRequest"]
export type TokenRequest = OAuthComponents["schemas"]["AuthorizationCodeTokenRequest"]
export type TokenResponse = OAuthComponents["schemas"]["OAuthTokenResponse"]

type OpenAPIResult<T> = { data?: T; error?: unknown; response: Response }

export class APIError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = "APIError"
    this.status = status
    this.code = code
  }
}

function errorDetails(error: unknown): { code: string; message: string } {
  if (error && typeof error === "object" && !Array.isArray(error)) {
    const value = error as Record<string, unknown>
    const code = typeof value.code === "string" ? value.code : typeof value.error === "string" ? value.error : "request_failed"
    const message = typeof value.message === "string" ? value.message : typeof value.error_description === "string" ? value.error_description : "The request could not be completed."
    return { code: code.slice(0, 128), message: message.slice(0, 4096) }
  }
  return { code: "request_failed", message: "The request could not be completed." }
}

function take<T>(result: OpenAPIResult<T>): T {
  if (result.data !== undefined) return result.data
  const details = errorDetails(result.error)
  throw new APIError(result.response.status, details.code, details.message)
}

function boundedFetch(maximumBytes: number) {
  return async (request: Request): Promise<Response> => {
    const response = await globalThis.fetch(request)
    const contentType = response.headers.get("content-type")?.split(";", 1)[0]?.trim().toLowerCase()
    if (contentType !== "application/json" && !contentType?.endsWith("+json")) return response
    const declared = Number(response.headers.get("content-length") ?? "0")
    if (Number.isFinite(declared) && declared > maximumBytes) throw new APIError(response.status, "response_too_large", "The server response exceeded its size limit.")
    const body = await response.arrayBuffer()
    if (body.byteLength > maximumBytes) throw new APIError(response.status, "response_too_large", "The server response exceeded its size limit.")
    return new Response(body, { status: response.status, statusText: response.statusText, headers: response.headers })
  }
}

function commonOptions(baseUrl: string, token = "", maximumBytes = 2 * 1024 * 1024) {
  return {
    baseUrl,
    cache: "no-store" as RequestCache,
    credentials: "omit" as RequestCredentials,
    redirect: "error" as RequestRedirect,
    referrerPolicy: "no-referrer" as ReferrerPolicy,
    headers: token ? { Accept: "application/json", Authorization: `Bearer ${token}` } : { Accept: "application/json" },
    fetch: boundedFetch(maximumBytes),
  }
}

export class ResourceAPI {
  readonly #client

  constructor(baseUrl: string, token: string) {
    this.#client = createClient<PublicPaths>(commonOptions(baseUrl, token))
  }

  async listWorkspaces() {
    return take(await this.#client.GET("/v2/workspaces")).workspaces.map(validateWorkspace)
  }

  async createWorkspace(body: PublicComponents["schemas"]["CreateWorkspaceRequest"]) {
    const result = take(await this.#client.POST("/v2/workspaces", { body }))
    validateWorkspace(result.workspace)
    return result
  }

  async updateWorkspace(workspaceId: string, body: PublicComponents["schemas"]["UpdateWorkspaceRequest"]) {
    const result = take(await this.#client.PATCH("/v2/workspaces/{workspaceId}", { params: { path: { workspaceId } }, body }))
    if (validateWorkspace(result.workspace).workspaceId !== canonicalID("workspace ID", workspaceId)) throw new Error("The workspace response escaped its requested scope.")
    return result
  }

  async archiveWorkspace(workspaceId: string, expectedVersion: number) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/actions/archive", { params: { path: { workspaceId } }, body: { expectedVersion } }))
    if (validateWorkspace(result.workspace).workspaceId !== canonicalID("workspace ID", workspaceId)) throw new Error("The workspace response escaped its requested scope.")
    return result
  }

  async listMembers(workspaceId: string) {
    return take(await this.#client.GET("/v2/workspaces/{workspaceId}/members", { params: { path: { workspaceId } } })).members.map(validateMember)
  }

  async addMember(workspaceId: string, body: PublicComponents["schemas"]["AddWorkspaceMemberRequest"]) {
    return take(await this.#client.POST("/v2/workspaces/{workspaceId}/members", { params: { path: { workspaceId } }, body }))
  }

  async updateMember(workspaceId: string, memberId: string, body: PublicComponents["schemas"]["UpdateWorkspaceMemberRequest"]) {
    return take(await this.#client.PATCH("/v2/workspaces/{workspaceId}/members/{memberId}", { params: { path: { workspaceId, memberId } }, body }))
  }

  async removeMember(workspaceId: string, memberId: string) {
    return take(await this.#client.DELETE("/v2/workspaces/{workspaceId}/members/{memberId}", { params: { path: { workspaceId, memberId } } }))
  }

  async listExecutors(workspaceId: string) {
    return take(await this.#client.GET("/v2/workspaces/{workspaceId}/executors", { params: { path: { workspaceId } } })).executors.map((value) => validateExecutor(value, workspaceId))
  }

  async createExecutor(workspaceId: string, executorId: string) {
    return take(await this.#client.POST("/v2/workspaces/{workspaceId}/executors", { params: { path: { workspaceId } }, body: { executorId } }))
  }

  async archiveExecutor(workspaceId: string, executorId: string) {
    return take(await this.#client.DELETE("/v2/workspaces/{workspaceId}/executors/{executorId}", { params: { path: { workspaceId, executorId } } }))
  }

  async issueEnrollmentToken(workspaceId: string, executorId: string, idempotencyKey: string) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/executors/{executorId}:enrollmentToken", {
      params: { path: { workspaceId, executorId }, header: { "Idempotency-Key": idempotencyKey } },
    }))
    if (result.executorId !== canonicalID("executor ID", executorId) || typeof result.token !== "string" || !/^asv2enr1\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/u.test(result.token) || !validTimestamp(result.expiresAt)) throw new Error("The enrollment token response is invalid.")
    return result
  }

  async listGateways(workspaceId: string) {
    return take(await this.#client.GET("/v2/workspaces/{workspaceId}/llm-gateways", { params: { path: { workspaceId } } })).gateways.map((value) => validateGateway(value, workspaceId))
  }

  async createGateway(workspaceId: string, body: CreateLLMGateway) {
    return take(await this.#client.POST("/v2/workspaces/{workspaceId}/llm-gateways", { params: { path: { workspaceId } }, body }))
  }

  async updateGateway(workspaceId: string, gatewayId: string, body: UpdateLLMGateway) {
    const result = take(await this.#client.PATCH("/v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}", { params: { path: { workspaceId, gatewayId } }, body }))
    if (validateGateway(result.gateway, workspaceId).gatewayId !== canonicalID("gateway ID", gatewayId)) throw new Error("The Gateway response escaped its requested scope.")
    return result
  }

  async beginGatewayAuthorization(workspaceId: string, gatewayId: string, browserBinding: string) {
    return take(await this.#client.POST("/v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}:authorize", {
      params: { path: { workspaceId, gatewayId } }, body: { browserBinding },
    }))
  }

  async completeGatewayAuthorization(workspaceId: string, gatewayId: string, body: PublicComponents["schemas"]["CompleteWorkspaceLLMGatewayAuthorizationRequest"]) {
    return take(await this.#client.POST("/v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}:completeAuthorization", {
      params: { path: { workspaceId, gatewayId } }, body,
    }))
  }

  async revokeGatewayGrant(workspaceId: string, gatewayId: string) {
    return take(await this.#client.POST("/v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}:revoke", { params: { path: { workspaceId, gatewayId } } }))
  }

  async disableGateway(workspaceId: string, gatewayId: string) {
    return take(await this.#client.POST("/v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}:disable", { params: { path: { workspaceId, gatewayId } } }))
  }

  async listCredentialProviders() {
    return take(await this.#client.GET("/v2/credential-providers")).providers.map(validateCredentialProvider)
  }

  async listCredentials(workspaceId: string, kind: string) {
    return take(await this.#client.GET("/v2/workspaces/{workspaceId}/credentials/{kind}", {
      params: { path: { workspaceId, kind } },
    })).bindings.map((value) => validateCredential(value, workspaceId, kind))
  }

  async createCredential(workspaceId: string, kind: string, body: PublicComponents["schemas"]["CreateWorkspaceCredentialRequest"]) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/credentials/{kind}", {
      params: { path: { workspaceId, kind } }, body,
    }))
    validateCredential(result.binding, workspaceId, kind)
    return result
  }

  async renameCredential(workspaceId: string, kind: string, bindingId: string, displayName: string, expectedAuthorityVersion: number) {
    const result = take(await this.#client.PATCH("/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}", {
      params: { path: { workspaceId, kind, bindingId } }, body: { displayName, expectedAuthorityVersion },
    }))
    validateCredential(result.binding, workspaceId, kind, bindingId)
    return result
  }

  async rotateCredential(workspaceId: string, kind: string, bindingId: string, body: PublicComponents["schemas"]["RotateWorkspaceCredentialRequest"]) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}:rotate", {
      params: { path: { workspaceId, kind, bindingId } }, body,
    }))
    validateCredential(result.binding, workspaceId, kind, bindingId)
    return result
  }

  async revokeCredential(workspaceId: string, kind: string, bindingId: string, expectedAuthorityVersion: number) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}:revoke", {
      params: { path: { workspaceId, kind, bindingId } }, body: { expectedAuthorityVersion },
    }))
    validateCredential(result.binding, workspaceId, kind, bindingId)
    return result
  }

  async deleteCredential(workspaceId: string, kind: string, bindingId: string, expectedAuthorityVersion: number) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}:delete", {
      params: { path: { workspaceId, kind, bindingId } }, body: { expectedAuthorityVersion },
    }))
    if (result.bindingId !== canonicalID("credential binding ID", bindingId) || typeof result.deleted !== "boolean") throw new Error("The credential deletion response escaped its requested scope.")
    return result
  }

  async setDefaultCredential(workspaceId: string, kind: string, bindingId: string, expectedAuthorityVersion: number) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}:setDefault", {
      params: { path: { workspaceId, kind, bindingId } }, body: { expectedAuthorityVersion },
    }))
    validateCredential(result.binding, workspaceId, kind, bindingId)
    return result
  }

  async beginCredentialAuthorization(workspaceId: string, kind: string, body: BeginCredentialAuthorization) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/credential-authorizations/{kind}", {
      params: { path: { workspaceId, kind } }, body,
    }))
    validateCredentialAuthorization(result.authorization, workspaceId, kind)
    return result
  }

  async getCredentialAuthorization(workspaceId: string, kind: string, authorizationId: string) {
    const result = take(await this.#client.GET("/v2/workspaces/{workspaceId}/credential-authorizations/{kind}/{authorizationId}", {
      params: { path: { workspaceId, kind, authorizationId } },
    }))
    validateCredentialAuthorization(result.authorization, workspaceId, kind, authorizationId)
    return result
  }

  async pollCredentialAuthorization(workspaceId: string, kind: string, authorizationId: string) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/credential-authorizations/{kind}/{authorizationId}:poll", {
      params: { path: { workspaceId, kind, authorizationId } },
    }))
    validateCredentialAuthorization(result.authorization, workspaceId, kind, authorizationId)
    return result
  }

  async cancelCredentialAuthorization(workspaceId: string, kind: string, authorizationId: string, expectedVersion: number) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/credential-authorizations/{kind}/{authorizationId}:cancel", {
      params: { path: { workspaceId, kind, authorizationId } }, body: { expectedVersion },
    }))
    validateCredentialAuthorization(result.authorization, workspaceId, kind, authorizationId)
    return result
  }

  async listSessions(workspaceId: string) {
    return take(await this.#client.GET("/v2/workspaces/{workspaceId}/sessions", { params: { path: { workspaceId } } })).sessions.map((value) => validateSession(value, workspaceId))
  }

  async createSession(workspaceId: string, body: PublicComponents["schemas"]["CreateUserSessionRequest"]) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/sessions", { params: { path: { workspaceId } }, body }))
    if (validateSession(result.session, workspaceId).sessionId !== canonicalID("session ID", body.sessionId)) throw new Error("The session creation response escaped its requested scope.")
    return result
  }

  async getSessionTranscript(workspaceId: string, sessionId: string) {
    const result = take(await this.#client.GET("/v2/workspaces/{workspaceId}/sessions/{sessionId}/transcript", { params: { path: { workspaceId, sessionId } } }))
    return validateSessionTranscript(result, workspaceId, sessionId)
  }

  async getSessionTrajectory(workspaceId: string, sessionId: string, before?: string, limit = 100) {
    const query: { before?: string; limit?: number } = { limit }
    if (before !== undefined) query.before = before
    const result = take(await this.#client.GET("/v2/workspaces/{workspaceId}/sessions/{sessionId}/trajectory", {
      params: { path: { workspaceId, sessionId }, query },
    }))
    return validateSessionTrajectory(result, workspaceId, sessionId)
  }

  async updateSession(workspaceId: string, sessionId: string, body: PublicComponents["schemas"]["UpdateUserSessionRequest"]) {
    const result = take(await this.#client.PATCH("/v2/workspaces/{workspaceId}/sessions/{sessionId}", { params: { path: { workspaceId, sessionId } }, body }))
    if (validateSession(result.session, workspaceId).sessionId !== canonicalID("session ID", sessionId)) throw new Error("The session update response escaped its requested scope.")
    return result
  }

  async archiveSession(workspaceId: string, sessionId: string, expectedVersion: number) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/sessions/{sessionId}/actions/archive", {
      params: { path: { workspaceId, sessionId } }, body: { expectedVersion },
    }))
    if (result.session.sessionId !== canonicalID("session ID", sessionId) || result.session.workspaceId !== canonicalID("workspace ID", workspaceId) || result.session.status !== "archived") throw new Error("The session archive response escaped its requested scope.")
    return result
  }

  async cancelRun(workspaceId: string, runId: string) {
    return take(await this.#client.POST("/v2/workspaces/{workspaceId}/runs/{runId}:cancel", { params: { path: { workspaceId, runId } } }))
  }

  async decideApproval(workspaceId: string, approvalId: string, body: ApprovalDecision) {
    return take(await this.#client.POST("/v2/workspaces/{workspaceId}/approvals/{approvalId}:decide", { params: { path: { workspaceId, approvalId } }, body }))
  }
}

export class EdgeAPI {
  readonly #client

  constructor(baseUrl: string, token = "") {
    this.#client = createClient<EdgePaths>(commonOptions(baseUrl, token, 128 * 1024))
  }

  async authorizationConfig(): Promise<AuthorizationConfig> {
    return take(await this.#client.GET("/auth/config"))
  }

  async streamRun(workspaceId: string, sessionId: string, idempotencyKey: string, body: AGUIRunRequest, signal: AbortSignal) {
    const result = await this.#client.POST("/v2/workspaces/{workspaceId}/sessions/{sessionId}/agui", {
      params: { path: { workspaceId, sessionId }, header: { "Idempotency-Key": idempotencyKey } },
      body,
      parseAs: "stream",
      signal,
      headers: { Accept: "text/event-stream" },
    })
    const contentType = result.response.headers.get("content-type")?.split(";", 1)[0]?.trim().toLowerCase()
    if (result.data !== undefined && contentType !== "text/event-stream") throw new APIError(result.response.status, "invalid_content_type", "The Browser Gateway did not return an AG-UI event stream.")
    return take(result)
  }
}

export class OAuthAPI {
  readonly #client

  constructor(baseUrl: string) {
    this.#client = createClient<OAuthPaths>({
      ...commonOptions(baseUrl, "", 128 * 1024),
      headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
    })
  }

  async exchange(body: TokenRequest): Promise<TokenResponse> {
    return take(await this.#client.POST("/oauth2/token", {
      body,
      bodySerializer: (value) => new URLSearchParams(value as Record<string, string>).toString(),
    }))
  }
}

function validateWorkspace(value: Workspace): Workspace {
  exactKeys(value, ["workspaceId", "name", "status", "currentUserRole", "managedLarkCredentialMode", "version", "createdAt", "updatedAt"], "workspace")
  canonicalID("workspace ID", value.workspaceId)
  boundedProtocolText(value.name, 256)
  if (!["active", "suspended", "archived"].includes(value.status) || !["owner", "developer", "viewer"].includes(value.currentUserRole) || !["webhook_swap", "process_env"].includes(value.managedLarkCredentialMode) || !positiveVersion(value.version) || !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt)) throw new Error("The workspace response is invalid.")
  return value
}

function validateMember(value: WorkspaceMember): WorkspaceMember {
  exactKeys(value, ["userId", "role", "version", "createdAt", "updatedAt"], "member")
  canonicalID("member ID", value.userId)
  if (!["owner", "developer", "viewer"].includes(value.role) || !positiveVersion(value.version) || !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt)) throw new Error("The member response is invalid.")
  return value
}

function validateExecutor(value: Executor, workspaceId: string): Executor {
  exactKeys(value, ["executorId", "workspaceId", "status", "version", "createdAt", "updatedAt"], "executor")
  canonicalID("executor ID", value.executorId)
  if (value.workspaceId !== canonicalID("workspace ID", workspaceId) || !["enrolling", "offline", "online", "revoked"].includes(value.status) || !positiveVersion(value.version) || !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt)) throw new Error("The executor response escaped its requested scope.")
  return value
}

function validateGateway(value: LLMGateway, workspaceId: string): LLMGateway {
  const keys = ["gatewayId", "workspaceId", "name", "responsesUrl", "oidcIssuer", "oidcClientId", "oidcScopes", "bearerTokenType", "defaultModel", "status", "default", "version", "grantStatus", "createdAt", "updatedAt"]
  if (value.grantExpiresAt !== undefined) keys.push("grantExpiresAt")
  exactKeys(value, keys, "LLM Gateway")
  canonicalID("Gateway ID", value.gatewayId)
  if (value.workspaceId !== canonicalID("workspace ID", workspaceId) || !positiveVersion(value.version) || !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt) || (value.grantExpiresAt !== undefined && !validTimestamp(value.grantExpiresAt))) throw new Error("The Gateway response escaped its requested scope.")
  boundedProtocolText(value.name, 128); boundedProtocolText(value.defaultModel, 256)
  return value
}

function validateCredentialProvider(value: CredentialProvider): CredentialProvider {
  exactKeys(value, ["kind", "displayName", "authTypes", "allowedHosts", "allowedHeaders", "secretFormat", "authorizationMethods"], "credential provider")
  providerKind(value.kind); boundedProtocolText(value.displayName, 256); boundedProtocolText(value.secretFormat, 256)
  boundedStringList(value.authTypes, 64, 128); boundedStringList(value.allowedHosts, 64, 512)
  boundedStringList(value.allowedHeaders, 64, 256); boundedStringList(value.authorizationMethods, 8, 64)
  if (value.authorizationMethods.some((method) => method !== "manual" && method !== "device_flow")) throw new Error("The credential provider response is invalid.")
  return value
}

function validateCredential(value: WorkspaceCredential, workspaceId: string, kind: string, bindingId = ""): WorkspaceCredential {
  const keys = ["id", "workspaceId", "kind", "displayName", "ownerScope", "publicMetadata", "authType", "authorityVersion", "credentialVersion", "status", "isDefault"]
  if (value.ownerUserId !== undefined) keys.push("ownerUserId")
  if (value.accessExpiresAt !== undefined) keys.push("accessExpiresAt")
  if (value.refreshExpiresAt !== undefined) keys.push("refreshExpiresAt")
  exactKeys(value, keys, "credential")
  const canonicalWorkspace = canonicalID("workspace ID", workspaceId)
  const canonicalBinding = canonicalID("credential binding ID", value.id)
  if (value.workspaceId !== canonicalWorkspace || value.kind !== providerKind(kind) || (bindingId && canonicalBinding !== canonicalID("credential binding ID", bindingId)) ||
      !positiveVersion(value.authorityVersion) || !positiveVersion(value.credentialVersion) || typeof value.isDefault !== "boolean" ||
      !["active", "reauth_required", "revoked", "disabled"].includes(value.status) || !["workspace", "user"].includes(value.ownerScope)) throw new Error("The credential response escaped its requested scope.")
  boundedProtocolText(value.displayName, 256); boundedProtocolText(value.authType, 128)
  if (!value.publicMetadata || typeof value.publicMetadata !== "object" || Array.isArray(value.publicMetadata)) throw new Error("The credential public metadata is invalid.")
  if (value.ownerScope === "user") canonicalID("credential owner user ID", value.ownerUserId ?? "")
  if (value.ownerScope === "workspace" && value.ownerUserId !== undefined) throw new Error("The credential owner scope is invalid.")
  if ((value.accessExpiresAt !== undefined && !validTimestamp(value.accessExpiresAt)) || (value.refreshExpiresAt !== undefined && !validTimestamp(value.refreshExpiresAt))) throw new Error("The credential expiry is invalid.")
  return value
}

function validateCredentialAuthorization(value: CredentialAuthorization, workspaceId: string, kind: string, authorizationId = ""): CredentialAuthorization {
  const keys = ["id", "workspaceId", "kind", "targetBindingId", "status", "userCode", "verificationUri", "verificationUriComplete", "pollIntervalSeconds", "nextPollAt", "expiresAt", "version"]
  if (value.lastErrorCode !== undefined) keys.push("lastErrorCode")
  if (value.binding !== undefined) keys.push("binding")
  exactKeys(value, keys, "credential authorization")
  const canonicalWorkspace = canonicalID("workspace ID", workspaceId)
  const canonicalAuthorization = canonicalID("credential authorization ID", value.id)
  if (value.workspaceId !== canonicalWorkspace || value.kind !== providerKind(kind) ||
      (authorizationId && canonicalAuthorization !== canonicalID("credential authorization ID", authorizationId)) ||
      !["pending", "succeeded", "denied", "expired", "cancelled", "failed"].includes(value.status) ||
      !positiveVersion(value.version) || !Number.isInteger(value.pollIntervalSeconds) || value.pollIntervalSeconds < 1 || value.pollIntervalSeconds > 60 ||
      !validTimestamp(value.nextPollAt) || !validTimestamp(value.expiresAt)) throw new Error("The credential authorization response escaped its requested scope.")
  canonicalID("target credential binding ID", value.targetBindingId)
  if (typeof value.userCode !== "string" || value.userCode.length > 1024 || /[\0\r\n]/u.test(value.userCode)) throw new Error("The credential authorization user code is invalid.")
  for (const raw of [value.verificationUri, value.verificationUriComplete]) {
    let parsed: URL
    try { parsed = new URL(raw) } catch { throw new Error("The credential authorization verification URL is invalid.") }
    if (parsed.protocol !== "https:" || parsed.username || parsed.password || raw.length > 8192) throw new Error("The credential authorization verification URL is invalid.")
  }
  if (value.lastErrorCode !== undefined) boundedProtocolText(value.lastErrorCode, 128)
  if (value.binding !== undefined) validateCredential(value.binding, workspaceId, kind, value.targetBindingId)
  if ((value.status === "succeeded") !== (value.binding !== undefined)) throw new Error("The credential authorization terminal response is invalid.")
  return value
}

function validateSession(value: UserSession, workspaceId: string): UserSession {
  const keys = ["sessionId", "workspaceId", "title", "status", "version", "createdAt", "updatedAt"]
  if (value.activeRunId !== undefined) keys.push("activeRunId")
  exactKeys(value, keys, "session")
  canonicalID("session ID", value.sessionId)
  if (value.workspaceId !== canonicalID("workspace ID", workspaceId) || value.status !== "active" || !positiveVersion(value.version) || !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt)) throw new Error("The session response escaped its requested scope.")
  if (value.activeRunId !== undefined) canonicalID("active run ID", value.activeRunId)
  boundedProtocolText(value.title, 256)
  return value
}

function validateSessionTranscript(value: SessionTranscript, workspaceId: string, sessionId: string): SessionTranscript {
  exactKeys(value, ["workspaceId", "sessionId", "messages", "truncated"], "session transcript")
  if (value.workspaceId !== canonicalID("workspace ID", workspaceId) || value.sessionId !== canonicalID("session ID", sessionId) ||
      !Array.isArray(value.messages) || value.messages.length > 512 || typeof value.truncated !== "boolean") throw new Error("The session transcript escaped its requested scope.")
  let contentBytes = 0
  for (const message of value.messages) {
    exactKeys(message, ["messageId", "runId", "role", "content", "complete", "createdAt"], "session transcript message")
    boundedProtocolText(message.messageId, 256); canonicalID("run ID", message.runId)
    if (!['user', 'assistant'].includes(message.role) || typeof message.content !== "string" || /\0/u.test(message.content) ||
        typeof message.complete !== "boolean" || !validTimestamp(message.createdAt)) throw new Error("A session transcript message is invalid.")
    contentBytes += new TextEncoder().encode(message.content).byteLength
    if (contentBytes > 256 * 1024) throw new Error("The session transcript exceeded its content bound.")
  }
  return value
}

export function validateSessionTrajectory(value: SessionTrajectory, workspaceId: string, sessionId: string): SessionTrajectory {
  const keys = ["schemaVersion", "workspaceId", "sessionId", "records", "hasMore", "truncated", "readAt"]
  if (value.activeRunId !== undefined) keys.push("activeRunId")
  if (value.nextBefore !== undefined) keys.push("nextBefore")
  exactKeys(value, keys, "session trajectory")
  if (value.schemaVersion !== 1 || value.workspaceId !== canonicalID("workspace ID", workspaceId) ||
      value.sessionId !== canonicalID("session ID", sessionId) || !Array.isArray(value.records) || value.records.length > 200 ||
      typeof value.hasMore !== "boolean" || typeof value.truncated !== "boolean" || !validTimestamp(value.readAt)) {
    throw new Error("The session trajectory escaped its requested scope.")
  }
  if (value.activeRunId !== undefined) canonicalID("active run ID", value.activeRunId)
  if (value.hasMore !== (value.nextBefore !== undefined)) throw new Error("The session trajectory pagination state is invalid.")
  if (value.nextBefore !== undefined) boundedOpaqueIdentifier(value.nextBefore, 4096)

  const identifiers = new Set<string>()
  let contentBytes = 0
  for (const record of value.records) {
    validateSessionTrajectoryRecord(record)
    if (identifiers.has(record.id)) throw new Error("The session trajectory contains duplicate records.")
    identifiers.add(record.id)
    contentBytes += utf8Bytes(record.input ?? "") + utf8Bytes(record.output ?? "") + utf8Bytes(record.summary)
    contentBytes += record.details.reduce((total, detail) => total + utf8Bytes(detail.name) + utf8Bytes(detail.value), 0)
    if (record.failure !== undefined) contentBytes += utf8Bytes(record.failure.message)
    if (contentBytes > 1024 * 1024) throw new Error("The session trajectory exceeded its content bound.")
  }
  return value
}

function validateSessionTrajectoryRecord(record: SessionTrajectoryRecord) {
  const keys = ["id", "kind", "status", "title", "summary", "runId", "startedAt", "details"]
  for (const optional of ["parentId", "runAttemptId", "runAttemptGeneration", "toolCallId", "executionId", "operationId", "sandboxId", "targetGeneration", "completedAt", "durationMillis", "input", "output", "inputTruncated", "outputTruncated", "failure"] as const) {
    if (record[optional] !== undefined) keys.push(optional)
  }
  exactKeys(record, keys, "session trajectory record")
  boundedOpaqueIdentifier(record.id, 512); canonicalID("trajectory run ID", record.runId)
  if (record.parentId !== undefined) boundedOpaqueIdentifier(record.parentId, 512)
  if (!["run", "input", "attempt", "model", "assistant", "reasoning", "tool", "approval", "execution", "operation", "sandbox", "credential", "checkpoint", "event"].includes(record.kind) ||
      !["queued", "running", "succeeded", "failed", "cancelled", "unknown", "info"].includes(record.status) ||
      !validTimestamp(record.startedAt) || !Array.isArray(record.details) || record.details.length > 32) {
    throw new Error("A session trajectory record is invalid.")
  }
  boundedOpaqueText(record.title, 1024); boundedOpaqueText(record.summary, 4096, true)
  if (record.runAttemptId !== undefined) canonicalID("trajectory attempt ID", record.runAttemptId)
  for (const identifier of [record.toolCallId, record.executionId, record.operationId, record.sandboxId]) {
    if (identifier !== undefined) boundedOpaqueIdentifier(identifier, 512)
  }
  for (const generation of [record.runAttemptGeneration, record.targetGeneration]) {
    if (generation !== undefined && (!Number.isSafeInteger(generation) || generation < 0)) throw new Error("A session trajectory generation is invalid.")
  }
  if (record.completedAt !== undefined && (!validTimestamp(record.completedAt) || Date.parse(record.completedAt) < Date.parse(record.startedAt))) throw new Error("A session trajectory completion time is invalid.")
  if (record.durationMillis !== undefined && (!Number.isSafeInteger(record.durationMillis) || record.durationMillis < 0)) throw new Error("A session trajectory duration is invalid.")
  if ((record.completedAt === undefined) !== (record.durationMillis === undefined)) throw new Error("A session trajectory timing pair is invalid.")
  if (record.input !== undefined) boundedContent(record.input, 16 * 1024)
  if (record.output !== undefined) boundedContent(record.output, 16 * 1024)
  if (record.inputTruncated !== undefined && typeof record.inputTruncated !== "boolean") throw new Error("A session trajectory truncation marker is invalid.")
  if (record.outputTruncated !== undefined && typeof record.outputTruncated !== "boolean") throw new Error("A session trajectory truncation marker is invalid.")
  for (const detail of record.details) {
    exactKeys(detail, ["name", "value"], "session trajectory detail")
    boundedOpaqueText(detail.name, 128); boundedOpaqueText(detail.value, 1024, true)
  }
  if (record.failure !== undefined) validateSessionTrajectoryFailure(record.failure)
}

function validateSessionTrajectoryFailure(failure: SessionTrajectoryFailure) {
  const keys = ["code", "category", "message", "component", "phase", "retryable"]
  if (failure.fingerprint !== undefined) keys.push("fingerprint")
  exactKeys(failure, keys, "session trajectory failure")
  boundedOpaqueText(failure.code, 128); boundedOpaqueText(failure.category, 128)
  boundedOpaqueText(failure.message, 4096); boundedOpaqueText(failure.component, 128); boundedOpaqueText(failure.phase, 128)
  if (typeof failure.retryable !== "boolean") throw new Error("A session trajectory failure is invalid.")
  if (failure.fingerprint !== undefined) boundedOpaqueText(failure.fingerprint, 256)
}

function exactKeys(value: unknown, expected: string[], label: string) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`The ${label} response is invalid.`)
  const actual = Object.keys(value).sort(); const wanted = [...expected].sort()
  if (actual.length !== wanted.length || actual.some((name, index) => name !== wanted[index])) throw new Error(`The ${label} response contains missing or unknown fields.`)
}
function providerKind(value: string) { if (typeof value !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/u.test(value)) throw new Error("The credential provider kind is invalid."); return value }
function boundedStringList(value: string[], maximumItems: number, maximumLength: number) { if (!Array.isArray(value) || value.length > maximumItems || new Set(value).size !== value.length || value.some((item) => typeof item !== "string" || !item || item.length > maximumLength || /[\0\r\n]/u.test(item))) throw new Error("A credential provider list is invalid.") }
function boundedProtocolText(value: string, maximum: number) { if (typeof value !== "string" || !value || value.length > maximum || value.trim() !== value || /[\0\r\n]/u.test(value)) throw new Error("A response text value is invalid.") }
function boundedOpaqueText(value: string, maximumBytes: number, allowEmpty = false) { if (typeof value !== "string" || (!allowEmpty && !value) || utf8Bytes(value) > maximumBytes || /\0/u.test(value)) throw new Error("A trajectory text value is invalid.") }
function boundedOpaqueIdentifier(value: string, maximumBytes: number) { boundedOpaqueText(value, maximumBytes); if (/[\r\n]/u.test(value)) throw new Error("A trajectory identifier is invalid.") }
function boundedContent(value: string, maximumBytes: number) { if (typeof value !== "string" || utf8Bytes(value) > maximumBytes || /\0/u.test(value)) throw new Error("A trajectory content value is invalid.") }
function utf8Bytes(value: string) { return new TextEncoder().encode(value).byteLength }
function positiveVersion(value: number) { return Number.isSafeInteger(value) && value > 0 }
function validTimestamp(value: string) { return typeof value === "string" && value.length <= 64 && Number.isFinite(Date.parse(value)) }
