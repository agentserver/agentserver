import test from 'node:test'
import assert from 'node:assert/strict'

import {
  GATEWAY_CALLBACK_CHANNEL_NAME,
  buildCreateGatewayRequest,
  createBrowserBinding,
  createGatewayCallbackChannel,
  validateBeginAuthorization,
  validateDisableGateway,
  validateGatewayCallbackMessage,
  validateGatewayList,
  workspaceLLMGatewayActionPath,
} from './llm-gateways.js'

const workspaceID = '71000000-0000-4000-8000-000000000011'
const gatewayID = '71000000-0000-4000-8000-000000000012'
const cryptoAPI = {
  randomUUID: () => gatewayID,
  getRandomValues: (bytes) => {
    bytes.fill(7)
    return bytes
  },
}

test('workspace Gateway form creates a public-client PKCE configuration', () => {
  const request = buildCreateGatewayRequest({
    name: 'SG Gateway', responsesUrl: 'https://llm.example.com/v1/responses', oidcIssuer: 'https://id.example.com',
    oidcClientId: 'agentserver-workspace', oidcScopes: '', bearerTokenType: 'id_token', defaultModel: 'model-1', makeDefault: true,
  }, cryptoAPI)
  assert.equal(request.gatewayId, gatewayID)
  assert.deepEqual(request.oidcScopes, ['openid', 'offline_access'])
  assert.equal(request.makeDefault, true)
  assert.throws(() => buildCreateGatewayRequest({ ...request, responsesUrl: 'http://localhost/v1/responses' }, cryptoAPI))
})

test('workspace Gateway projections and action paths remain scope-bound', () => {
  const gateways = validateGatewayList({ gateways: [{
    gatewayId: gatewayID, workspaceId: workspaceID, name: 'SG Gateway', responsesUrl: 'https://llm.example.com/v1/responses',
    oidcIssuer: 'https://id.example.com', oidcClientId: 'agentserver-workspace', oidcScopes: ['openid', 'offline_access'],
    bearerTokenType: 'id_token', defaultModel: 'model-1', status: 'active', default: true, version: 1,
    grantStatus: '', createdAt: '2026-08-02T00:00:00Z', updatedAt: '2026-08-02T00:00:00Z',
  }] }, workspaceID)
  assert.equal(gateways[0].gatewayId, gatewayID)
  assert.equal(workspaceLLMGatewayActionPath(workspaceID, gatewayID, 'authorize'), `/v2/workspaces/${workspaceID}/llm-gateways/${gatewayID}:authorize`)
  assert.equal(workspaceLLMGatewayActionPath(workspaceID, gatewayID, 'disable'), `/v2/workspaces/${workspaceID}/llm-gateways/${gatewayID}:disable`)
  assert.deepEqual(validateDisableGateway({ gatewayId: gatewayID, status: 'disabled', version: 2, changed: true }, gatewayID), {
    gatewayId: gatewayID, status: 'disabled', version: 2, changed: true,
  })
  assert.throws(() => validateGatewayList({ gateways: [{ ...gateways[0], workspaceId: gatewayID }] }, workspaceID))
})

test('browser binding and callback use a bounded in-memory correlation protocol', () => {
  const binding = createBrowserBinding(cryptoAPI)
  assert.match(binding, /^[A-Za-z0-9_-]{43}$/u)
  const state = 's'.repeat(43)
  const result = validateBeginAuthorization({
    gatewayId: gatewayID,
    authorizationUrl: `https://id.example.com/oauth2/auth?state=${state}`,
    expiresAt: '2026-08-02T00:05:00Z',
  }, gatewayID, Date.parse('2026-08-02T00:00:00Z'))
  assert.equal(result.gatewayId, gatewayID)
  assert.equal(result.callbackState, state)
  const callback = validateGatewayCallbackMessage({
    type: 'agentserver-v2.llm-gateway-oidc-callback', version: 1,
    state, code: 'code-1', providerError: '', providerErrorDescription: '',
  })
  assert.equal(callback.code, 'code-1')
  assert.throws(() => validateGatewayCallbackMessage({ ...callback, type: 'wrong', version: 1 }))
  assert.throws(() => validateBeginAuthorization({ ...result, authorizationUrl: `https://id.example.com/oauth2/auth?state=${state}&state=${state}` }, gatewayID))
})

test('callback channel has one versioned same-origin protocol name and fails closed when unavailable', () => {
  class FakeBroadcastChannel {
    constructor(name) { this.name = name }
  }
  assert.equal(createGatewayCallbackChannel(FakeBroadcastChannel).name, GATEWAY_CALLBACK_CHANNEL_NAME)
  assert.equal(createGatewayCallbackChannel(undefined), null)
  assert.equal(createGatewayCallbackChannel(class { constructor() { throw new Error('unavailable') } }), null)
})
