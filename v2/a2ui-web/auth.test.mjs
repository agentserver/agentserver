import assert from 'node:assert/strict'
import { webcrypto } from 'node:crypto'
import test from 'node:test'

import {
  buildTokenExchangeBody,
  consumeAuthorizationTransaction,
  createAuthorizationTransaction,
  readAuthorizationCallback,
  storeAuthorizationTransaction,
  validateAuthorizationConfig,
  validateTokenResponse,
} from './auth.js'

const config = {
  version: 1,
  authorizationEndpoint: '/oauth2/auth',
  tokenEndpoint: '/oauth2/token',
  redirectPath: '/',
  clientId: 'agentserver-web',
  scopes: ['openid', 'runs:write'],
  audience: 'agentserver-api',
}

test('PKCE transaction binds browser state without storing an access token', async () => {
  const generated = await createAuthorizationTransaction({
    config,
    origin: 'https://127.0.0.1:17444',
    workspaceID: '40000000-0000-4000-8000-000000000004',
    sessionID: '50000000-0000-4000-8000-000000000005',
    cryptoAPI: webcrypto,
    nowMS: 1_800_000_000_000,
  })
  const authorizationURL = new URL(generated.authorizationURL)
  assert.equal(authorizationURL.origin, 'https://127.0.0.1:17444')
  assert.equal(authorizationURL.pathname, '/oauth2/auth')
  assert.equal(authorizationURL.searchParams.get('code_challenge_method'), 'S256')
  assert.equal(authorizationURL.searchParams.get('code_challenge').length, 43)
  assert.equal(authorizationURL.searchParams.get('state'), generated.transaction.state)
  assert.equal('accessToken' in generated.transaction, false)

  const values = new Map()
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  }
  storeAuthorizationTransaction(storage, generated.transaction)
  const callback = readAuthorizationCallback(`?code=opaque-code&state=${encodeURIComponent(generated.transaction.state)}`)
  const consumed = consumeAuthorizationTransaction(storage, config, callback, 1_800_000_001_000)
  assert.equal(consumed.verifier, generated.transaction.verifier)
  assert.equal(values.size, 0)
  assert.equal(buildTokenExchangeBody(config, consumed, callback.code), new URLSearchParams({
    grant_type: 'authorization_code',
    code: 'opaque-code',
    redirect_uri: 'https://127.0.0.1:17444/',
    client_id: 'agentserver-web',
    code_verifier: consumed.verifier,
  }).toString())
})

test('callback and token validation fail closed on replay, mismatch, and persistent token material', async () => {
  assert.throws(() => readAuthorizationCallback('?code=x&state=s&future=y'), /unknown/)
  assert.throws(() => readAuthorizationCallback('?code=x&error=denied&state=s'), /exactly one/)
  assert.throws(() => validateAuthorizationConfig({ ...config, future: true }), /unknown/)

  const generated = await createAuthorizationTransaction({
    config,
    origin: 'https://browser.example',
    workspaceID: '40000000-0000-4000-8000-000000000004',
    sessionID: '50000000-0000-4000-8000-000000000005',
    cryptoAPI: webcrypto,
    nowMS: 1_800_000_000_000,
  })
  const values = new Map()
  const storage = {
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  }
  storeAuthorizationTransaction(storage, generated.transaction)
  assert.throws(
    () => consumeAuthorizationTransaction(storage, config, { state: 'A'.repeat(43), code: 'x', error: '' }, 1_800_000_001_000),
    /mismatched/,
  )
  assert.equal(values.size, 0)
  assert.throws(
    () => consumeAuthorizationTransaction(storage, config, { state: generated.transaction.state, code: 'x', error: '' }, 1_800_000_001_000),
    /no pending/,
  )
  assert.throws(() => validateTokenResponse({
    access_token: 'opaque', token_type: 'Bearer', expires_in: 900,
    scope: 'openid runs:write', refresh_token: 'must-not-persist',
  }, config.scopes), /persistent/)
  assert.deepEqual(validateTokenResponse({
    access_token: 'opaque', token_type: 'Bearer', expires_in: 900, scope: 'runs:write openid',
  }, config.scopes), { accessToken: 'opaque', expiresIn: 900, scopes: ['runs:write', 'openid'] })
})
