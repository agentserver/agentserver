export function readAuthorizationCallback(rawSearch) {
  const parameters = new URLSearchParams(String(rawSearch || '').replace(/^\?/, ''))
  const allowed = new Set(['code', 'state', 'scope', 'error', 'error_description', 'error_uri', 'iss', 'session_state'])
  if (![...parameters.keys()].some((name) => allowed.has(name))) return null
  for (const [name, value] of parameters) {
    if (!allowed.has(name) || parameters.getAll(name).length !== 1 || !value || value.length > 8192 || /[\0\r\n]/u.test(value)) {
      throw new Error('The authorization callback is invalid.')
    }
  }
  const state = parameters.get('state') || ''
  const code = parameters.get('code') || ''
  const error = parameters.get('error') || ''
  if (!state || Boolean(code) === Boolean(error)) throw new Error('The authorization callback is incomplete.')
  return Object.freeze({ state, code, error, scopes: parseCallbackScopes(parameters.get('scope') || '') })
}

function parseCallbackScopes(raw) {
  if (!raw) return Object.freeze([])
  const scopes = raw.split(' ')
  if (scopes.length > 32 || new Set(scopes).size !== scopes.length ||
      scopes.some((scope) => !scope || scope.length > 128 || scope.trim() !== scope || /\s/u.test(scope))) {
    throw new Error('The authorization callback scope is invalid.')
  }
  return Object.freeze(scopes)
}
