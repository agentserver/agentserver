const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u

export function userSessionsPath(workspaceID) {
  return `/v2/workspaces/${encodeURIComponent(validateID('workspace ID', workspaceID))}/sessions`
}

export function userSessionPath(workspaceID, sessionID) {
  return `${userSessionsPath(workspaceID)}/${encodeURIComponent(validateID('session ID', sessionID))}`
}

export function archiveUserSessionPath(workspaceID, sessionID) {
  return `${userSessionPath(workspaceID, sessionID)}/actions/archive`
}

export function newSessionID(cryptoAPI) {
  if (!cryptoAPI || typeof cryptoAPI.randomUUID !== 'function') throw new Error('browser UUID generation is unavailable')
  return validateID('generated session ID', cryptoAPI.randomUUID())
}

export function sessionTitleFromPrompt(prompt) {
  if (typeof prompt !== 'string') return 'New conversation'
  const canonical = prompt.replace(/\s+/gu, ' ').trim()
  if (!canonical) return 'New conversation'
  return truncateUTF8(canonical, 120)
}

export function validateUserSessionList(value, workspaceID) {
  requireExactObject(value, ['sessions'], 'session list')
  if (!Array.isArray(value.sessions) || value.sessions.length > 256) throw new Error('session list is outside bounds')
  const seen = new Set()
  const sessions = value.sessions.map((item) => {
    const session = validateUserSessionState(item, workspaceID)
    if (session.status !== 'active' || seen.has(session.sessionId)) throw new Error('session list contains archived or duplicate state')
    seen.add(session.sessionId)
    return session
  })
  return Object.freeze(sessions)
}

export function validateUserSessionMutation(value, workspaceID, sessionID, flagName) {
  requireExactObject(value, ['session', flagName], 'session mutation')
  if (typeof value[flagName] !== 'boolean') throw new Error(`session mutation ${flagName} flag is invalid`)
  return Object.freeze({
    session: validateUserSessionState(value.session, workspaceID, sessionID),
    [flagName]: value[flagName],
  })
}

export function validateUserSessionState(value, workspaceID, sessionID = '') {
  const names = ['sessionId', 'workspaceId', 'title', 'status', 'version', 'createdAt', 'updatedAt']
  if (value && Object.prototype.hasOwnProperty.call(value, 'activeRunId')) names.push('activeRunId')
  requireExactObject(value, names, 'session')
  const canonicalWorkspaceID = validateID('workspace ID', workspaceID)
  const canonicalSessionID = validateID('session ID', value.sessionId)
  if (value.workspaceId !== canonicalWorkspaceID || (sessionID && canonicalSessionID !== validateID('expected session ID', sessionID))) {
    throw new Error('session escaped its requested scope')
  }
  validateTitle(value.title)
  if (value.status !== 'active' && value.status !== 'archived') throw new Error('session status is invalid')
  if ('activeRunId' in value) validateID('active run ID', value.activeRunId)
  if (!Number.isSafeInteger(value.version) || value.version < 1) throw new Error('session version is invalid')
  const createdAt = Date.parse(value.createdAt)
  const updatedAt = Date.parse(value.updatedAt)
  if (!Number.isFinite(createdAt) || !Number.isFinite(updatedAt) || updatedAt < createdAt) throw new Error('session timestamps are invalid')
  return Object.freeze({ ...value })
}

export function validateSessionTitle(title) {
  validateTitle(title)
  return title
}

function validateID(label, value) {
  if (typeof value !== 'string' || !uuidPattern.test(value) || value === '00000000-0000-0000-0000-000000000000') {
    throw new Error(`${label} must be a non-zero canonical UUID`)
  }
  return value
}

function validateTitle(title) {
  if (typeof title !== 'string' || title.trim() !== title || title.length === 0 || /\p{Cc}/u.test(title) || new TextEncoder().encode(title).length > 256) {
    throw new Error('session title must contain 1 to 256 canonical UTF-8 bytes without control characters')
  }
}

function truncateUTF8(value, maximumBytes) {
  let result = ''
  for (const character of value) {
    if (new TextEncoder().encode(result + character).length > maximumBytes) break
    result += character
  }
  return result || 'New conversation'
}

function requireExactObject(value, names, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`)
  const actual = Object.keys(value).sort()
  const expected = [...names].sort()
  if (actual.length !== expected.length || actual.some((name, index) => name !== expected[index])) {
    throw new Error(`${label} contains missing or unknown fields`)
  }
}
