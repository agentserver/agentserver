import { readAuthorizationCallback } from './auth.js'

const transactionKey = 'agentserver-v2.platform-pkce.v1'
const maximumTransactionAgeMS = 10 * 60 * 1000

const loginButton = requiredElement('login-button')
const sessionButton = requiredElement('session-button')
const statusTitle = requiredElement('status-title')
const statusCopy = requiredElement('status-copy')
const scopeList = requiredElement('scope-list')
const errorElement = requiredElement('error')

let config = null
let accessToken = ''

loginButton.addEventListener('click', beginAuthorization)
sessionButton.addEventListener('click', () => {
  if (!accessToken) {
    void beginAuthorization()
    return
  }
  accessToken = ''
  renderSignedOut()
})

void initialize()

async function initialize() {
  if (window.location.hash) history.replaceState(null, '', `${window.location.pathname}${window.location.search}`)
  try {
    config = validateConfig(await fetchJSON('/auth/config', 32 * 1024))
    const callback = readAuthorizationCallback(window.location.search)
    if (!callback) return
    history.replaceState(null, '', window.location.pathname)
    await completeAuthorization(callback)
  } catch (error) {
    showError(error)
  }
}

async function beginAuthorization() {
  if (!config || loginButton.disabled) return
  loginButton.disabled = true
  clearError()
  try {
    const verifier = randomBase64URL()
    const state = randomBase64URL()
    const nonce = randomBase64URL()
    const digest = await window.crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
    const redirectURI = new URL(config.redirectPath, window.location.origin).href
    const authorizationURL = new URL(config.authorizationEndpoint, window.location.origin)
    authorizationURL.search = new URLSearchParams({
      response_type: 'code', client_id: config.clientId, redirect_uri: redirectURI,
      scope: config.scopes.join(' '), audience: config.audience, state, nonce,
      code_challenge: encodeBase64URL(new Uint8Array(digest)), code_challenge_method: 'S256',
    }).toString()
    const transaction = { version: 1, state, verifier, createdAtMS: Date.now(), redirectURI, clientId: config.clientId }
    window.sessionStorage.setItem(transactionKey, JSON.stringify(transaction))
    window.location.assign(authorizationURL.href)
  } catch (error) {
    loginButton.disabled = false
    showError(error)
  }
}

async function completeAuthorization(callback) {
  const raw = window.sessionStorage.getItem(transactionKey)
  window.sessionStorage.removeItem(transactionKey)
  if (!raw || raw.length > 16 * 1024) throw new Error('The authorization transaction is missing or expired.')
  const transaction = JSON.parse(raw)
  if (!transaction || transaction.version !== 1 || transaction.state !== callback.state ||
      transaction.clientId !== config.clientId || !validPKCESecret(transaction.verifier) ||
      !Number.isSafeInteger(transaction.createdAtMS) || Date.now() < transaction.createdAtMS ||
      Date.now() - transaction.createdAtMS > maximumTransactionAgeMS) {
    throw new Error('The authorization transaction does not match this callback.')
  }
  const requestedScopes = new Set(config.scopes)
  if (callback.scopes.some((scope) => !requestedScopes.has(scope))) {
    throw new Error('The authorization callback scope exceeds the requested Platform authority.')
  }
  if (callback.error) throw new Error(`Authorization failed: ${callback.error}`)
  const tokenURL = new URL(config.tokenEndpoint, window.location.origin)
  const response = await fetch(tokenURL.href, {
    method: 'POST', mode: tokenURL.origin === window.location.origin ? 'same-origin' : 'cors',
    cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer',
    headers: { Accept: 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'authorization_code', code: callback.code, redirect_uri: transaction.redirectURI,
      client_id: transaction.clientId, code_verifier: transaction.verifier,
    }).toString(),
  })
  if (!response.ok) throw new Error(`Token exchange failed with HTTP ${response.status}.`)
  const token = validateToken(await boundedJSON(response, 128 * 1024), config.scopes)
  accessToken = token.accessToken
  renderSignedIn(token.scopes)
}

function validateConfig(value) {
  requireExactKeys(value, ['version', 'authorizationEndpoint', 'tokenEndpoint', 'redirectPath', 'clientId', 'scopes', 'audience'])
  if (value.version !== 1 || value.redirectPath !== '/') {
    throw new Error('Unsupported Platform authorization configuration.')
  }
  const authorizationEndpoint = validateOAuthEndpoint('authorization', value.authorizationEndpoint, '/oauth2/auth')
  const tokenEndpoint = validateOAuthEndpoint('token', value.tokenEndpoint, '/oauth2/token')
  if (oauthEndpointAuthority(authorizationEndpoint) !== oauthEndpointAuthority(tokenEndpoint)) {
    throw new Error('Platform authorization endpoints use different authorities.')
  }
  validateText(value.clientId, 512)
  validateText(value.audience, 512)
  if (!Array.isArray(value.scopes) || value.scopes.length < 1 || value.scopes.length > 32 || new Set(value.scopes).size !== value.scopes.length) {
    throw new Error('Platform authorization scopes are invalid.')
  }
  value.scopes.forEach((scope) => { validateText(scope, 128); if (/\s/u.test(scope)) throw new Error('Platform scope is invalid.') })
  return Object.freeze({ ...value, authorizationEndpoint, tokenEndpoint, scopes: Object.freeze([...value.scopes]) })
}

function validateOAuthEndpoint(name, raw, requiredPath) {
  if (raw === requiredPath) return raw
  if (typeof raw !== 'string') throw new Error(`Platform OAuth ${name} endpoint is invalid.`)
  let parsed
  try { parsed = new URL(raw) } catch { throw new Error(`Platform OAuth ${name} endpoint is invalid.`) }
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.pathname !== requiredPath ||
      parsed.search || parsed.hash || parsed.href !== raw) {
    throw new Error(`Platform OAuth ${name} endpoint is invalid.`)
  }
  return parsed.href
}

function oauthEndpointAuthority(endpoint) { return endpoint.startsWith('/') ? '' : new URL(endpoint).origin }

function validateToken(value, requestedScopes) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Token response is invalid.')
  validateText(value.access_token, 8192)
  validateText(value.scope, 4096)
  if (value.token_type !== 'Bearer' || !Number.isSafeInteger(value.expires_in) || value.expires_in < 1 || value.expires_in > 86400 || 'refresh_token' in value) {
    throw new Error('Token response authority is invalid.')
  }
  const scopes = value.scope.split(' ')
  const requested = new Set(requestedScopes)
  if (new Set(scopes).size !== scopes.length || !scopes.includes('openid') || scopes.some((scope) => !requested.has(scope))) {
    throw new Error('Token contains permissions outside the requested Platform authority.')
  }
  return { accessToken: value.access_token, scopes }
}

async function fetchJSON(path, maximumBytes) {
  const response = await fetch(path, { method: 'GET', cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer', headers: { Accept: 'application/json' } })
  if (!response.ok) throw new Error(`Configuration request failed with HTTP ${response.status}.`)
  return boundedJSON(response, maximumBytes)
}

async function boundedJSON(response, maximumBytes) {
  const raw = await response.text()
  if (new TextEncoder().encode(raw).length > maximumBytes) throw new Error('Response exceeded its size limit.')
  return JSON.parse(raw)
}

function renderSignedIn(scopes) {
  clearError()
  loginButton.hidden = true
  sessionButton.textContent = 'Sign out'
  statusTitle.textContent = 'Platform session active'
  statusCopy.textContent = 'The opaque Hydra access token is held only in this page memory and will be lost on refresh.'
  scopeList.replaceChildren(...scopes.map((scope) => { const item = document.createElement('li'); item.textContent = scope; return item }))
}

function renderSignedOut() {
  loginButton.hidden = false
  loginButton.disabled = false
  sessionButton.textContent = 'Sign in'
  statusTitle.textContent = 'Not signed in'
  statusCopy.textContent = 'No access token is stored. Sign in to establish an in-memory Platform session.'
  const item = document.createElement('li')
  item.textContent = 'None'
  scopeList.replaceChildren(item)
}

function showError(error) {
  errorElement.textContent = error instanceof Error ? error.message : 'Authorization failed.'
  errorElement.hidden = false
}

function clearError() { errorElement.hidden = true; errorElement.textContent = '' }
function requiredElement(id) { const value = document.getElementById(id); if (!value) throw new Error(`Missing UI element ${id}.`); return value }
function validateText(value, maximum) { if (typeof value !== 'string' || !value || value.length > maximum || value.trim() !== value || /[\0\r\n]/u.test(value)) throw new Error('Protocol text is invalid.') }
function requireExactKeys(value, expected) { if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).sort().join('\0') !== [...expected].sort().join('\0')) throw new Error('Configuration fields are invalid.') }
function validPKCESecret(value) { return typeof value === 'string' && value.length >= 43 && value.length <= 128 && /^[A-Za-z0-9._~-]+$/u.test(value) }
function randomBase64URL() { const value = new Uint8Array(32); window.crypto.getRandomValues(value); return encodeBase64URL(value) }
function encodeBase64URL(bytes) { let binary = ''; for (const value of bytes) binary += String.fromCharCode(value); return btoa(binary).replace(/\+/gu, '-').replace(/\//gu, '_').replace(/=+$/gu, '') }
