import assert from 'node:assert/strict'
import test from 'node:test'

import { readAuthorizationCallback, validateTokenResponse } from './auth.js'

test('accepts Hydra code callbacks with one bounded scope response', () => {
  const callback = readAuthorizationCallback(
    '?code=opaque-code&scope=openid+executors%3Aread+workspaces%3Aread&state=opaque-state',
  )
  assert.deepEqual(callback, {
    state: 'opaque-state',
    code: 'opaque-code',
    error: '',
    scopes: ['openid', 'executors:read', 'workspaces:read'],
  })
})

test('rejects unknown, duplicate, and malformed callback parameters', () => {
  assert.throws(() => readAuthorizationCallback('?code=x&state=s&future=y'), /invalid/)
  assert.throws(() => readAuthorizationCallback('?code=x&state=s&scope=openid&scope=openid'), /invalid/)
  assert.throws(() => readAuthorizationCallback('?code=x&state=s&scope=openid++workspaces%3Aread'), /scope/)
  assert.throws(() => readAuthorizationCallback('?code=x&error=denied&state=s'), /incomplete/)
})

test('accepts the RFC case-insensitive Hydra bearer token type without accepting persistent authority', () => {
  const requested = ['openid', 'workspaces:read']
  assert.deepEqual(validateTokenResponse({
    access_token: 'opaque-token', token_type: 'bearer', expires_in: 3599, scope: requested.join(' '),
  }, requested), { accessToken: 'opaque-token', scopes: requested })
  assert.throws(() => validateTokenResponse({
    access_token: 'opaque-token', token_type: 'Basic', expires_in: 3599, scope: requested.join(' '),
  }, requested), /authority/)
  assert.throws(() => validateTokenResponse({
    access_token: 'opaque-token', token_type: 'Bearer', expires_in: 3599, scope: requested.join(' '),
    refresh_token: 'persistent-token',
  }, requested), /authority/)
})
