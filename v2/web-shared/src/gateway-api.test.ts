import { afterEach, describe, expect, it, vi } from "vitest"
import { ResourceAPI, type LLMGateway } from "./api"

const workspaceId = "9271bfe5-68a4-484b-a2d3-e9f450a42d0c"
const gatewayId = "3a18bfee-a152-422c-873f-f17dbd086f5a"

function gateway(authType: "oidc" | "api_key"): LLMGateway {
  return {
    authType, baseUrl: "https://gateway.example/v1", apiKeyConfigured: authType === "api_key",
    gatewayId, workspaceId, name: "Test Gateway", responsesUrl: "https://gateway.example/v1/responses",
    oidcIssuer: authType === "oidc" ? "https://gateway.example" : "",
    oidcClientId: authType === "oidc" ? "public-client" : "",
    oidcScopes: authType === "oidc" ? ["openid", "offline_access"] : [],
    bearerTokenType: "access_token", defaultModel: "gpt-5.6-sol", status: "active", default: true,
    version: 1, grantStatus: "", createdAt: "2026-10-08T05:00:00Z", updatedAt: "2026-10-08T05:00:00Z",
  }
}

afterEach(() => vi.unstubAllGlobals())

describe("LLM Gateway API contract", () => {
  it.each(["oidc", "api_key"] as const)("accepts the current %s list response", async (authType) => {
    const state = gateway(authType)
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ gateways: [state] })))
    expect(await new ResourceAPI("https://agent.example", "test-token").listGateways(workspaceId)).toEqual([state])
  })

  it("creates then reloads an API-key gateway without expecting the key in responses", async () => {
    const state = gateway("api_key")
    const fetch = vi.fn(async (request: Request) => {
      expect(new URL(request.url).pathname).toBe(`/v2/workspaces/${workspaceId}/llm-gateways`)
      expect(request.headers.get("Authorization")).toBe("Bearer test-token")
      if (request.method === "POST") {
        expect(await request.json()).toEqual({ gatewayId, authType: "api_key", baseUrl: state.baseUrl, apiKey: "synthetic-test-key", name: state.name, defaultModel: state.defaultModel, makeDefault: true })
        return Response.json({ gateway: state, created: true })
      }
      return Response.json({ gateways: [state] })
    })
    vi.stubGlobal("fetch", fetch)
    const api = new ResourceAPI("https://agent.example", "test-token")
    const result = await api.createGateway(workspaceId, { gatewayId, authType: "api_key", baseUrl: state.baseUrl, apiKey: "synthetic-test-key", name: state.name, defaultModel: state.defaultModel, makeDefault: true })
    expect(result.gateway.apiKeyConfigured).toBe(true)
    expect(result.gateway).not.toHaveProperty("apiKey")
    expect(await api.listGateways(workspaceId)).toEqual([state])
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it.each([
    { apiKey: "must-not-be-returned" },
    { workspaceId: "aaaaaaaa-1111-4444-8888-111111111111" },
    { authType: "unknown" },
    { apiKeyConfigured: "true" },
  ])("still rejects invalid or secret-bearing Gateway responses", async (change) => {
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ gateways: [{ ...gateway("api_key"), ...change }] })))
    await expect(new ResourceAPI("https://agent.example", "test-token").listGateways(workspaceId)).rejects.toThrow()
  })
})
