import type { LLMGateway } from "@agentserver/v2-web-shared"

export const gatewayCallbackChannelName = "agentserver-v2.llm-gateway-oidc-callback.v1"
const secretPattern = /^[A-Za-z0-9._~-]{43,128}$/u

export interface GatewayCallbackSuccess {
  type: "agentserver-v2.llm-gateway-oidc-callback"
  version: 1
  state: string
  code: string
  providerError: string
  providerErrorDescription: string
}

export interface GatewayCallbackProtocolError {
  type: "agentserver-v2.llm-gateway-oidc-callback"
  version: 1
  state: string
  protocolError: "invalid_callback"
}

export type GatewayCallback = GatewayCallbackSuccess | GatewayCallbackProtocolError

export function gatewayBrowserBinding(): string {
  const value = new Uint8Array(32)
  crypto.getRandomValues(value)
  let binary = ""
  for (const byte of value) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/gu, "-").replace(/\//gu, "_").replace(/=+$/gu, "")
}

export function callbackState(authorizationUrl: string, expiresAt: string): string {
  const url = new URL(authorizationUrl)
  const states = url.searchParams.getAll("state")
  const expiry = Date.parse(expiresAt)
  if (url.protocol !== "https:" || url.hash || states.length !== 1 || !secretPattern.test(states[0] ?? "") || !Number.isFinite(expiry) || expiry <= Date.now() || expiry - Date.now() > 10 * 60 * 1000) {
    throw new Error("The Gateway authorization response is invalid.")
  }
  return states[0] ?? ""
}

export function validateGatewayCallback(value: unknown): GatewayCallback {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("The Gateway callback is invalid.")
  const callback = value as Record<string, unknown>
  if (callback.protocolError !== undefined) {
    const expected = ["type", "version", "state", "protocolError"].sort()
    const actual = Object.keys(callback).sort()
    if (actual.length !== expected.length || actual.some((name, index) => name !== expected[index]) ||
        callback.type !== "agentserver-v2.llm-gateway-oidc-callback" || callback.version !== 1 ||
        typeof callback.state !== "string" || !secretPattern.test(callback.state) || callback.protocolError !== "invalid_callback") {
      throw new Error("The Gateway callback is invalid.")
    }
    return callback as unknown as GatewayCallbackProtocolError
  }
  const expected = ["type", "version", "state", "code", "providerError", "providerErrorDescription"].sort()
  const actual = Object.keys(callback).sort()
  if (actual.length !== expected.length || actual.some((name, index) => name !== expected[index]) ||
      callback.type !== "agentserver-v2.llm-gateway-oidc-callback" || callback.version !== 1 ||
      typeof callback.state !== "string" || !secretPattern.test(callback.state) ||
      typeof callback.code !== "string" || typeof callback.providerError !== "string" || Boolean(callback.code) === Boolean(callback.providerError) ||
      callback.providerError.length > 128 || /[\0\r\n]/u.test(callback.providerError) ||
      typeof callback.providerErrorDescription !== "string" || callback.providerErrorDescription.length > 8192 || /[\0\r\n]/u.test(callback.providerErrorDescription)) {
    throw new Error("The Gateway callback is invalid.")
  }
  return callback as unknown as GatewayCallbackSuccess
}

export function buildGatewayRequest(form: FormData, gatewayId: string) {
  return {
    gatewayId,
    ...buildGatewayConfiguration(form),
  } satisfies Parameters<import("@agentserver/v2-web-shared").ResourceAPI["createGateway"]>[1]
}

export function buildGatewayUpdateRequest(form: FormData, expectedVersion: number) {
  if (!Number.isSafeInteger(expectedVersion) || expectedVersion < 1) throw new Error("Gateway version is invalid.")
  return {
    ...buildGatewayConfiguration(form),
    expectedVersion,
  } satisfies Parameters<import("@agentserver/v2-web-shared").ResourceAPI["updateGateway"]>[2]
}

function buildGatewayConfiguration(form: FormData) {
  const scopes = String(form.get("scopes") ?? "").trim().split(/\s+/u)
  if (scopes.length < 1 || scopes.length > 16 || new Set(scopes).size !== scopes.length || !scopes.includes("openid") || !scopes.includes("offline_access")) {
    throw new Error("OIDC scopes must be unique and include openid and offline_access.")
  }
  const responsesUrl = String(form.get("responsesUrl") ?? "").trim()
  const issuer = String(form.get("issuer") ?? "").trim()
  const parsedResponses = new URL(responsesUrl)
  const parsedIssuer = new URL(issuer)
  if (parsedResponses.protocol !== "https:" || parsedResponses.pathname !== "/v1/responses" || parsedResponses.search || parsedResponses.hash ||
      parsedIssuer.protocol !== "https:" || parsedIssuer.search || parsedIssuer.hash || issuer.endsWith("/")) {
    throw new Error("Gateway URLs must be canonical HTTPS endpoints; Responses URL must end at /v1/responses.")
  }
  const text = (name: string, maximum: number) => {
    const value = String(form.get(name) ?? "").trim()
    if (!value || value.length > maximum || /[\0\r\n]/u.test(value)) throw new Error(`${name} is outside protocol bounds.`)
    return value
  }
  const bearer = String(form.get("bearer") ?? "id_token")
  if (bearer !== "id_token" && bearer !== "access_token") throw new Error("Bearer token type is invalid.")
  return {
    name: text("name", 128),
    responsesUrl,
    oidcIssuer: issuer,
    oidcClientId: text("clientId", 512),
    oidcScopes: scopes,
    bearerTokenType: bearer as "id_token" | "access_token",
    defaultModel: text("model", 256),
    makeDefault: form.get("makeDefault") === "on",
  }
}

export function gatewayTone(gateway: LLMGateway): "neutral" | "success" | "warning" | "danger" {
  if (gateway.status === "disabled") return "danger"
  if (gateway.grantStatus === "active") return "success"
  if (gateway.grantStatus === "reauth_required") return "warning"
  return "neutral"
}
