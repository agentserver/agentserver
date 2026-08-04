import { readFileSync } from "node:fs"
import { describe, expect, it } from "vitest"
import { enUS, zhCN } from "./i18n"
import { SSEDecoder, appendUserMessage, createConversationState, reduceAGUIEvent } from "./agui"
import {
  authorizationSessionStorageKey,
  persistAuthorizationSession,
  readAuthorizationCallback,
  restoreAuthorizationSession,
  validateAuthorizationConfig,
} from "./oauth"
import type { AuthorizationConfig } from "./api"
import { randomSecret } from "./utils"

describe("product web contracts", () => {
  it("keeps English and Chinese catalog keys identical", () => {
    expect(Object.keys(zhCN).sort()).toEqual(Object.keys(enUS).sort())
    expect(Object.values(zhCN).every((value) => value.length > 0)).toBe(true)
  })

  it("accepts only the closed Platform and Browser authorization profiles", () => {
    const common = {
      version: 1 as const,
      authorizationEndpoint: "https://auth.example/oauth2/auth",
      tokenEndpoint: "https://auth.example/oauth2/token",
      redirectPath: "/" as const,
      scopes: ["openid", "workspaces:read"],
      audience: "agentserver-platform-api",
    }
    expect(validateAuthorizationConfig({ ...common, clientId: "agentserver-platform" }, "platform")).toMatchObject({ clientId: "agentserver-platform" })
    const browser = { ...common, clientId: "agentserver-browser", scopes: ["openid", "sessions:read"], audience: "agentserver-browser-api", apiOrigin: "https://browser-api.example" }
    expect(validateAuthorizationConfig(browser, "browser")).toMatchObject({ apiOrigin: "https://browser-api.example" })
    expect(() => validateAuthorizationConfig({ ...browser, unexpected: true } as unknown as AuthorizationConfig, "browser")).toThrow(/fields/u)
    expect(() => validateAuthorizationConfig({ ...common, clientId: "agentserver-browser" }, "platform")).toThrow(/different application/u)
  })

  it("rejects ambiguous OAuth callbacks", () => {
    expect(readAuthorizationCallback("?code=code&state=state&scope=openid")).toMatchObject({ code: "code", state: "state", scopes: ["openid"] })
    expect(() => readAuthorizationCallback("?code=a&code=b&state=s")).toThrow(/invalid/u)
    expect(() => readAuthorizationCallback("?code=a&state=s&unknown=x")).toThrow(/invalid/u)
  })

  it("persists isolated and configuration-bound authorization sessions", () => {
    const storage = new MemoryStorage()
    const now = Date.now()
    const platform = platformAuthorizationConfig()
    persistAuthorizationSession(storage, platform, "platform", { token: "platform-token", expiresAt: now + 60_000, scopes: ["openid", "workspaces:read"], workspaceId: "" })
    expect(authorizationSessionStorageKey("platform")).not.toBe(authorizationSessionStorageKey("browser"))
    expect(restoreAuthorizationSession(storage, platform, "platform", now)).toEqual({ token: "platform-token", expiresAt: now + 60_000, scopes: ["openid", "workspaces:read"], workspaceId: "" })

    const changed = { ...platform, audience: "replacement-platform-api" }
    expect(restoreAuthorizationSession(storage, changed, "platform", now)).toBeNull()
    expect(storage.getItem(authorizationSessionStorageKey("platform"))).toBeNull()
    persistAuthorizationSession(storage, platform, "platform", { token: "platform-token", expiresAt: now + 60_000, scopes: ["openid"], workspaceId: "" })
    expect(restoreAuthorizationSession(storage, { ...platform, scopes: [...platform.scopes, "llm-gateways:update"] }, "platform", now)).toBeNull()
  })

  it("rejects expired, over-scoped, and incorrectly bound saved sessions", () => {
    const storage = new MemoryStorage()
    const now = Date.now()
    const platform = platformAuthorizationConfig()
    expect(() => persistAuthorizationSession(storage, platform, "platform", { token: "token", expiresAt: now + 60_000, scopes: ["openid", "sessions:write"], workspaceId: "" })).toThrow(/scopes/u)
    expect(() => persistAuthorizationSession(storage, platform, "platform", { token: "token", expiresAt: now + 60_000, scopes: ["openid"], workspaceId: "9271bfe5-68a4-484b-a2d3-e9f450a42d0c" })).toThrow(/workspace-bound/u)
    persistAuthorizationSession(storage, platform, "platform", { token: "token", expiresAt: now + 1_000, scopes: ["openid"], workspaceId: "" })
    expect(restoreAuthorizationSession(storage, platform, "platform", now + 1_001)).toBeNull()
  })

  it("generates OAuth correlation secrets accepted by the Core boundary", () => {
    const values = Array.from({ length: 16 }, () => randomSecret())
    expect(new Set(values).size).toBe(values.length)
    for (const value of values) {
      expect(value.length).toBeGreaterThanOrEqual(43)
      expect(value.length).toBeLessThanOrEqual(128)
      expect(value).toMatch(/^[A-Za-z0-9._~-]+$/u)
    }
  })

  it("decodes bounded SSE and reduces a text lifecycle", () => {
    const decoder = new SSEDecoder()
    const events = decoder.push('data: {"type":"RUN_STARTED","runId":"9271bfe5-68a4-484b-a2d3-e9f450a42d0c"}\n\ndata: {"type":"TEXT_MESSAGE_START","messageId":"m1","role":"assistant"}\n\ndata: {"type":"TEXT_MESSAGE_CONTENT","messageId":"m1","delta":"hello"}\n\n')
    let state = appendUserMessage(createConversationState(), "u1", "hi")
    for (const event of events) state = reduceAGUIEvent(state, event)
    expect(state.status).toBe("running")
    expect(state.messages.at(-1)?.text).toBe("hello")
  })

  it("keeps feature code behind generated OpenAPI transports", () => {
    for (const source of ["../../platform-web/src/platform-app.tsx", "../../a2ui-web/src/browser-app.tsx"]) {
      const contents = readFileSync(new URL(source, import.meta.url), "utf8")
      expect(contents).not.toMatch(/\bfetch\s*\(/u)
      expect(contents).not.toMatch(/XMLHttpRequest/u)
      expect(contents).not.toMatch(/["'`]\/v2\//u)
    }
  })
})

function platformAuthorizationConfig(): AuthorizationConfig {
  return {
    version: 1,
    authorizationEndpoint: "https://auth.example/oauth2/auth",
    tokenEndpoint: "https://auth.example/oauth2/token",
    redirectPath: "/",
    clientId: "agentserver-platform",
    scopes: ["openid", "workspaces:read"],
    audience: "agentserver-platform-api",
  }
}

class MemoryStorage implements Storage {
  readonly #values = new Map<string, string>()
  get length(): number { return this.#values.size }
  clear(): void { this.#values.clear() }
  getItem(key: string): string | null { return this.#values.get(key) ?? null }
  key(index: number): string | null { return [...this.#values.keys()][index] ?? null }
  removeItem(key: string): void { this.#values.delete(key) }
  setItem(key: string, value: string): void { this.#values.set(key, value) }
}
