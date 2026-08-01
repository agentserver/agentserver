export const EVENT_CURSOR_NAME = 'agentserver.event_cursor'
export const TOOL_PROGRESS_NAME = 'agentserver.tool_progress'
export const RUN_STATUS_NAME = 'agentserver.run_status'
export const APPROVAL_NAME = 'agentserver.approval'
export const A2UI_OPERATIONS_NAME = 'a2ui.operations'
export const A2UI_VERSION = 'v0.9'
export const A2UI_BASIC_CATALOG = 'https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json'
export const MAXIMUM_EVENT_STREAM_BYTES = 16 * 1024 * 1024

const canonicalUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const maximumDiagnostics = 80

export function isTerminalRunStatus(status) {
  return status === 'completed' || status === 'failed' || status === 'cancelled'
}

export function validateScopeID(label, value) {
  if (typeof value !== 'string' || !canonicalUUID.test(value)) {
    throw new Error(`${label} must be a canonical lowercase UUID`)
  }
  return value
}

export function validateBearer(value) {
  if (typeof value !== 'string' || value.length < 1 || value.length > 8192 || /[\s\0]/u.test(value)) {
    throw new Error('browser bearer must be one non-empty token without whitespace')
  }
  return value
}

export function buildRunEndpoint(workspaceID, sessionID) {
  validateScopeID('workspace ID', workspaceID)
  validateScopeID('session ID', sessionID)
  return `/v2/workspaces/${encodeURIComponent(workspaceID)}/sessions/${encodeURIComponent(sessionID)}/agui`
}

// buildRunRequest deliberately sends one new user message and no browser-owned
// history, state, context, or tools. Those authorities remain server-side.
export function buildRunRequest({ sessionID, clientRunID, messageID, prompt, cursor = '' }) {
  validateScopeID('session ID', sessionID)
  for (const [label, value, maximum] of [
    ['client run ID', clientRunID, 256],
    ['message ID', messageID, 256],
  ]) {
    if (typeof value !== 'string' || value.length < 1 || value.length > maximum || /[\r\n\0]/u.test(value)) {
      throw new Error(`${label} must be bounded text without line breaks`)
    }
  }
  if (typeof prompt !== 'string' || prompt.length < 1 || new TextEncoder().encode(prompt).length > 256 * 1024 || prompt.includes('\0')) {
    throw new Error('prompt must contain between 1 and 262144 bytes of UTF-8 text without NUL')
  }
  if (typeof cursor !== 'string' || /[\s\0]/u.test(cursor)) {
    throw new Error('event cursor must be opaque text without whitespace')
  }
  const request = {
    threadId: sessionID,
    runId: clientRunID,
    messages: [{ id: messageID, role: 'user', content: prompt }],
    tools: [],
    context: [],
  }
  if (cursor) {
    request.forwardedProps = { agentserver: { eventCursor: cursor } }
  }
  return request
}

export class SSEDecoder {
  constructor(maximumBytes = MAXIMUM_EVENT_STREAM_BYTES) {
    if (!Number.isSafeInteger(maximumBytes) || maximumBytes < 1) {
      throw new Error('maximum SSE bytes must be a positive safe integer')
    }
    this.maximumBytes = maximumBytes
    this.buffer = ''
    this.dataLines = []
    this.seenCharacters = 0
  }

  push(chunk) {
    if (typeof chunk !== 'string') throw new Error('SSE chunk must be text')
    this.seenCharacters += chunk.length
    if (this.seenCharacters > this.maximumBytes) {
      throw new Error(`AG-UI event stream exceeds ${this.maximumBytes} characters`)
    }
    this.buffer += chunk
    const events = []
    while (true) {
      const newline = this.buffer.indexOf('\n')
      if (newline < 0) break
      let line = this.buffer.slice(0, newline)
      this.buffer = this.buffer.slice(newline + 1)
      if (line.endsWith('\r')) line = line.slice(0, -1)
      this.#consumeLine(line, events)
    }
    return events
  }

  finish() {
    const events = []
    if (this.buffer.length > 0) {
      let line = this.buffer
      if (line.endsWith('\r')) line = line.slice(0, -1)
      this.buffer = ''
      this.#consumeLine(line, events)
    }
    this.#dispatch(events)
    return events
  }

  #consumeLine(line, events) {
    if (line === '') {
      this.#dispatch(events)
      return
    }
    if (line.startsWith(':')) return
    const separator = line.indexOf(':')
    const field = separator < 0 ? line : line.slice(0, separator)
    let value = separator < 0 ? '' : line.slice(separator + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'data') this.dataLines.push(value)
  }

  #dispatch(events) {
    if (this.dataLines.length === 0) return
    const raw = this.dataLines.join('\n')
    this.dataLines = []
    let event
    try {
      event = JSON.parse(raw)
    } catch (error) {
      throw new Error(`invalid AG-UI SSE JSON: ${error instanceof Error ? error.message : String(error)}`)
    }
    if (!isRecord(event) || typeof event.type !== 'string' || event.type.length === 0) {
      throw new Error('AG-UI SSE data must be an event object with a type')
    }
    events.push(event)
  }
}

export function createViewState() {
  return {
    status: 'idle',
    runID: '',
    cursor: '',
    cursorSequence: 0,
    messages: [],
    reasoning: [],
    tools: [],
    approvals: {},
    approvalOrder: [],
    surfaces: {},
    surfaceOrder: [],
    snapshot: null,
    error: null,
    eventCount: 0,
    diagnostics: [],
  }
}

export function cloneViewState(state) {
  if (typeof structuredClone === 'function') return structuredClone(state)
  return JSON.parse(JSON.stringify(state))
}

export function appendUserMessage(state, id, text) {
  requireText('user message ID', id)
  requireText('user message text', text)
  return {
    ...state,
    messages: [...state.messages, { id, role: 'user', text, complete: true }],
    error: null,
  }
}

export function reduceAGUIEvent(state, event) {
  if (!isRecord(state) || !Array.isArray(state.messages) || !isRecord(event) || typeof event.type !== 'string') {
    throw new Error('AG-UI reducer requires a valid state and event')
  }
  let next = {
    ...state,
    eventCount: state.eventCount + 1,
    diagnostics: appendDiagnostic(state.diagnostics, event),
  }
  switch (event.type) {
    case 'RUN_STARTED': {
      const runID = requireText('runId', event.runId)
      next = { ...next, status: 'running', runID, error: null }
      break
    }
    case 'TEXT_MESSAGE_START': {
      const messageID = requireText('messageId', event.messageId)
      if (next.messages.some((message) => message.id === messageID)) throw new Error(`message ${messageID} started more than once`)
      next.messages = [...next.messages, { id: messageID, role: event.role || 'assistant', text: '', complete: false }]
      break
    }
    case 'TEXT_MESSAGE_CONTENT': {
      next.messages = updateByID(next.messages, event.messageId, 'message', (message) => ({
        ...message,
        text: message.text + requireString('message delta', event.delta),
      }))
      break
    }
    case 'TEXT_MESSAGE_END': {
      next.messages = updateByID(next.messages, event.messageId, 'message', (message) => ({ ...message, complete: true }))
      break
    }
    case 'REASONING_MESSAGE_START': {
      const messageID = requireText('reasoning messageId', event.messageId)
      if (next.reasoning.some((message) => message.id === messageID)) throw new Error(`reasoning ${messageID} started more than once`)
      next.reasoning = [...next.reasoning, { id: messageID, text: '', complete: false }]
      break
    }
    case 'REASONING_MESSAGE_CONTENT': {
      next.reasoning = updateByID(next.reasoning, event.messageId, 'reasoning', (message) => ({
        ...message,
        text: message.text + requireString('reasoning delta', event.delta),
      }))
      break
    }
    case 'REASONING_MESSAGE_END': {
      next.reasoning = updateByID(next.reasoning, event.messageId, 'reasoning', (message) => ({ ...message, complete: true }))
      break
    }
    case 'TOOL_CALL_START': {
      const toolCallID = requireText('toolCallId', event.toolCallId)
      if (next.tools.some((tool) => tool.id === toolCallID)) throw new Error(`tool call ${toolCallID} started more than once`)
      next.tools = [...next.tools, {
        id: toolCallID,
        name: requireText('toolCallName', event.toolCallName),
        arguments: '',
        result: '',
        progress: null,
        status: 'arguments',
      }]
      break
    }
    case 'TOOL_CALL_ARGS': {
      next.tools = updateByID(next.tools, event.toolCallId, 'tool call', (tool) => ({
        ...tool,
        arguments: tool.arguments + requireString('tool argument delta', event.delta),
      }))
      break
    }
    case 'TOOL_CALL_END': {
      next.tools = updateByID(next.tools, event.toolCallId, 'tool call', (tool) => ({ ...tool, status: 'awaiting-result' }))
      break
    }
    case 'TOOL_CALL_RESULT': {
      next.tools = updateByID(next.tools, event.toolCallId, 'tool call', (tool) => ({
        ...tool,
        result: requireString('tool result', event.content),
        status: 'completed',
      }))
      break
    }
    case 'STATE_SNAPSHOT':
      next.snapshot = event.snapshot ?? null
      break
    case 'CUSTOM':
      next = reduceCustomEvent(next, event)
      break
    case 'RUN_FINISHED':
      if (event.runId && next.runID && event.runId !== next.runID) throw new Error('RUN_FINISHED escaped the active run')
      next = { ...next, status: 'completed', error: null }
      break
    case 'RUN_ERROR':
      if (event.runId && next.runID && event.runId !== next.runID) throw new Error('RUN_ERROR escaped the active run')
      next = {
        ...next,
        status: event.code === 'user_cancelled' || event.code === 'run.cancelled' ? 'cancelled' : 'failed',
        error: { code: typeof event.code === 'string' ? event.code : 'run_error', message: requireText('run error message', event.message) },
      }
      break
    default:
      break
  }
  return next
}

function reduceCustomEvent(state, event) {
  switch (event.name) {
    case EVENT_CURSOR_NAME: {
      const value = event.value
      if (!isRecord(value) || value.version !== 1 || typeof value.cursor !== 'string' || value.cursor.length === 0 ||
          !Number.isSafeInteger(value.lastEventSequence) || value.lastEventSequence < 0 ||
          (state.runID && value.runId !== state.runID)) {
        throw new Error('invalid lifecycle-safe event cursor')
      }
      return { ...state, cursor: value.cursor, cursorSequence: value.lastEventSequence }
    }
    case TOOL_PROGRESS_NAME: {
      const value = event.value
      if (!isRecord(value)) throw new Error('invalid tool progress payload')
      const progress = {
        value: typeof value.progress === 'number' ? value.progress : null,
        total: typeof value.total === 'number' ? value.total : null,
        message: typeof value.message === 'string' ? value.message : '',
      }
      return {
        ...state,
        tools: updateByID(state.tools, value.toolCallId, 'tool call', (tool) => ({ ...tool, progress, status: 'running' })),
      }
    }
    case RUN_STATUS_NAME: {
      const value = event.value
      if (!isRecord(value) || value.status !== 'cancelling' || typeof value.runId !== 'string' ||
          (state.runID && value.runId !== state.runID) || typeof value.code !== 'string' || typeof value.message !== 'string') {
        throw new Error('invalid canonical run status payload')
      }
      return { ...state, status: 'cancelling', error: null }
    }
    case APPROVAL_NAME:
      return reduceApprovalAuthority(state, event.value)
    case A2UI_OPERATIONS_NAME: {
      const applied = applyA2UIOperations(state.surfaces, state.surfaceOrder, event.value)
      return { ...state, surfaces: applied.surfaces, surfaceOrder: applied.surfaceOrder }
    }
    default:
      return state
  }
}

function reduceApprovalAuthority(state, value) {
  if (!isRecord(value)) throw new Error('invalid canonical approval payload')
  const approvalID = requireCanonicalUUID('approvalId', value.approvalId)
  const executionID = requireCanonicalUUID('executionId', value.executionId)
  const runID = requireCanonicalUUID('runId', value.runId)
  const runAttemptID = requireCanonicalUUID('runAttemptId', value.runAttemptId)
  const nonce = requireCanonicalUUID('approval nonce', value.nonce)
  if (state.runID && runID !== state.runID) throw new Error('approval escaped the active run')
  if (!Number.isSafeInteger(value.runAttemptGeneration) || value.runAttemptGeneration < 1 ||
      !Number.isSafeInteger(value.version) || value.version < 1) {
    throw new Error('approval generation and version must be positive safe integers')
  }
  const toolName = requireText('approval toolName', value.toolName)
  if (toolName.length > 128) throw new Error('approval toolName exceeds 128 characters')
  const status = requireText('approval status', value.status)
  if (!['pending', 'approved', 'denied', 'expired', 'cancelled', 'consumed'].includes(status)) {
    throw new Error('approval status is unsupported')
  }
  const decision = value.decision === undefined ? '' : requireString('approval decision', value.decision)
  const approverID = value.approverId === undefined ? '' : requireString('approval approverId', value.approverId)
  validateApprovalOutcome(status, decision, approverID)
  if (!isRecord(value.contextDigest) ||
      !hasExactObjectKeys(value.contextDigest, ['domain', 'canonicalizerVersion', 'sha256']) ||
      value.contextDigest.domain !== 'approval-context' || value.contextDigest.canonicalizerVersion !== 'rfc8785-v1' ||
      typeof value.contextDigest.sha256 !== 'string' || !/^[0-9a-f]{64}$/u.test(value.contextDigest.sha256) ||
      /^0{64}$/u.test(value.contextDigest.sha256)) {
    throw new Error('approval contextDigest is invalid')
  }
  const expiresAt = requireText('approval expiresAt', value.expiresAt)
  if (expiresAt.length > 64 || Number.isNaN(Date.parse(expiresAt))) throw new Error('approval expiresAt is invalid')

  const approval = {
    approvalId: approvalID,
    executionId: executionID,
    runId: runID,
    runAttemptId: runAttemptID,
    runAttemptGeneration: value.runAttemptGeneration,
    nonce,
    contextDigest: cloneValue(value.contextDigest),
    toolName,
    status,
    decision,
    approverId: approverID,
    expiresAt,
    version: value.version,
  }
  const existing = state.approvals[approvalID]
  if (existing) {
    for (const field of ['executionId', 'runId', 'runAttemptId', 'runAttemptGeneration', 'nonce', 'toolName', 'expiresAt']) {
      if (existing[field] !== approval[field]) throw new Error(`approval ${approvalID} changed immutable ${field}`)
    }
    if (JSON.stringify(existing.contextDigest) !== JSON.stringify(approval.contextDigest)) {
      throw new Error(`approval ${approvalID} changed immutable contextDigest`)
    }
    if (approval.version < existing.version) throw new Error(`approval ${approvalID} version moved backwards`)
    if (approval.version === existing.version) {
      if (approval.status !== existing.status || approval.decision !== existing.decision || approval.approverId !== existing.approverId) {
        throw new Error(`approval ${approvalID} reused a version for different state`)
      }
      return state
    }
    if (!validApprovalTransition(existing.status, approval.status)) {
      throw new Error(`approval ${approvalID} has invalid ${existing.status} to ${approval.status} transition`)
    }
  }
  const approvals = { ...state.approvals, [approvalID]: approval }
  const approvalOrder = existing ? state.approvalOrder : [...state.approvalOrder, approvalID]
  return { ...state, approvals, approvalOrder }
}

function validateApprovalOutcome(status, decision, approverID) {
  if (status === 'pending') {
    if (decision !== '' || approverID !== '') throw new Error('pending approval contains decision evidence')
    return
  }
  if (status === 'approved' || status === 'consumed') {
    if (decision !== 'approve') throw new Error(`${status} approval must contain approve`)
    requireCanonicalUUID('approval approverId', approverID)
    return
  }
  if (status === 'denied') {
    if (decision !== 'deny') throw new Error('denied approval must contain deny')
    requireCanonicalUUID('approval approverId', approverID)
    return
  }
  if ((decision === '') !== (approverID === '')) throw new Error(`${status} approval has partial decision evidence`)
  if (decision !== '' && decision !== 'approve') throw new Error(`${status} approval can preserve only approve evidence`)
  if (approverID !== '') requireCanonicalUUID('approval approverId', approverID)
}

function validApprovalTransition(from, to) {
  if (from === 'pending') return ['approved', 'denied', 'expired', 'cancelled'].includes(to)
  if (from === 'approved') return ['consumed', 'expired', 'cancelled'].includes(to)
  return false
}

export function applyA2UIOperations(currentSurfaces, currentOrder, operations) {
  if (!isRecord(currentSurfaces) || !Array.isArray(currentOrder) || !Array.isArray(operations) || operations.length < 1) {
    throw new Error('A2UI operations must be a non-empty array')
  }
  const surfaces = cloneValue(currentSurfaces)
  const surfaceOrder = [...currentOrder]
  for (const [index, message] of operations.entries()) {
    if (!isRecord(message) || message.version !== A2UI_VERSION) throw new Error(`A2UI operation ${index} has an unsupported version`)
    const names = ['createSurface', 'updateComponents', 'updateDataModel', 'deleteSurface'].filter((name) => message[name] !== undefined)
    if (names.length !== 1 || !isRecord(message[names[0]])) throw new Error(`A2UI operation ${index} must contain exactly one operation`)
    const name = names[0]
    const operation = message[name]
    const surfaceID = safeObjectKey('A2UI surfaceId', operation.surfaceId)
    if (name === 'createSurface') {
      if (Object.prototype.hasOwnProperty.call(surfaces, surfaceID)) throw new Error(`A2UI surface ${surfaceID} was created twice`)
      if (operation.catalogId !== A2UI_BASIC_CATALOG || operation.sendDataModel === true) throw new Error('A2UI surface uses an unsupported catalog or data echo')
      surfaces[surfaceID] = { id: surfaceID, catalogID: operation.catalogId, components: [], dataModel: {} }
      surfaceOrder.push(surfaceID)
      continue
    }
    if (!Object.prototype.hasOwnProperty.call(surfaces, surfaceID)) throw new Error(`A2UI surface ${surfaceID} was updated before creation`)
    const surface = surfaces[surfaceID]
    if (name === 'deleteSurface') {
      delete surfaces[surfaceID]
      const orderIndex = surfaceOrder.indexOf(surfaceID)
      if (orderIndex >= 0) surfaceOrder.splice(orderIndex, 1)
    } else if (name === 'updateComponents') {
      surfaces[surfaceID] = { ...surface, components: validateComponents(operation.components) }
    } else {
      surfaces[surfaceID] = {
        ...surface,
        dataModel: updateDataModel(surface.dataModel, operation.path || '', operation.value),
      }
    }
  }
  return { surfaces, surfaceOrder }
}

function validateComponents(components) {
  if (!Array.isArray(components) || components.length < 1 || components.length > 512) {
    throw new Error('A2UI components must contain between 1 and 512 entries')
  }
  const copied = cloneValue(components)
  const byID = new Map()
  for (const component of copied) {
    if (!isRecord(component)) throw new Error('A2UI component must be an object')
    const id = requireText('A2UI component id', component.id)
    if (byID.has(id)) throw new Error(`A2UI component ${id} is duplicated`)
    if (!['Card', 'Column', 'Text'].includes(component.component)) throw new Error(`unsupported A2UI component ${component.component}`)
    byID.set(id, component)
  }
  if (!byID.has('root')) throw new Error('A2UI components require a root component')
  for (const component of copied) {
    const references = component.child ? [component.child] : (Array.isArray(component.children) ? component.children : [])
    for (const reference of references) {
      if (!byID.has(reference)) throw new Error(`A2UI component ${component.id} references an unknown child`)
    }
    if (component.component === 'Card' && (!component.child || references.length !== 1)) throw new Error('A2UI Card requires one child')
    if (component.component === 'Column' && (!Array.isArray(component.children) || component.children.length < 1)) throw new Error('A2UI Column requires children')
    if (component.component === 'Text' && component.text === undefined) throw new Error('A2UI Text requires text')
  }
  return copied
}

function updateDataModel(current, pointer, value) {
  if (value === undefined) throw new Error('A2UI updateDataModel requires a value')
  if (pointer === '') return cloneValue(value)
  if (!pointer.startsWith('/')) throw new Error('A2UI data path must be an absolute JSON Pointer')
  const next = cloneValue(current)
  const segments = decodePointer(pointer)
  let target = next
  for (let index = 0; index < segments.length - 1; index += 1) {
    const segment = segments[index]
    if (!isRecord(target[segment]) && !Array.isArray(target[segment])) target[segment] = {}
    target = target[segment]
  }
  target[segments[segments.length - 1]] = cloneValue(value)
  return next
}

export function resolveJSONPointer(value, pointer) {
  if (pointer === '') return value
  if (typeof pointer !== 'string' || !pointer.startsWith('/')) return undefined
  let current = value
  for (const segment of decodePointer(pointer)) {
    if ((isRecord(current) || Array.isArray(current)) && Object.prototype.hasOwnProperty.call(current, segment)) {
      current = current[segment]
    } else {
      return undefined
    }
  }
  return current
}

function decodePointer(pointer) {
  return pointer.slice(1).split('/').map((segment) => {
    if (/~(?:[^01]|$)/u.test(segment)) throw new Error('invalid JSON Pointer escape')
    return safeObjectKey('JSON Pointer segment', segment.replace(/~1/gu, '/').replace(/~0/gu, '~'))
  })
}

function updateByID(items, rawID, label, update) {
  const id = requireText(`${label} id`, rawID)
  let found = false
  const next = items.map((item) => {
    if (item.id !== id) return item
    found = true
    return update(item)
  })
  if (!found) throw new Error(`${label} ${id} was used before start`)
  return next
}

function appendDiagnostic(diagnostics, event) {
  const entry = {
    type: event.type,
    name: typeof event.name === 'string' ? event.name : '',
    timestamp: Number.isFinite(event.timestamp) ? event.timestamp : Date.now(),
  }
  const next = [...diagnostics, entry]
  return next.length > maximumDiagnostics ? next.slice(next.length - maximumDiagnostics) : next
}

function cloneValue(value) {
  if (typeof structuredClone === 'function') return structuredClone(value)
  return JSON.parse(JSON.stringify(value))
}

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function requireText(label, value) {
  if (typeof value !== 'string' || value.length < 1 || /[\0\r\n]/u.test(value)) throw new Error(`${label} must be non-empty bounded text`)
  return value
}

function requireCanonicalUUID(label, value) {
  validateScopeID(label, value)
  if (value === '00000000-0000-0000-0000-000000000000') throw new Error(`${label} must not be the zero UUID`)
  return value
}

function hasExactObjectKeys(value, expected) {
  const actual = Object.keys(value).sort()
  const wanted = [...expected].sort()
  return actual.length === wanted.length && actual.every((key, index) => key === wanted[index])
}

function safeObjectKey(label, value) {
  const key = requireText(label, value)
  if (key === '__proto__' || key === 'prototype' || key === 'constructor') {
    throw new Error(`${label} uses a forbidden object key`)
  }
  return key
}

function requireString(label, value) {
  if (typeof value !== 'string') throw new Error(`${label} must be text`)
  return value
}
