import { readFileSync } from "node:fs"
import { describe, expect, it } from "vitest"
import { enUS, zhCN } from "./i18n"
import { SSEDecoder, appendUserMessage, conversationFromTranscript, createConversationState, reduceAGUIEvent } from "./agui"
import {
  authorizationSessionStorageKey,
  persistAuthorizationSession,
  readAuthorizationCallback,
  restoreAuthorizationSession,
  validateAuthorizationConfig,
} from "./oauth"
import { validateSessionTrajectory, type AuthorizationConfig, type SessionTrajectory } from "./api"
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

  it("restores durable user and assistant messages from a session transcript", () => {
    const runId = "9271bfe5-68a4-484b-a2d3-e9f450a42d0c"
    const state = conversationFromTranscript({
      workspaceId: "50000000-0000-4000-8000-000000000005",
      sessionId: "60000000-0000-4000-8000-000000000006",
      messages: [
        { messageId: "user-1", runId, role: "user", content: "你好", complete: true, createdAt: "2026-08-05T01:00:00Z" },
        { messageId: "assistant-1", runId, role: "assistant", content: "你好！", complete: true, createdAt: "2026-08-05T01:00:01Z" },
      ],
      truncated: false,
    })
    expect(state.status).toBe("idle")
    expect(state.messages.map((message) => [message.role, message.text, message.complete])).toEqual([
      ["user", "你好", true], ["assistant", "你好！", true],
    ])
    expect(new Set(state.messages.map((message) => message.id)).size).toBe(2)
  })

  it("accepts only scoped, bounded, closed-world session trajectories", () => {
    const workspaceId = "50000000-0000-4000-8000-000000000005"
    const sessionId = "60000000-0000-4000-8000-000000000006"
    const runId = "9271bfe5-68a4-484b-a2d3-e9f450a42d0c"
    const trajectory: SessionTrajectory = {
      schemaVersion: 1, workspaceId, sessionId, activeRunId: runId,
      records: [{
        id: `operation:${runId}`, parentId: `execution:${runId}`, kind: "operation", status: "failed",
        title: "run command", summary: "The dispatched operation has no confirmed terminal result", runId,
        startedAt: "2026-08-15T01:00:00Z", completedAt: "2026-08-15T01:00:01Z", durationMillis: 1000,
        input: "lark-cli skills read lark-doc", output: "", outputTruncated: true,
        details: [{ name: "target", value: "tae" }],
        failure: {
          code: "output_incomplete", category: "output_incomplete", message: "TAE session ended before a terminal response",
          component: "executor-gateway", phase: "operation", retryable: true,
        },
      }],
      nextBefore: "v1.cursor", hasMore: true, truncated: false, readAt: "2026-08-15T01:00:02Z",
    }
    expect(validateSessionTrajectory(trajectory, workspaceId, sessionId)).toBe(trajectory)
    expect(() => validateSessionTrajectory({ ...trajectory, workspaceId: sessionId }, workspaceId, sessionId)).toThrow(/scope/u)
    expect(() => validateSessionTrajectory({ ...trajectory, hasMore: false }, workspaceId, sessionId)).toThrow(/pagination/u)
    expect(() => validateSessionTrajectory({ ...trajectory, nextBefore: "v1.\r\ncursor" }, workspaceId, sessionId)).toThrow(/identifier/u)
    expect(() => validateSessionTrajectory({ ...trajectory, records: [{ ...trajectory.records[0]!, id: "operation:\ninvalid" }] }, workspaceId, sessionId)).toThrow(/identifier/u)
    expect(() => validateSessionTrajectory({ ...trajectory, future: true } as unknown as SessionTrajectory, workspaceId, sessionId)).toThrow(/unknown fields/u)
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
