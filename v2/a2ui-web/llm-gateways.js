const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/u
const oidcSecretPattern = /^[A-Za-z0-9._~-]{43,128}$/u
export const GATEWAY_CALLBACK_CHANNEL_NAME = 'agentserver-v2.llm-gateway-oidc-callback.v1'

export function createGatewayCallbackChannel(BroadcastChannelAPI) {
  if (typeof BroadcastChannelAPI !== 'function') return null
  try {
    return new BroadcastChannelAPI(GATEWAY_CALLBACK_CHANNEL_NAME)
  } catch {
    return null
  }
}

export function workspaceLLMGatewaysPath(workspaceID) {
  return `/v2/workspaces/${encodeURIComponent(validateUUID('workspace ID', workspaceID))}/llm-gateways`
}

export function workspaceLLMGatewayActionPath(workspaceID, gatewayID, action) {
  if (!['authorize', 'completeAuthorization', 'revoke', 'disable'].includes(action)) throw new Error('unsupported workspace LLM Gateway action')
  return `${workspaceLLMGatewaysPath(workspaceID)}/${encodeURIComponent(validateUUID('Gateway ID', gatewayID))}:${action}`
}

export function createBrowserBinding(cryptoAPI) {
  if (!cryptoAPI || typeof cryptoAPI.getRandomValues !== 'function') throw new Error('browser cryptography is required')
  const bytes = new Uint8Array(32)
  cryptoAPI.getRandomValues(bytes)
  return encodeBase64URL(bytes)
}

export function createGatewayID(cryptoAPI) {
  if (!cryptoAPI || typeof cryptoAPI.getRandomValues !== 'function') throw new Error('browser cryptography is required')
  if (typeof cryptoAPI.randomUUID === 'function') return validateUUID('generated Gateway ID', cryptoAPI.randomUUID())
  const bytes = new Uint8Array(16)
  cryptoAPI.getRandomValues(bytes)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const raw = Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
  return `${raw.slice(0, 8)}-${raw.slice(8, 12)}-${raw.slice(12, 16)}-${raw.slice(16, 20)}-${raw.slice(20)}`
}

export function buildCreateGatewayRequest(form, cryptoAPI) {
  if (!form || typeof form !== 'object') throw new Error('Gateway form is required')
  const name = validateText('Gateway name', form.name, 128)
  const responsesUrl = validateExactResponsesURL(form.responsesUrl)
  const oidcIssuer = validateIssuer(form.oidcIssuer)
  const oidcClientId = validateText('OIDC client ID', form.oidcClientId, 512)
  const defaultModel = validateText('default model', form.defaultModel, 256)
  const bearerTokenType = form.bearerTokenType || 'id_token'
  if (!['id_token', 'access_token'].includes(bearerTokenType)) throw new Error('bearer token type must be id_token or access_token')
  const scopes = String(form.oidcScopes || '').trim()
    ? String(form.oidcScopes).trim().split(/\s+/u)
    : ['openid', 'offline_access']
  validateScopes(scopes)
  return Object.freeze({
    gatewayId: createGatewayID(cryptoAPI),
    name,
    responsesUrl,
    oidcIssuer,
    oidcClientId,
    oidcScopes: Object.freeze([...scopes]),
    bearerTokenType,
    defaultModel,
    makeDefault: Boolean(form.makeDefault),
  })
}

export function validateGatewayList(value, workspaceID) {
  workspaceID = validateUUID('workspace ID', workspaceID)
  requireExactObject(value, ['gateways'], 'Gateway list')
  if (!Array.isArray(value.gateways) || value.gateways.length > 128) throw new Error('Gateway list is outside protocol bounds')
  return Object.freeze(value.gateways.map((gateway) => validateGateway(gateway, workspaceID)))
}

export function validateCreateGateway(value, workspaceID) {
  requireExactObject(value, ['gateway', 'created'], 'Gateway creation result')
  if (typeof value.created !== 'boolean') throw new Error('Gateway creation result has an invalid created flag')
  const [gateway] = validateGatewayList({ gateways: [value.gateway] }, workspaceID)
  return Object.freeze({ gateway, created: value.created })
}

export function validateBeginAuthorization(value, gatewayID, nowMS = Date.now()) {
  requireExactObject(value, ['gatewayId', 'authorizationUrl', 'expiresAt'], 'Gateway authorization result')
  if (validateUUID('Gateway ID', value.gatewayId) !== validateUUID('expected Gateway ID', gatewayID)) throw new Error('Gateway authorization escaped its requested scope')
  const authorizationURL = validateHTTPSURL('authorization URL', value.authorizationUrl, 8192)
  const parsedAuthorizationURL = new URL(authorizationURL)
  const callbackStates = parsedAuthorizationURL.searchParams.getAll('state')
  if (parsedAuthorizationURL.hash || callbackStates.length !== 1 || !oidcSecretPattern.test(callbackStates[0])) {
    throw new Error('Gateway authorization callback state is invalid')
  }
  const expiresAtMS = Date.parse(value.expiresAt)
  if (!Number.isFinite(expiresAtMS) || !Number.isSafeInteger(nowMS) || expiresAtMS <= nowMS || expiresAtMS - nowMS > 10 * 60 * 1000) {
    throw new Error('Gateway authorization expiry is invalid')
  }
  return Object.freeze({
    gatewayId: value.gatewayId, authorizationUrl: authorizationURL,
    callbackState: callbackStates[0], expiresAt: value.expiresAt,
  })
}

export function validateGatewayCallbackMessage(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value) ||
      value.type !== 'agentserver-v2.llm-gateway-oidc-callback' || value.version !== 1) {
    throw new Error('Gateway callback message has an invalid protocol envelope')
  }
  if ('protocolError' in value) {
    requireExactObject(value, ['type', 'version', 'protocolError'], 'Gateway callback message')
    if (value.protocolError !== 'invalid_callback') throw new Error('Gateway callback protocol error is invalid')
    throw new Error('third-party Gateway returned an invalid OIDC callback')
  }
  requireExactObject(value, ['type', 'version', 'state', 'code', 'providerError', 'providerErrorDescription'], 'Gateway callback message')
  if (!oidcSecretPattern.test(value.state) || Boolean(value.code) === Boolean(value.providerError)) {
    throw new Error('Gateway callback state or outcome is invalid')
  }
  if (value.code) validateText('authorization code', value.code, 8192)
  if (value.providerError) validateText('provider error', value.providerError, 128)
  if (typeof value.providerErrorDescription !== 'string' || value.providerErrorDescription.length > 8192 || /[\0\r\n]/u.test(value.providerErrorDescription)) {
    throw new Error('Gateway callback error description is invalid')
  }
  return Object.freeze({
    state: value.state,
    code: value.code,
    providerError: value.providerError,
    providerErrorDescription: value.providerErrorDescription,
  })
}

export function validateCompleteAuthorization(value, gatewayID) {
  requireExactObject(value, ['gatewayId', 'grantStatus', 'bearerExpiresAt'], 'Gateway authorization completion')
  if (value.gatewayId !== validateUUID('Gateway ID', gatewayID) || value.grantStatus !== 'active' || !validTimestamp(value.bearerExpiresAt)) {
    throw new Error('Gateway authorization completion is invalid')
  }
  return Object.freeze({ gatewayId: value.gatewayId, grantStatus: value.grantStatus, bearerExpiresAt: value.bearerExpiresAt })
}

export function validateRevokeGrant(value, gatewayID) {
  requireExactObject(value, ['gatewayId', 'grantStatus', 'changed'], 'Gateway grant revocation')
  if (value.gatewayId !== validateUUID('Gateway ID', gatewayID) || value.grantStatus !== 'revoked' || typeof value.changed !== 'boolean') {
    throw new Error('Gateway grant revocation is invalid')
  }
  return Object.freeze({ gatewayId: value.gatewayId, grantStatus: value.grantStatus, changed: value.changed })
}

export function validateDisableGateway(value, gatewayID) {
  requireExactObject(value, ['gatewayId', 'status', 'version', 'changed'], 'Gateway disable result')
  if (value.gatewayId !== validateUUID('Gateway ID', gatewayID) || value.status !== 'disabled' ||
      !Number.isSafeInteger(value.version) || value.version < 1 || typeof value.changed !== 'boolean') {
    throw new Error('Gateway disable result is invalid')
  }
  return Object.freeze({ gatewayId: value.gatewayId, status: value.status, version: value.version, changed: value.changed })
}

function validateGateway(value, workspaceID) {
  const required = [
    'gatewayId', 'workspaceId', 'name', 'responsesUrl', 'oidcIssuer', 'oidcClientId', 'oidcScopes', 'bearerTokenType',
    'defaultModel', 'status', 'default', 'version', 'grantStatus', 'createdAt', 'updatedAt',
  ]
  const expected = 'grantExpiresAt' in (value || {}) ? [...required, 'grantExpiresAt'] : required
  requireExactObject(value, expected, 'Gateway state')
  if (validateUUID('Gateway workspace ID', value.workspaceId) !== workspaceID) throw new Error('Gateway state escaped its workspace scope')
  validateUUID('Gateway ID', value.gatewayId)
  validateText('Gateway name', value.name, 128)
  validateExactResponsesURL(value.responsesUrl)
  validateIssuer(value.oidcIssuer)
  validateText('OIDC client ID', value.oidcClientId, 512)
  validateScopes(value.oidcScopes)
  validateText('default model', value.defaultModel, 256)
  if (!['id_token', 'access_token'].includes(value.bearerTokenType) || !['active', 'disabled'].includes(value.status) ||
      typeof value.default !== 'boolean' || !Number.isSafeInteger(value.version) || value.version < 1 ||
      !['', 'active', 'reauth_required', 'revoked'].includes(value.grantStatus) || !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt) ||
      ('grantExpiresAt' in value && !validTimestamp(value.grantExpiresAt))) {
    throw new Error('Gateway state contains an invalid status, version, or timestamp')
  }
  return Object.freeze({ ...value, oidcScopes: Object.freeze([...value.oidcScopes]) })
}

function validateExactResponsesURL(raw) {
  const value = validateHTTPSURL('Responses URL', raw, 4096)
  const parsed = new URL(value)
  if (parsed.pathname !== '/v1/responses' || parsed.search || parsed.hash || (parsed.port && parsed.port !== '443')) {
    throw new Error('Responses URL must be an exact public HTTPS /v1/responses endpoint')
  }
  return value
}

function validateIssuer(raw) {
  const value = validateHTTPSURL('OIDC issuer', raw, 2048)
  const parsed = new URL(value)
  if (parsed.search || parsed.hash || value.endsWith('/')) throw new Error('OIDC issuer must not contain query, fragment, or trailing slash')
  return value
}

function validateHTTPSURL(label, raw, maximum) {
  validateText(label, raw, maximum)
  const parsed = new URL(raw)
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password || !parsed.hostname ||
      parsed.hostname !== parsed.hostname.toLowerCase() || parsed.hostname.endsWith('.') || raw.includes('\\')) {
    throw new Error(`${label} must be a canonical HTTPS URL without credentials`)
  }
  return raw
}

function validateScopes(scopes) {
  if (!Array.isArray(scopes) || scopes.length < 1 || scopes.length > 16) throw new Error('OIDC scopes are outside protocol bounds')
  const unique = new Set()
  for (const scope of scopes) {
    validateText('OIDC scope', scope, 128)
    if (/\s/u.test(scope) || unique.has(scope)) throw new Error('OIDC scopes must be unique tokens')
    unique.add(scope)
  }
  if (!unique.has('openid') || !unique.has('offline_access')) throw new Error('OIDC scopes must include openid and offline_access')
}

function validateUUID(label, value) {
  if (typeof value !== 'string' || !uuidPattern.test(value) || value === '00000000-0000-0000-0000-000000000000') throw new Error(`${label} is not a canonical UUID`)
  return value
}

function validateText(label, value, maximum) {
  if (typeof value !== 'string' || value.length < 1 || value.length > maximum || value.trim() !== value || /[\0\r\n]/u.test(value)) {
    throw new Error(`${label} is empty or outside protocol bounds`)
  }
  return value
}

function validTimestamp(value) {
  return typeof value === 'string' && value.length <= 64 && Number.isFinite(Date.parse(value))
}

function requireExactObject(value, names, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`)
  const actual = Object.keys(value).sort()
  const expected = [...names].sort()
  if (actual.length !== expected.length || actual.some((name, index) => name !== expected[index])) throw new Error(`${label} contains missing or unknown fields`)
}

function encodeBase64URL(bytes) {
  let binary = ''
  for (const value of bytes) binary += String.fromCharCode(value)
  return btoa(binary).replace(/\+/gu, '-').replace(/\//gu, '_').replace(/=+$/gu, '')
}
