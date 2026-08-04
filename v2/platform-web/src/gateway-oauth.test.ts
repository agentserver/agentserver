import { describe, expect, it } from "vitest"
import { buildGatewayRequest, buildGatewayUpdateRequest, callbackState, validateGatewayCallback } from "./gateway-oauth"

describe("Gateway OAuth boundary", () => {
  it("validates callback state and the exact callback envelope", () => {
    const state = "a".repeat(43)
    expect(callbackState(`https://provider.example/oauth2/auth?state=${state}`, new Date(Date.now() + 60_000).toISOString())).toBe(state)
    expect(validateGatewayCallback({ type: "agentserver-v2.llm-gateway-oidc-callback", version: 1, state, code: "code", providerError: "", providerErrorDescription: "" })).toMatchObject({ code: "code" })
    expect(validateGatewayCallback({ type: "agentserver-v2.llm-gateway-oidc-callback", version: 1, state, protocolError: "invalid_callback" })).toMatchObject({ state, protocolError: "invalid_callback" })
    expect(() => validateGatewayCallback({ type: "agentserver-v2.llm-gateway-oidc-callback", version: 1, state: "short", protocolError: "invalid_callback" })).toThrow(/invalid/u)
    expect(() => validateGatewayCallback({ type: "agentserver-v2.llm-gateway-oidc-callback", version: 1, state, code: "code", providerError: "error", providerErrorDescription: "" })).toThrow(/invalid/u)
  })

  it("builds the reviewed public PKCE gateway profile", () => {
    const form = new FormData()
    for (const [name, value] of Object.entries({ name: "Example", model: "gpt-5", responsesUrl: "https://gateway.example/v1/responses", issuer: "https://gateway.example", clientId: "client", scopes: "openid offline_access project:inference", bearer: "id_token" })) form.set(name, value)
    form.set("makeDefault", "on")
    expect(buildGatewayRequest(form, "9271bfe5-68a4-484b-a2d3-e9f450a42d0c")).toMatchObject({ oidcScopes: ["openid", "offline_access", "project:inference"], makeDefault: true })
    expect(buildGatewayUpdateRequest(form, 7)).toMatchObject({ expectedVersion: 7, defaultModel: "gpt-5", bearerTokenType: "id_token" })
  })

  it("bounds provider errors before displaying or forwarding them", () => {
    const state = "a".repeat(43)
    expect(validateGatewayCallback({ type: "agentserver-v2.llm-gateway-oidc-callback", version: 1, state, code: "", providerError: "invalid_scope", providerErrorDescription: "openid is not allowed" })).toMatchObject({ providerError: "invalid_scope" })
    expect(() => validateGatewayCallback({ type: "agentserver-v2.llm-gateway-oidc-callback", version: 1, state, code: "", providerError: "x".repeat(129), providerErrorDescription: "" })).toThrow(/invalid/u)
  })
})
