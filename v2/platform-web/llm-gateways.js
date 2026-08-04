import { createResourceID, validateText, validateUUID } from './resources.js'

const oidcSecretPattern = /^[A-Za-z0-9._~-]{43,128}$/u

export function workspaceLLMGatewaysPath(workspaceID) {
  return `/v2/workspaces/${encodeURIComponent(validateUUID('workspace ID', workspaceID))}/llm-gateways`
}

export function workspaceLLMGatewayActionPath(workspaceID, gatewayID, action) {
  if (!['authorize', 'completeAuthorization', 'revoke', 'disable'].includes(action)) throw new Error('Unsupported LLM Gateway action.')
  return `${workspaceLLMGatewaysPath(workspaceID)}/${encodeURIComponent(validateUUID('Gateway ID', gatewayID))}:${action}`
}

export function createBrowserBinding(cryptoAPI) {
  const bytes = new Uint8Array(32)
  cryptoAPI.getRandomValues(bytes)
  let binary = ''
  for (const value of bytes) binary += String.fromCharCode(value)
  return btoa(binary).replace(/\+/gu, '-').replace(/\//gu, '_').replace(/=+$/gu, '')
}

export function buildCreateGatewayRequest(form, cryptoAPI) {
  const scopes = String(form.oidcScopes || '').trim().split(/\s+/u)
  if (scopes.length < 1 || scopes.length > 16 || new Set(scopes).size !== scopes.length ||
      !scopes.includes('openid') || !scopes.includes('offline_access')) throw new Error('OIDC scopes must be unique and include openid and offline_access.')
  scopes.forEach((scope) => { validateText('OIDC scope', scope, 128); if (/\s/u.test(scope)) throw new Error('OIDC scope is invalid.') })
  const bearerTokenType = form.bearerTokenType || 'id_token'
  if (!['id_token', 'access_token'].includes(bearerTokenType)) throw new Error('Bearer type is invalid.')
  return Object.freeze({
    gatewayId: createResourceID(cryptoAPI),
    name: validateText('Gateway name', form.name, 128),
    responsesUrl: validateResponsesURL(form.responsesUrl),
    oidcIssuer: validateIssuer(form.oidcIssuer),
    oidcClientId: validateText('OIDC client ID', form.oidcClientId, 512),
    oidcScopes: Object.freeze(scopes),
    bearerTokenType,
    defaultModel: validateText('default model', form.defaultModel, 256),
    makeDefault: Boolean(form.makeDefault),
  })
}

export function validateGatewayList(value, workspaceID) {
  workspaceID = validateUUID('workspace ID', workspaceID)
  requireObject(value, ['gateways'], 'Gateway list')
  if (!Array.isArray(value.gateways) || value.gateways.length > 128) throw new Error('Gateway list is outside protocol bounds.')
  return Object.freeze(value.gateways.map((gateway) => validateGateway(gateway, workspaceID)))
}

export function validateCreateGateway(value, workspaceID) {
  requireObject(value, ['gateway', 'created'], 'Gateway creation')
  if (typeof value.created !== 'boolean') throw new Error('Gateway creation result is invalid.')
  return validateGateway(value.gateway, validateUUID('workspace ID', workspaceID))
}

export function validateBeginAuthorization(value, gatewayID, nowMS = Date.now()) {
  requireObject(value, ['gatewayId', 'authorizationUrl', 'expiresAt'], 'Gateway authorization')
  if (value.gatewayId !== validateUUID('Gateway ID', gatewayID)) throw new Error('Gateway authorization escaped its scope.')
  const authorizationUrl = validateHTTPSURL('authorization URL', value.authorizationUrl, 8192)
  const expires = Date.parse(value.expiresAt)
  if (!Number.isFinite(expires) || expires <= nowMS || expires - nowMS > 10 * 60 * 1000) throw new Error('Gateway authorization expiry is invalid.')
  return Object.freeze({ ...value, authorizationUrl })
}

export function validateGatewayCallbackMessage(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value) || value.type !== 'agentserver-v2.llm-gateway-oidc-callback' || value.version !== 1) {
    throw new Error('Gateway callback message is invalid.')
  }
  if ('protocolError' in value) throw new Error('The third-party Gateway returned an invalid callback.')
  requireObject(value, ['type', 'version', 'state', 'code', 'providerError', 'providerErrorDescription'], 'Gateway callback')
  if (!oidcSecretPattern.test(value.state) || Boolean(value.code) === Boolean(value.providerError)) throw new Error('Gateway callback outcome is invalid.')
  if (value.code) validateText('authorization code', value.code, 8192)
  if (value.providerError) validateText('provider error', value.providerError, 128)
  if (typeof value.providerErrorDescription !== 'string' || value.providerErrorDescription.length > 8192 || /[\0\r\n]/u.test(value.providerErrorDescription)) {
    throw new Error('Gateway callback description is invalid.')
  }
  return Object.freeze({ ...value })
}

export function validateCompleteAuthorization(value, gatewayID) {
  requireObject(value, ['gatewayId', 'grantStatus', 'bearerExpiresAt'], 'Gateway authorization completion')
  if (value.gatewayId !== validateUUID('Gateway ID', gatewayID) || value.grantStatus !== 'active' || !validTimestamp(value.bearerExpiresAt)) {
    throw new Error('Gateway authorization completion is invalid.')
  }
}

export function validateRevokeGrant(value, gatewayID) {
  requireObject(value, ['gatewayId', 'grantStatus', 'changed'], 'Gateway grant revocation')
  if (value.gatewayId !== validateUUID('Gateway ID', gatewayID) || value.grantStatus !== 'revoked' || typeof value.changed !== 'boolean') {
    throw new Error('Gateway revocation result is invalid.')
  }
}

export function validateDisableGateway(value, gatewayID) {
  requireObject(value, ['gatewayId', 'status', 'version', 'changed'], 'Gateway disable result')
  if (value.gatewayId !== validateUUID('Gateway ID', gatewayID) || value.status !== 'disabled' || !Number.isSafeInteger(value.version) ||
      value.version < 1 || typeof value.changed !== 'boolean') throw new Error('Gateway disable result is invalid.')
}

function validateGateway(value, workspaceID) {
  const required = ['gatewayId', 'workspaceId', 'name', 'responsesUrl', 'oidcIssuer', 'oidcClientId', 'oidcScopes', 'bearerTokenType',
    'defaultModel', 'status', 'default', 'version', 'grantStatus', 'createdAt', 'updatedAt']
  requireObject(value, value && Object.prototype.hasOwnProperty.call(value, 'grantExpiresAt') ? [...required, 'grantExpiresAt'] : required, 'Gateway')
  validateUUID('Gateway ID', value.gatewayId)
  if (value.workspaceId !== workspaceID) throw new Error('Gateway escaped its workspace scope.')
  validateText('Gateway name', value.name, 128)
  validateResponsesURL(value.responsesUrl)
  validateIssuer(value.oidcIssuer)
  validateText('OIDC client ID', value.oidcClientId, 512)
  validateText('default model', value.defaultModel, 256)
  if (!Array.isArray(value.oidcScopes) || !['id_token', 'access_token'].includes(value.bearerTokenType) ||
      !['active', 'disabled'].includes(value.status) || typeof value.default !== 'boolean' || !Number.isSafeInteger(value.version) || value.version < 1 ||
      !['', 'active', 'reauth_required', 'revoked'].includes(value.grantStatus) || !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt) ||
      ('grantExpiresAt' in value && !validTimestamp(value.grantExpiresAt))) throw new Error('Gateway state is invalid.')
  return Object.freeze({ ...value, oidcScopes: Object.freeze([...value.oidcScopes]) })
}

function validateResponsesURL(raw) {
  const value = validateHTTPSURL('Responses URL', raw, 4096)
  const parsed = new URL(value)
  if (parsed.pathname !== '/v1/responses' || parsed.search || parsed.hash || (parsed.port && parsed.port !== '443')) throw new Error('Responses URL must end at exact /v1/responses.')
  return value
}

function validateIssuer(raw) {
  const value = validateHTTPSURL('OIDC issuer', raw, 2048)
  const parsed = new URL(value)
  if (parsed.search || parsed.hash || value.endsWith('/')) throw new Error('OIDC issuer must not contain query, fragment, or trailing slash.')
  return value
}

function validateHTTPSURL(label, raw, maximum) {
  validateText(label, raw, maximum)
  const parsed = new URL(raw)
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password || !parsed.hostname || parsed.hostname !== parsed.hostname.toLowerCase() || parsed.hostname.endsWith('.') || raw.includes('\\')) {
    throw new Error(`${label} must be a canonical public HTTPS URL.`)
  }
  return raw
}

function validTimestamp(value) { return typeof value === 'string' && value.length <= 64 && Number.isFinite(Date.parse(value)) }

function requireObject(value, names, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object.`)
  const actual = Object.keys(value).sort()
  const expected = [...names].sort()
  if (actual.length !== expected.length || actual.some((name, index) => name !== expected[index])) throw new Error(`${label} contains missing or unknown fields.`)
}
