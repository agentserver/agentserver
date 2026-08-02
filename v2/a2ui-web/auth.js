const transactionStorageKey = 'agentserver-v2.oauth-pkce.v1'
const maximumTransactionAgeMS = 10 * 60 * 1000

export function validateAuthorizationConfig(value) {
  requireExactObject(value, [
    'version', 'authorizationEndpoint', 'tokenEndpoint', 'redirectPath', 'clientId', 'scopes', 'audience', 'apiOrigin',
  ], 'authorization config')
  if (value.version !== 1 || value.authorizationEndpoint !== '/oauth2/auth' ||
      value.tokenEndpoint !== '/oauth2/token' || value.redirectPath !== '/') {
    throw new Error('authorization config contains unsupported endpoints or version')
  }
  validateProtocolText('OAuth client ID', value.clientId, 512)
  validateProtocolText('OAuth audience', value.audience, 512)
  const apiOrigin = validateOptionalHTTPSOrigin(value.apiOrigin)
  if (!Array.isArray(value.scopes) || value.scopes.length < 1 || value.scopes.length > 16) {
    throw new Error('authorization config scopes are missing or outside bounds')
  }
  const scopes = new Set()
  for (const scope of value.scopes) {
    validateProtocolText('OAuth scope', scope, 128)
    if (/\s/u.test(scope) || scopes.has(scope)) throw new Error('authorization config scopes must be unique tokens')
    scopes.add(scope)
  }
  return Object.freeze({
    version: 1,
    authorizationEndpoint: value.authorizationEndpoint,
    tokenEndpoint: value.tokenEndpoint,
    redirectPath: value.redirectPath,
    clientId: value.clientId,
    scopes: Object.freeze([...value.scopes]),
    audience: value.audience,
    apiOrigin,
  })
}

export async function createAuthorizationTransaction({ config, origin, workspaceID, sessionID, cryptoAPI, nowMS = Date.now() }) {
  config = validateAuthorizationConfig(config)
  const canonicalOrigin = validateHTTPSOrigin(origin)
  if (!cryptoAPI || typeof cryptoAPI.getRandomValues !== 'function' || !cryptoAPI.subtle ||
      typeof cryptoAPI.subtle.digest !== 'function' || !Number.isSafeInteger(nowMS) || nowMS < 1) {
    throw new Error('browser cryptography and a valid clock are required for PKCE')
  }
  const verifier = randomBase64URL(cryptoAPI)
  const state = randomBase64URL(cryptoAPI)
  const nonce = randomBase64URL(cryptoAPI)
  const digest = await cryptoAPI.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
  const challenge = encodeBase64URL(new Uint8Array(digest))
  const redirectURI = new URL(config.redirectPath, canonicalOrigin).href
  const authorizationURL = new URL(config.authorizationEndpoint, canonicalOrigin)
  authorizationURL.search = new URLSearchParams({
    response_type: 'code',
    client_id: config.clientId,
    redirect_uri: redirectURI,
    scope: config.scopes.join(' '),
    audience: config.audience,
    state,
    nonce,
    code_challenge: challenge,
    code_challenge_method: 'S256',
  }).toString()
  return {
    authorizationURL: authorizationURL.href,
    transaction: {
      version: 1,
      state,
      verifier,
      nonce,
      workspaceID,
      sessionID,
      createdAtMS: nowMS,
      clientID: config.clientId,
      tokenEndpoint: config.tokenEndpoint,
      redirectURI,
      scopes: [...config.scopes],
      audience: config.audience,
      apiOrigin: config.apiOrigin,
    },
  }
}

export function storeAuthorizationTransaction(storage, transaction) {
  if (!storage || typeof storage.setItem !== 'function') throw new Error('sessionStorage is unavailable for PKCE')
  validateStoredTransaction(transaction)
  storage.setItem(transactionStorageKey, JSON.stringify(transaction))
}

export function consumeAuthorizationTransaction(storage, config, callback, nowMS = Date.now()) {
  if (!storage || typeof storage.getItem !== 'function' || typeof storage.removeItem !== 'function') {
    throw new Error('sessionStorage is unavailable for PKCE')
  }
  const raw = storage.getItem(transactionStorageKey)
  storage.removeItem(transactionStorageKey)
  if (typeof raw !== 'string' || raw.length < 1 || raw.length > 16 * 1024) {
    throw new Error('authorization callback has no pending PKCE transaction')
  }
  let transaction
  try {
    transaction = JSON.parse(raw)
  } catch {
    throw new Error('pending PKCE transaction is invalid')
  }
  validateStoredTransaction(transaction)
  config = validateAuthorizationConfig(config)
  if (!callback || callback.state !== transaction.state || !Number.isSafeInteger(nowMS) ||
      nowMS < transaction.createdAtMS || nowMS - transaction.createdAtMS > maximumTransactionAgeMS) {
    throw new Error('authorization callback state is missing, mismatched, or expired')
  }
  if (transaction.clientID !== config.clientId || transaction.tokenEndpoint !== config.tokenEndpoint ||
      transaction.audience !== config.audience || transaction.apiOrigin !== config.apiOrigin ||
      !sameTextArray(transaction.scopes, config.scopes)) {
    throw new Error('authorization configuration changed during the PKCE transaction')
  }
  return transaction
}

export function readAuthorizationCallback(rawSearch) {
  const parameters = new URLSearchParams(String(rawSearch || '').replace(/^\?/, ''))
  const callbackNames = new Set(['code', 'state', 'error', 'error_description', 'error_uri', 'iss', 'session_state'])
  const hasCallback = [...parameters.keys()].some((name) => callbackNames.has(name))
  if (!hasCallback) return null
  for (const [name, value] of parameters) {
    if (!callbackNames.has(name) || parameters.getAll(name).length !== 1 || value === '' || value.length > 8192 || /[\0\r\n]/u.test(value)) {
      throw new Error('authorization callback contains unknown, duplicate, empty, or oversized parameters')
    }
  }
  const state = parameters.get('state') || ''
  const code = parameters.get('code') || ''
  const providerError = parameters.get('error') || ''
  if (!state || Boolean(code) === Boolean(providerError)) {
    throw new Error('authorization callback must contain state and exactly one code or error')
  }
  return {
    state,
    code,
    error: providerError,
    errorDescription: parameters.get('error_description') || '',
  }
}

export function buildTokenExchangeBody(config, transaction, code) {
  config = validateAuthorizationConfig(config)
  validateStoredTransaction(transaction)
  validateProtocolText('authorization code', code, 8192)
  if (transaction.clientID !== config.clientId || transaction.tokenEndpoint !== config.tokenEndpoint) {
    throw new Error('token exchange does not match its authorization transaction')
  }
  return new URLSearchParams({
    grant_type: 'authorization_code',
    code,
    redirect_uri: transaction.redirectURI,
    client_id: transaction.clientID,
    code_verifier: transaction.verifier,
  }).toString()
}

export function validateTokenResponse(value, expectedScopes) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('token response must be an object')
  validateProtocolText('access token', value.access_token, 8192)
  if (value.token_type !== 'Bearer' || !Number.isSafeInteger(value.expires_in) || value.expires_in < 1 || value.expires_in > 24 * 60 * 60) {
    throw new Error('token response type or lifetime is invalid')
  }
  validateProtocolText('token scope', value.scope, 2048)
  const scopes = value.scope.split(' ')
  if (!sameTextSet(scopes, expectedScopes)) throw new Error('token response scope differs from the requested authority')
  if ('refresh_token' in value || 'client_secret' in value) throw new Error('reference browser does not accept persistent or client-secret token material')
  return { accessToken: value.access_token, expiresIn: value.expires_in, scopes }
}

function validateStoredTransaction(value) {
  requireExactObject(value, [
    'version', 'state', 'verifier', 'nonce', 'workspaceID', 'sessionID', 'createdAtMS',
    'clientID', 'tokenEndpoint', 'redirectURI', 'scopes', 'audience', 'apiOrigin',
  ], 'PKCE transaction')
  if (value.version !== 1 || !validPKCESecret(value.state) || !validPKCESecret(value.verifier) || !validPKCESecret(value.nonce) ||
      !Number.isSafeInteger(value.createdAtMS) || value.createdAtMS < 1 || !Array.isArray(value.scopes)) {
    throw new Error('PKCE transaction fields are invalid')
  }
  for (const [label, text, maximum] of [
    ['workspace ID', value.workspaceID, 128], ['session ID', value.sessionID, 128], ['client ID', value.clientID, 512],
    ['token endpoint', value.tokenEndpoint, 2048], ['redirect URI', value.redirectURI, 4096], ['audience', value.audience, 512],
  ]) validateProtocolText(label, text, maximum)
  if (value.tokenEndpoint !== '/oauth2/token' || value.scopes.length < 1 || !sameTextSet(value.scopes, value.scopes)) {
    throw new Error('PKCE transaction endpoint or scopes are invalid')
  }
  validateOptionalHTTPSOrigin(value.apiOrigin)
  return value
}

function validateOptionalHTTPSOrigin(raw) {
  if (raw === '') return ''
  if (typeof raw !== 'string') throw new Error('browser API origin must be an exact HTTPS origin')
  const parsed = new URL(raw)
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.pathname !== '/' || parsed.search || parsed.hash || parsed.origin !== raw) {
    throw new Error('browser API origin must be an exact HTTPS origin')
  }
  return parsed.origin
}

function validateHTTPSOrigin(raw) {
  const parsed = new URL(raw)
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.pathname !== '/' || parsed.search || parsed.hash) {
    throw new Error('browser origin must be an exact HTTPS origin')
  }
  return parsed.origin
}

function randomBase64URL(cryptoAPI) {
  const value = new Uint8Array(32)
  cryptoAPI.getRandomValues(value)
  return encodeBase64URL(value)
}

function encodeBase64URL(bytes) {
  let binary = ''
  for (const value of bytes) binary += String.fromCharCode(value)
  return btoa(binary).replace(/\+/gu, '-').replace(/\//gu, '_').replace(/=+$/gu, '')
}

function validPKCESecret(value) {
  return typeof value === 'string' && value.length >= 43 && value.length <= 128 && /^[A-Za-z0-9._~-]+$/u.test(value)
}

function validateProtocolText(label, value, maximum) {
  if (typeof value !== 'string' || value.length < 1 || value.length > maximum || value.trim() !== value || /[\0\r\n]/u.test(value)) {
    throw new Error(`${label} is empty or outside protocol bounds`)
  }
}

function requireExactObject(value, names, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`)
  const actual = Object.keys(value).sort()
  const expected = [...names].sort()
  if (!sameTextArray(actual, expected)) throw new Error(`${label} contains missing or unknown fields`)
}

function sameTextArray(left, right) {
  return Array.isArray(left) && Array.isArray(right) && left.length === right.length && left.every((value, index) => value === right[index])
}

function sameTextSet(left, right) {
  if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) return false
  const leftSet = new Set(left)
  const rightSet = new Set(right)
  return leftSet.size === left.length && rightSet.size === right.length && [...leftSet].every((value) => rightSet.has(value))
}
