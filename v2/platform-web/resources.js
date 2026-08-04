const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u

export function createResourceID(cryptoAPI) {
  if (!cryptoAPI || typeof cryptoAPI.getRandomValues !== 'function') throw new Error('Browser cryptography is unavailable.')
  if (typeof cryptoAPI.randomUUID === 'function') return validateUUID('generated resource ID', cryptoAPI.randomUUID())
  const bytes = new Uint8Array(16)
  cryptoAPI.getRandomValues(bytes)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const raw = Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
  return `${raw.slice(0, 8)}-${raw.slice(8, 12)}-${raw.slice(12, 16)}-${raw.slice(16, 20)}-${raw.slice(20)}`
}

export function workspacesPath() { return '/v2/workspaces' }
export function workspacePath(workspaceID) { return `${workspacesPath()}/${encodeURIComponent(validateUUID('workspace ID', workspaceID))}` }
export function workspaceArchivePath(workspaceID) { return `${workspacePath(workspaceID)}/actions/archive` }
export function workspaceMembersPath(workspaceID) { return `${workspacePath(workspaceID)}/members` }
export function workspaceMemberPath(workspaceID, userID) { return `${workspaceMembersPath(workspaceID)}/${encodeURIComponent(validateUUID('user ID', userID))}` }
export function workspaceExecutorsPath(workspaceID) { return `${workspacePath(workspaceID)}/executors` }
export function executorPath(workspaceID, executorID) { return `${workspaceExecutorsPath(workspaceID)}/${encodeURIComponent(validateUUID('executor ID', executorID))}` }
export function executorEnrollmentPath(workspaceID, executorID) { return `${executorPath(workspaceID, executorID)}:enrollmentToken` }

export function validateWorkspaceList(value) {
  requireExactObject(value, ['workspaces'], 'Workspace list')
  if (!Array.isArray(value.workspaces) || value.workspaces.length > 256) throw new Error('Workspace list is outside protocol bounds.')
  return Object.freeze(value.workspaces.map(validateWorkspace))
}

export function validateWorkspaceResult(value, operation) {
  const flag = operation === 'create' ? 'created' : 'changed'
  requireExactObject(value, ['workspace', flag], 'Workspace result')
  if (typeof value[flag] !== 'boolean') throw new Error('Workspace result flag is invalid.')
  return Object.freeze({ workspace: validateWorkspace(value.workspace), [flag]: value[flag] })
}

export function validateWorkspace(value) {
  requireExactObject(value, ['workspaceId', 'name', 'status', 'currentUserRole', 'version', 'createdAt', 'updatedAt'], 'Workspace')
  validateUUID('workspace ID', value.workspaceId)
  validateText('workspace name', value.name, 256)
  if (!['active', 'suspended', 'archived'].includes(value.status) || !['owner', 'developer', 'viewer'].includes(value.currentUserRole) ||
      !Number.isSafeInteger(value.version) || value.version < 1 || !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt)) {
    throw new Error('Workspace state is invalid.')
  }
  return Object.freeze({ ...value })
}

export function validateMemberList(value) {
  requireExactObject(value, ['members'], 'Member list')
  if (!Array.isArray(value.members) || value.members.length > 256) throw new Error('Member list is outside protocol bounds.')
  return Object.freeze(value.members.map(validateMember))
}

export function validateMemberResult(value, operation) {
  const flag = operation === 'add' ? 'created' : 'changed'
  requireExactObject(value, ['member', flag], 'Member result')
  if (typeof value[flag] !== 'boolean') throw new Error('Member result flag is invalid.')
  return Object.freeze({ member: validateMember(value.member), [flag]: value[flag] })
}

export function validateRemoveMember(value, userID) {
  requireExactObject(value, ['userId', 'removed'], 'Member removal')
  if (value.userId !== validateUUID('user ID', userID) || typeof value.removed !== 'boolean') throw new Error('Member removal result is invalid.')
  return Object.freeze({ ...value })
}

export function validateMember(value) {
  requireExactObject(value, ['userId', 'role', 'version', 'createdAt', 'updatedAt'], 'Member')
  validateUUID('user ID', value.userId)
  if (!['owner', 'developer', 'viewer'].includes(value.role) || !Number.isSafeInteger(value.version) || value.version < 1 ||
      !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt)) throw new Error('Member state is invalid.')
  return Object.freeze({ ...value })
}

export function validateExecutorList(value, workspaceID) {
  workspaceID = validateUUID('workspace ID', workspaceID)
  requireExactObject(value, ['executors'], 'Executor list')
  if (!Array.isArray(value.executors) || value.executors.length > 256) throw new Error('Executor list is outside protocol bounds.')
  return Object.freeze(value.executors.map((executor) => validateExecutor(executor, workspaceID)))
}

export function validateExecutorResult(value, workspaceID, operation) {
  const flag = operation === 'create' ? 'created' : 'changed'
  requireExactObject(value, ['executor', flag], 'Executor result')
  if (typeof value[flag] !== 'boolean') throw new Error('Executor result flag is invalid.')
  return Object.freeze({ executor: validateExecutor(value.executor, workspaceID), [flag]: value[flag] })
}

export function validateEnrollmentToken(value, executorID) {
  requireExactObject(value, ['executorId', 'token', 'expiresAt', 'created'], 'Enrollment token')
  if (value.executorId !== validateUUID('executor ID', executorID) || typeof value.token !== 'string' || value.token.length < 40 ||
      value.token.length > 8192 || /[\0\r\n]/u.test(value.token) || !validTimestamp(value.expiresAt) || typeof value.created !== 'boolean') {
    throw new Error('Enrollment token response is invalid.')
  }
  return Object.freeze({ ...value })
}

function validateExecutor(value, workspaceID) {
  requireExactObject(value, ['executorId', 'workspaceId', 'status', 'version', 'createdAt', 'updatedAt'], 'Executor')
  validateUUID('executor ID', value.executorId)
  if (value.workspaceId !== workspaceID || !['enrolling', 'offline', 'online', 'revoked'].includes(value.status) ||
      !Number.isSafeInteger(value.version) || value.version < 1 || !validTimestamp(value.createdAt) || !validTimestamp(value.updatedAt)) {
    throw new Error('Executor state is invalid.')
  }
  return Object.freeze({ ...value })
}

export function validateUUID(label, value) {
  if (typeof value !== 'string' || !uuidPattern.test(value) || value === '00000000-0000-0000-0000-000000000000') throw new Error(`${label} is not a canonical UUID.`)
  return value
}

export function validateText(label, value, maximum) {
  if (typeof value !== 'string' || value.length < 1 || value.length > maximum || value.trim() !== value || /[\0\r\n]/u.test(value)) {
    throw new Error(`${label} is empty or outside protocol bounds.`)
  }
  return value
}

function validTimestamp(value) { return typeof value === 'string' && value.length <= 64 && Number.isFinite(Date.parse(value)) }

function requireExactObject(value, names, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object.`)
  const actual = Object.keys(value).sort()
  const expected = [...names].sort()
  if (actual.length !== expected.length || actual.some((name, index) => name !== expected[index])) throw new Error(`${label} contains missing or unknown fields.`)
}
