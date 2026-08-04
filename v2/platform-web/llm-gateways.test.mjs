import assert from 'node:assert/strict'
import test from 'node:test'

import {
  GATEWAY_CALLBACK_CHANNEL_NAME,
  buildCreateGatewayRequest,
  createGatewayCallbackChannel,
  validateBeginAuthorization,
  validateGatewayCallbackMessage,
} from './llm-gateways.js'

const gatewayID = '71000000-0000-4000-8000-000000000012'
const cryptoAPI = {
  randomUUID: () => gatewayID,
  getRandomValues: (bytes) => {
    bytes.fill(7)
    return bytes
  },
}

test('Platform creates the minimal refreshable OIDC Gateway configuration', () => {
  const request = buildCreateGatewayRequest({
    name: 'SG Gateway', responsesUrl: 'https://llm.example.com/v1/responses', oidcIssuer: 'https://id.example.com',
    oidcClientId: 'agentserver-workspace', oidcScopes: '', bearerTokenType: 'access_token', defaultModel: 'model-1', makeDefault: true,
  }, cryptoAPI)
  assert.equal(request.gatewayId, gatewayID)
  assert.deepEqual(request.oidcScopes, ['openid', 'offline_access'])
})

test('Platform binds callback delivery to the state in the server authorization URL', () => {
  const state = 's'.repeat(43)
  const result = validateBeginAuthorization({
    gatewayId: gatewayID,
    authorizationUrl: `https://id.example.com/oauth2/auth?state=${state}`,
    expiresAt: '2026-08-04T00:05:00Z',
  }, gatewayID, Date.parse('2026-08-04T00:00:00Z'))
  assert.equal(result.callbackState, state)
  const callback = validateGatewayCallbackMessage({
    type: 'agentserver-v2.llm-gateway-oidc-callback', version: 1,
    state, code: 'code-1', providerError: '', providerErrorDescription: '',
  })
  assert.equal(callback.state, result.callbackState)
  assert.throws(() => validateBeginAuthorization({
    ...result,
    authorizationUrl: `https://id.example.com/oauth2/auth?state=${state}&state=${state}`,
  }, gatewayID))
})

test('Platform callback channel is versioned and tolerates unsupported browsers', () => {
  class FakeBroadcastChannel {
    constructor(name) { this.name = name }
  }
  assert.equal(createGatewayCallbackChannel(FakeBroadcastChannel).name, GATEWAY_CALLBACK_CHANNEL_NAME)
  assert.equal(createGatewayCallbackChannel(undefined), null)
  assert.equal(createGatewayCallbackChannel(class { constructor() { throw new Error('unavailable') } }), null)
})
