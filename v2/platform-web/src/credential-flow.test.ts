import { describe, expect, it } from "vitest"
import {
  clearCredentialAuthorization,
  credentialAuthorizationPollDelay,
  persistCredentialAuthorization,
  restoreCredentialAuthorization,
} from "./credential-flow"

const workspaceId = "50000000-0000-4000-8000-000000000005"
const authorizationId = "70000000-0000-4000-8000-000000000007"

describe("credential device-flow state", () => {
  it("persists only a scoped public transaction reference", () => {
    const storage = new MemoryStorage()
    const reference = { workspaceId, kind: "lark", authorizationId }
    persistCredentialAuthorization(storage, reference)
    expect(restoreCredentialAuthorization(storage, workspaceId)).toEqual(reference)
    clearCredentialAuthorization(storage, workspaceId)
    expect(restoreCredentialAuthorization(storage, workspaceId)).toBeNull()
  })

  it("drops malformed or cross-workspace state", () => {
    const storage = new MemoryStorage()
    storage.setItem(`agentserver.v2.credential-authorization.${workspaceId}`, JSON.stringify({ workspaceId, kind: "lark", authorizationId, token: "must-not-survive" }))
    expect(restoreCredentialAuthorization(storage, workspaceId)).toBeNull()
  })

  it("honors the server polling deadline and stops for terminal state", () => {
    const now = Date.parse("2026-08-11T12:00:00Z")
    const authorization = {
      id: authorizationId, workspaceId, kind: "lark", targetBindingId: "80000000-0000-4000-8000-000000000008",
      status: "pending" as const, userCode: "CODE", verificationUri: "https://example.test/device",
      verificationUriComplete: "https://example.test/device?code=CODE", pollIntervalSeconds: 5,
      nextPollAt: "2026-08-11T12:00:05Z", expiresAt: "2026-08-11T12:05:00Z", version: 1,
    }
    expect(credentialAuthorizationPollDelay(authorization, now)).toBe(5_000)
    expect(credentialAuthorizationPollDelay({ ...authorization, status: "succeeded" }, now)).toBe(-1)
  })
})

class MemoryStorage implements Storage {
  readonly #values = new Map<string, string>()
  get length(): number { return this.#values.size }
  clear(): void { this.#values.clear() }
  getItem(key: string): string | null { return this.#values.get(key) ?? null }
  key(index: number): string | null { return [...this.#values.keys()][index] ?? null }
  removeItem(key: string): void { this.#values.delete(key) }
  setItem(key: string, value: string): void { this.#values.set(key, value) }
}
