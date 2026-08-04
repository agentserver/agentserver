import type { AuthorizationConfig, BrowserAuthorizationConfig, TokenResponse } from "./api"
import { OAuthAPI } from "./api"
import { canonicalID, randomSecret } from "./utils"

export type AuthMode = "platform" | "browser"

export interface OAuthCallback {
  state: string
  code: string
  error: string
  errorDescription: string
  scopes: readonly string[]
}

export interface AuthorizationSession {
  token: string
  expiresAt: number
  scopes: readonly string[]
  workspaceId: string
}

interface PKCETransaction {
  version: 2
  mode: AuthMode
  state: string
  verifier: string
  createdAt: number
  redirectUri: string
  clientId: string
  tokenEndpoint: string
  scopes: string[]
  audience: string
  workspaceId: string
  returnPath: string
}

const transactionKey = "agentserver-v2.web-pkce.v2"
const maximumTransactionAge = 10 * 60 * 1000
const maximumAuthorizationSessionAge = 24 * 60 * 60 * 1000
const maximumAuthorizationSessionBytes = 32 * 1024
const pkcePattern = /^[A-Za-z0-9._~-]{43,128}$/u
const authorizationSessionKeys: Record<AuthMode, string> = {
  platform: "agentserver-v2.auth.platform.v1",
  browser: "agentserver-v2.auth.browser.v1",
}

export function authorizationSessionStorageKey(mode: AuthMode): string {
  return authorizationSessionKeys[mode]
}

export function persistAuthorizationSession(storage: Storage, configValue: AuthorizationConfig, mode: AuthMode, session: AuthorizationSession): void {
  const config = validateAuthorizationConfig(configValue, mode)
  const value = {
    version: 1,
    configuration: authorizationConfigurationFingerprint(config, mode),
    accessToken: session.token,
    scopes: [...session.scopes],
    workspaceId: session.workspaceId,
    expiresAt: session.expiresAt,
  }
  validatePersistedAuthorizationSession(value, config, mode, Date.now())
  storage.setItem(authorizationSessionStorageKey(mode), JSON.stringify(value))
}

export function restoreAuthorizationSession(storage: Storage, configValue: AuthorizationConfig, mode: AuthMode, now = Date.now()): AuthorizationSession | null {
  const config = validateAuthorizationConfig(configValue, mode)
  const key = authorizationSessionStorageKey(mode)
  const raw = storage.getItem(key)
  if (raw === null) return null
  if (raw.length > maximumAuthorizationSessionBytes) {
    storage.removeItem(key)
    return null
  }
  try {
    return validatePersistedAuthorizationSession(JSON.parse(raw), config, mode, now)
  } catch {
    storage.removeItem(key)
    return null
  }
}

export function removeAuthorizationSession(storage: Storage, mode: AuthMode): void {
  storage.removeItem(authorizationSessionStorageKey(mode))
}

export function validateAuthorizationConfig(value: AuthorizationConfig, mode: AuthMode): AuthorizationConfig {
  const record = exactRecord(value, mode === "browser" ? [
    "version", "authorizationEndpoint", "tokenEndpoint", "redirectPath", "clientId", "scopes", "audience", "apiOrigin",
  ] : ["version", "authorizationEndpoint", "tokenEndpoint", "redirectPath", "clientId", "scopes", "audience"])
  if (record.version !== 1 || record.redirectPath !== "/") throw new Error("The authorization profile version is unsupported.")
  const expectedClient = mode === "platform" ? "agentserver-platform" : "agentserver-browser"
  if (record.clientId !== expectedClient) throw new Error("The authorization profile belongs to a different application.")
  const authorizationEndpoint = oauthEndpoint(record.authorizationEndpoint, "/oauth2/auth")
  const tokenEndpoint = oauthEndpoint(record.tokenEndpoint, "/oauth2/token")
  if (endpointOrigin(authorizationEndpoint) !== endpointOrigin(tokenEndpoint)) throw new Error("OAuth endpoints use different authorities.")
  protocolText(record.audience, 512)
  if (!Array.isArray(record.scopes) || record.scopes.length < 1 || record.scopes.length > 32) throw new Error("OAuth scopes are outside protocol bounds.")
  const scopes = record.scopes.map((scope) => protocolText(scope, 128))
  if (new Set(scopes).size !== scopes.length || scopes.some((scope) => /\s/u.test(scope)) || !scopes.includes("openid")) {
    throw new Error("OAuth scopes are invalid.")
  }
  if (mode === "browser") exactHTTPSOrigin(String(record.apiOrigin ?? ""), true)
  return value
}

export function readAuthorizationCallback(search: string): OAuthCallback | null {
  const parameters = new URLSearchParams(search.replace(/^\?/u, ""))
  const allowed = new Set(["code", "state", "scope", "error", "error_description", "error_uri", "iss", "session_state"])
  if (![...parameters.keys()].some((name) => allowed.has(name))) return null
  for (const [name, value] of parameters) {
    if (!allowed.has(name) || parameters.getAll(name).length !== 1 || !value || value.length > 8192 || /[\0\r\n]/u.test(value)) {
      throw new Error("The authorization callback is invalid.")
    }
  }
  const state = parameters.get("state") ?? ""
  const code = parameters.get("code") ?? ""
  const error = parameters.get("error") ?? ""
  if (!state || Boolean(code) === Boolean(error)) throw new Error("The authorization callback is incomplete.")
  const rawScopes = parameters.get("scope") ?? ""
  const scopes = rawScopes ? rawScopes.split(" ") : []
  if (scopes.length > 32 || new Set(scopes).size !== scopes.length || scopes.some((scope) => !scope || /\s/u.test(scope))) {
    throw new Error("The authorization callback scopes are invalid.")
  }
  return { state, code, error, errorDescription: parameters.get("error_description") ?? "", scopes }
}

export async function beginAuthorization(configValue: AuthorizationConfig, mode: AuthMode, workspaceId = "", returnPath = "/"): Promise<never> {
  const config = validateAuthorizationConfig(configValue, mode)
  if (mode === "browser") canonicalID("workspace ID", workspaceId)
  if (!returnPath.startsWith("/") || returnPath.startsWith("//") || returnPath.length > 2048) throw new Error("The return route is invalid.")
  const verifier = randomSecret()
  const state = randomSecret()
  const nonce = randomSecret()
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier))
  let binary = ""
  for (const byte of new Uint8Array(digest)) binary += String.fromCharCode(byte)
  const challenge = btoa(binary).replace(/\+/gu, "-").replace(/\//gu, "_").replace(/=+$/gu, "")
  const redirectUri = new URL(config.redirectPath, window.location.origin).href
  const authorizationURL = new URL(config.authorizationEndpoint, window.location.origin)
  const query: Record<string, string> = {
    response_type: "code",
    client_id: config.clientId,
    redirect_uri: redirectUri,
    scope: config.scopes.join(" "),
    audience: config.audience,
    state,
    nonce,
    code_challenge: challenge,
    code_challenge_method: "S256",
  }
  if (mode === "browser") query.resource = `urn:agentserver:workspace:${workspaceId}`
  authorizationURL.search = new URLSearchParams(query).toString()
  const transaction: PKCETransaction = {
    version: 2,
    mode,
    state,
    verifier,
    createdAt: Date.now(),
    redirectUri,
    clientId: config.clientId,
    tokenEndpoint: config.tokenEndpoint,
    scopes: [...config.scopes],
    audience: config.audience,
    workspaceId,
    returnPath,
  }
  sessionStorage.setItem(transactionKey, JSON.stringify(transaction))
  window.location.assign(authorizationURL.href)
  return new Promise<never>(() => undefined)
}

export async function completeAuthorization(configValue: AuthorizationConfig, mode: AuthMode, callback: OAuthCallback): Promise<{
  token: string
  expiresAt: number
  scopes: readonly string[]
  workspaceId: string
  returnPath: string
}> {
  const config = validateAuthorizationConfig(configValue, mode)
  const raw = sessionStorage.getItem(transactionKey)
  sessionStorage.removeItem(transactionKey)
  if (!raw || raw.length > 16 * 1024) throw new Error("The authorization transaction is missing or expired.")
  let transaction: PKCETransaction
  try {
    transaction = JSON.parse(raw) as PKCETransaction
  } catch {
    throw new Error("The authorization transaction is invalid.")
  }
  validateTransaction(transaction, config, mode, callback)
  if (callback.error) throw new Error(`Authorization failed: ${callback.error}${callback.errorDescription ? ` — ${callback.errorDescription}` : ""}`)
  const tokenOrigin = new URL(config.tokenEndpoint, window.location.origin).origin
  const response = await new OAuthAPI(tokenOrigin).exchange({
    grant_type: "authorization_code",
    code: protocolText(callback.code, 8192),
    redirect_uri: transaction.redirectUri,
    client_id: transaction.clientId,
    code_verifier: transaction.verifier,
  })
  const token = validateTokenResponse(response, transaction.scopes)
  return {
    token: token.access_token,
    expiresAt: Date.now() + token.expires_in * 1000,
    scopes: token.scope.split(" "),
    workspaceId: transaction.workspaceId,
    returnPath: transaction.returnPath,
  }
}

function validateTransaction(transaction: PKCETransaction, config: AuthorizationConfig, mode: AuthMode, callback: OAuthCallback) {
  if (!transaction || transaction.version !== 2 || transaction.mode !== mode || transaction.state !== callback.state ||
      !pkcePattern.test(transaction.verifier) || !Number.isSafeInteger(transaction.createdAt) || Date.now() < transaction.createdAt ||
      Date.now() - transaction.createdAt > maximumTransactionAge || transaction.clientId !== config.clientId ||
      transaction.tokenEndpoint !== config.tokenEndpoint || transaction.audience !== config.audience ||
      transaction.redirectUri !== new URL(config.redirectPath, window.location.origin).href ||
      transaction.scopes.join("\0") !== config.scopes.join("\0") || !transaction.returnPath.startsWith("/") || transaction.returnPath.startsWith("//")) {
    throw new Error("The authorization transaction does not match this callback.")
  }
  if (mode === "browser") canonicalID("workspace ID", transaction.workspaceId)
  const requested = new Set(transaction.scopes)
  if (callback.scopes.some((scope) => !requested.has(scope))) throw new Error("The callback exceeds the requested authority.")
}

function validateTokenResponse(value: TokenResponse, requestedScopes: readonly string[]): TokenResponse {
  protocolText(value.access_token, 8192)
  protocolText(value.scope, 4096)
  if (value.token_type.toLowerCase() !== "bearer" || !Number.isSafeInteger(value.expires_in) || value.expires_in < 1 || value.expires_in > 86400 ||
      "refresh_token" in value || "client_secret" in value) {
    throw new Error("The token response authority is invalid.")
  }
  const scopes = value.scope.split(" ")
  const requested = new Set(requestedScopes)
  if (!scopes.includes("openid") || new Set(scopes).size !== scopes.length || scopes.some((scope) => !requested.has(scope))) {
    throw new Error("The token contains permissions outside the requested authority.")
  }
  return value
}

function authorizationConfigurationFingerprint(config: AuthorizationConfig, mode: AuthMode): string {
  return JSON.stringify({
    version: 1,
    mode,
    clientId: config.clientId,
    audience: config.audience,
    authorizationEndpoint: config.authorizationEndpoint,
    tokenEndpoint: config.tokenEndpoint,
    scopes: config.scopes,
  })
}

function validatePersistedAuthorizationSession(value: unknown, config: AuthorizationConfig, mode: AuthMode, now: number): AuthorizationSession {
  const record = exactRecord(value, ["version", "configuration", "accessToken", "scopes", "workspaceId", "expiresAt"])
  if (record.version !== 1 || record.configuration !== authorizationConfigurationFingerprint(config, mode)) {
    throw new Error("The saved authorization session belongs to a different application configuration.")
  }
  const token = protocolText(record.accessToken, 8192)
  if (!Array.isArray(record.scopes) || record.scopes.length < 1 || record.scopes.length > 32) {
    throw new Error("The saved authorization scopes are invalid.")
  }
  const scopes = record.scopes.map((scope: unknown) => protocolText(scope, 128))
  const configuredScopes = new Set(config.scopes)
  if (!scopes.includes("openid") || new Set(scopes).size !== scopes.length || scopes.some((scope: string) => /\s/u.test(scope) || !configuredScopes.has(scope))) {
    throw new Error("The saved authorization scopes exceed the current application configuration.")
  }
  if (!Number.isSafeInteger(record.expiresAt) || record.expiresAt <= now || record.expiresAt - now > maximumAuthorizationSessionAge) {
    throw new Error("The saved authorization session is expired or invalid.")
  }
  if (typeof record.workspaceId !== "string") throw new Error("The saved authorization workspace is invalid.")
  const workspaceId = record.workspaceId
  if (mode === "platform") {
    if (workspaceId !== "") throw new Error("A Platform authorization session cannot be workspace-bound.")
  } else {
    canonicalID("workspace ID", workspaceId)
  }
  return { token, expiresAt: record.expiresAt, scopes, workspaceId }
}

function exactRecord(value: unknown, keys: string[]): Record<string, any> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("The authorization profile is invalid.")
  const actual = Object.keys(value).sort()
  const expected = [...keys].sort()
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new Error("The authorization profile fields are invalid.")
  return value as Record<string, any>
}

function oauthEndpoint(value: unknown, requiredPath: string): string {
  if (value === requiredPath) return value
  const raw = protocolText(value, 2048)
  const parsed = new URL(raw)
  if (parsed.protocol !== "https:" || parsed.username || parsed.password || parsed.pathname !== requiredPath || parsed.search || parsed.hash || parsed.href !== raw) {
    throw new Error("An OAuth endpoint is invalid.")
  }
  return raw
}

function endpointOrigin(endpoint: string): string {
  return endpoint.startsWith("/") ? "" : new URL(endpoint).origin
}

function exactHTTPSOrigin(raw: string, optional = false): string {
  if (optional && raw === "") return ""
  const parsed = new URL(raw)
  if (parsed.protocol !== "https:" || parsed.username || parsed.password || parsed.pathname !== "/" || parsed.search || parsed.hash || parsed.origin !== raw) {
    throw new Error("The API origin is invalid.")
  }
  return parsed.origin
}

function protocolText(value: unknown, maximum: number): string {
  if (typeof value !== "string" || !value || value.length > maximum || value.trim() !== value || /[\0\r\n]/u.test(value)) {
    throw new Error("A protocol value is invalid.")
  }
  return value
}

export function browserConfig(config: AuthorizationConfig): BrowserAuthorizationConfig {
  if (!("apiOrigin" in config)) throw new Error("The Browser API origin is missing.")
  return config
}
