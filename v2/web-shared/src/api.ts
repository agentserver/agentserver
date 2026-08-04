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
export type UserSession = PublicComponents["schemas"]["UserSessionState"]
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

  async listSessions(workspaceId: string) {
    return take(await this.#client.GET("/v2/workspaces/{workspaceId}/sessions", { params: { path: { workspaceId } } })).sessions.map((value) => validateSession(value, workspaceId))
  }

  async createSession(workspaceId: string, body: PublicComponents["schemas"]["CreateUserSessionRequest"]) {
    const result = take(await this.#client.POST("/v2/workspaces/{workspaceId}/sessions", { params: { path: { workspaceId } }, body }))
    if (validateSession(result.session, workspaceId).sessionId !== canonicalID("session ID", body.sessionId)) throw new Error("The session creation response escaped its requested scope.")
    return result
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
  exactKeys(value, ["workspaceId", "name", "status", "currentUserRole", "version", "createdAt", "updatedAt"], "workspace")
  canonicalID("workspace ID", value.workspaceId)
  boundedProtocolText(value.name, 256)
  if (!["active", "suspended", "archived"].includes(value.status) || !["owner", "developer", "viewer"].includes(value.currentUserRole) || !positiveVersion(value.version) || !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt)) throw new Error("The workspace response is invalid.")
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

function exactKeys(value: unknown, expected: string[], label: string) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`The ${label} response is invalid.`)
  const actual = Object.keys(value).sort(); const wanted = [...expected].sort()
  if (actual.length !== wanted.length || actual.some((name, index) => name !== wanted[index])) throw new Error(`The ${label} response contains missing or unknown fields.`)
}
function boundedProtocolText(value: string, maximum: number) { if (typeof value !== "string" || !value || value.length > maximum || value.trim() !== value || /[\0\r\n]/u.test(value)) throw new Error("A response text value is invalid.") }
function positiveVersion(value: number) { return Number.isSafeInteger(value) && value > 0 }
function validTimestamp(value: string) { return typeof value === "string" && value.length <= 64 && Number.isFinite(Date.parse(value)) }
