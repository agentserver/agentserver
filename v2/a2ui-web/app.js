import {
  EVENT_CURSOR_NAME,
  MAXIMUM_EVENT_STREAM_BYTES,
  SSEDecoder,
  appendUserMessage,
  buildRunEndpoint,
  buildRunRequest,
  cloneViewState,
  createViewState,
  isTerminalRunStatus,
  readFragmentConfiguration,
  reduceAGUIEvent,
  resolveJSONPointer,
  validateBearer,
  validateScopeID,
} from './protocol.js'

const developmentWorkspaceID = '40000000-0000-4000-8000-000000000004'
const developmentSessionID = '50000000-0000-4000-8000-000000000005'

const elements = {
  healthPill: requiredElement('health-pill'),
  healthLabel: requiredElement('health-label'),
  connectionButton: requiredElement('connection-button'),
  connectionLayer: requiredElement('connection-layer'),
  connectionForm: requiredElement('connection-form'),
  connectionError: requiredElement('connection-error'),
  bearer: requiredElement('bearer'),
  workspaceID: requiredElement('workspace-id'),
  sessionID: requiredElement('session-id'),
  runState: requiredElement('run-state'),
  runStateLabel: requiredElement('run-state-label'),
  cancelButton: requiredElement('cancel-button'),
  transcript: requiredElement('transcript'),
  welcomeCard: requiredElement('welcome-card'),
  streamWarning: requiredElement('stream-warning'),
  reconnectButton: requiredElement('reconnect-button'),
  composer: requiredElement('composer'),
  prompt: requiredElement('prompt'),
  composerHint: requiredElement('composer-hint'),
  sendButton: requiredElement('send-button'),
  workspaceFact: requiredElement('workspace-fact'),
  sessionFact: requiredElement('session-fact'),
  runFact: requiredElement('run-fact'),
  cursorFact: requiredElement('cursor-fact'),
  eventCount: requiredElement('event-count'),
  toolsEmpty: requiredElement('tools-empty'),
  toolList: requiredElement('tool-list'),
  surfacesEmpty: requiredElement('surfaces-empty'),
  surfaceList: requiredElement('surface-list'),
  diagnosticCount: requiredElement('diagnostic-count'),
  eventLog: requiredElement('event-log'),
}

const welcomeTemplate = elements.welcomeCard.cloneNode(true)
let connection = null
let viewState = createViewState()
let activeRun = null
let cancellationPending = false

initialize()

function initialize() {
  elements.workspaceID.value = developmentWorkspaceID
  elements.sessionID.value = developmentSessionID
  elements.connectionForm.addEventListener('submit', connectFromForm)
  elements.connectionButton.addEventListener('click', toggleConnection)
  elements.composer.addEventListener('submit', sendPrompt)
  elements.reconnectButton.addEventListener('click', () => streamRun(true))
  elements.cancelButton.addEventListener('click', cancelActiveRun)
  elements.prompt.addEventListener('input', renderComposer)
  window.addEventListener('online', checkGatewayHealth)
  window.addEventListener('offline', () => setHealth('offline', 'browser offline'))

  const fragment = readFragmentConfiguration(window.location.hash)
  if (window.location.hash) {
    history.replaceState(null, '', `${window.location.pathname}${window.location.search}`)
  }
  if (fragment.workspaceID) elements.workspaceID.value = fragment.workspaceID
  if (fragment.sessionID) elements.sessionID.value = fragment.sessionID
  if (fragment.token) {
    elements.bearer.value = fragment.token
    if (!connectFromValues()) showConnectionLayer()
  } else {
    showConnectionLayer()
  }
  renderAll()
  checkGatewayHealth()
  window.setInterval(checkGatewayHealth, 10_000)
}

function connectFromForm(event) {
  event.preventDefault()
  connectFromValues()
}

function connectFromValues() {
  try {
    const token = validateBearer(elements.bearer.value)
    const workspaceID = validateScopeID('workspace ID', elements.workspaceID.value.trim())
    const sessionID = validateScopeID('session ID', elements.sessionID.value.trim())
    connection = { token, workspaceID, sessionID }
    elements.bearer.value = ''
    elements.connectionError.hidden = true
    elements.connectionLayer.hidden = true
    elements.connectionButton.textContent = 'Disconnect'
    viewState = createViewState()
    activeRun = null
    cancellationPending = false
    renderAll()
    elements.prompt.focus()
    return true
  } catch (error) {
    elements.connectionError.textContent = safeErrorMessage(error)
    elements.connectionError.hidden = false
    return false
  }
}

function toggleConnection() {
  if (!connection) {
    showConnectionLayer()
    return
  }
  if (activeRun && !window.confirm('Disconnect this browser stream? The server-side run will continue and the in-memory cursor will be forgotten.')) {
    return
  }
  const previous = activeRun
  activeRun = null
  cancellationPending = false
  if (previous?.controller) previous.controller.abort()
  connection.token = ''
  connection = null
  viewState = createViewState()
  elements.connectionButton.textContent = 'Connect'
  elements.prompt.value = ''
  showConnectionLayer()
  renderAll()
}

function showConnectionLayer() {
  elements.connectionLayer.hidden = false
  elements.connectionError.hidden = true
  window.setTimeout(() => elements.bearer.focus(), 0)
}

function sendPrompt(event) {
  event.preventDefault()
  if (!connection || activeRun) return
  const prompt = elements.prompt.value.trim()
  if (!prompt) return
  const nonce = randomID()
  const messageID = `user-${nonce}`
  viewState = {
    ...viewState,
    status: 'connecting',
    runID: '',
    cursor: '',
    cursorSequence: 0,
    error: null,
  }
  viewState = appendUserMessage(viewState, messageID, prompt)
  activeRun = {
    idempotencyKey: `web-${nonce}`,
    clientRunID: `web-run-${nonce}`,
    messageID,
    prompt,
    cursor: '',
    checkpoint: cloneViewState(viewState),
    controller: null,
    reconnects: 0,
  }
  elements.prompt.value = ''
  renderAll()
  streamRun(false)
}

async function streamRun(reconnect) {
  if (!connection || !activeRun || activeRun.controller) return
  const run = activeRun
  if (reconnect) {
    viewState = cloneViewState(run.checkpoint)
    viewState = { ...viewState, status: 'connecting', error: null }
    run.reconnects += 1
  }
  const controller = new AbortController()
  run.controller = controller
  renderAll()
  try {
    const response = await fetch(buildRunEndpoint(connection.workspaceID, connection.sessionID), {
      method: 'POST',
      mode: 'same-origin',
      cache: 'no-store',
      credentials: 'omit',
      redirect: 'error',
      referrerPolicy: 'no-referrer',
      signal: controller.signal,
      headers: {
        Accept: 'text/event-stream',
        Authorization: `Bearer ${connection.token}`,
        'Content-Type': 'application/json',
        'Idempotency-Key': run.idempotencyKey,
      },
      body: JSON.stringify(buildRunRequest({
        sessionID: connection.sessionID,
        clientRunID: run.clientRunID,
        messageID: run.messageID,
        prompt: run.prompt,
        cursor: run.cursor,
      })),
    })
    if (!response.ok) throw await responseError(response)
    const contentType = response.headers.get('Content-Type') || ''
    if (contentType.split(';', 1)[0].trim().toLowerCase() !== 'text/event-stream') {
      throw new Error(`browser-gateway returned ${contentType || 'no Content-Type'} instead of text/event-stream`)
    }
    if (!response.body) throw new Error('browser does not expose the AG-UI response stream')

    const reader = response.body.getReader()
    const text = new TextDecoder('utf-8', { fatal: true })
    const decoder = new SSEDecoder(MAXIMUM_EVENT_STREAM_BYTES)
    let receivedBytes = 0
    while (true) {
      const result = await reader.read()
      if (result.done) break
      receivedBytes += result.value.byteLength
      if (receivedBytes > MAXIMUM_EVENT_STREAM_BYTES) throw new Error('AG-UI event stream exceeded the 16 MiB reference-client limit')
      for (const event of decoder.push(text.decode(result.value, { stream: true }))) applyServerEvent(run, event)
    }
    const finalText = text.decode()
    for (const event of decoder.push(finalText)) applyServerEvent(run, event)
    for (const event of decoder.finish()) applyServerEvent(run, event)
    if (!isTerminalRunStatus(viewState.status)) {
      throw new Error('AG-UI stream ended without a terminal event')
    }
    run.controller = null
    if (activeRun === run) activeRun = null
    cancellationPending = false
    renderAll()
  } catch (error) {
    run.controller = null
    if (activeRun !== run || controller.signal.aborted) return
    if (isTerminalRunStatus(viewState.status)) {
      activeRun = null
      cancellationPending = false
      renderAll()
      return
    }
    viewState = {
      ...viewState,
      status: 'disconnected',
      error: { code: 'stream_disconnected', message: safeErrorMessage(error) },
    }
    renderAll()
  }
}

async function cancelActiveRun() {
  if (!connection || !activeRun || !viewState.runID || cancellationPending) return
  if (!window.confirm('Cancel this server-side run? The projection stream will stay open until Core commits the terminal cancellation.')) return
  const run = activeRun
  const runID = viewState.runID
  const currentConnection = connection
  cancellationPending = true
  renderAll()
  try {
    const response = await fetch(`/v2/workspaces/${encodeURIComponent(currentConnection.workspaceID)}/runs/${encodeURIComponent(runID)}:cancel`, {
      method: 'POST',
      mode: 'same-origin',
      cache: 'no-store',
      credentials: 'omit',
      redirect: 'error',
      referrerPolicy: 'no-referrer',
      headers: { Accept: 'application/json', Authorization: `Bearer ${currentConnection.token}` },
    })
    if (!response.ok) throw await responseError(response)
    const result = await boundedJSONResponse(response, 64 * 1024)
    const validStatuses = new Set(['cancelling', 'completed', 'failed', 'interrupted', 'cancelled'])
    const terminalStatus = result && ['completed', 'failed', 'interrupted', 'cancelled'].includes(result.status)
    if (!result || result.workspaceId !== currentConnection.workspaceID || result.sessionId !== currentConnection.sessionID ||
        result.runId !== runID || !validStatuses.has(result.status) || typeof result.terminal !== 'boolean' ||
        result.terminal !== terminalStatus || typeof result.changed !== 'boolean' ||
        !Number.isSafeInteger(result.runVersion) || result.runVersion < 1 || result.runVersion >= Number.MAX_SAFE_INTEGER) {
      throw new Error('browser-gateway returned an invalid cancel result')
    }
    if (activeRun !== run) return
    if (result.status === 'cancelling') {
      viewState = { ...viewState, status: 'cancelling', error: null }
    } else if (result.terminal) {
      const status = result.status === 'completed' ? 'completed' : result.status === 'cancelled' ? 'cancelled' : 'failed'
      viewState = { ...viewState, status, error: status === 'failed' ? {
        code: `run_${result.status}`,
        message: `The run was already terminal with status ${result.status}.`,
      } : null }
    }
  } catch (error) {
    if (activeRun !== run) return
    viewState = {
      ...viewState,
      error: { code: 'cancel_failed', message: safeErrorMessage(error) },
    }
  } finally {
    if (activeRun === run) cancellationPending = false
    renderAll()
  }
}

function applyServerEvent(run, event) {
  if (activeRun !== run) return
  viewState = reduceAGUIEvent(viewState, event)
  if (event.type === 'CUSTOM' && event.name === EVENT_CURSOR_NAME) {
    run.cursor = viewState.cursor
    run.checkpoint = cloneViewState(viewState)
  }
  renderAll()
}

async function responseError(response) {
  let message = `browser-gateway returned HTTP ${response.status}`
  let code = 'http_error'
  try {
    const raw = await response.text()
    if (raw.length <= 64 * 1024) {
      const envelope = JSON.parse(raw)
      if (envelope && typeof envelope === 'object') {
        if (typeof envelope.code === 'string') code = envelope.code
        if (typeof envelope.message === 'string') message = envelope.message
        if (typeof envelope.currentRunId === 'string') message += ` (active run ${shortID(envelope.currentRunId)})`
      }
    }
  } catch {
    // Keep the bounded status-only error.
  }
  const error = new Error(`${code}: ${message}`)
  error.code = code
  return error
}

async function boundedJSONResponse(response, maximumBytes) {
  if (!response.body) throw new Error('browser does not expose the response body')
  const reader = response.body.getReader()
  const chunks = []
  let size = 0
  while (true) {
    const result = await reader.read()
    if (result.done) break
    size += result.value.byteLength
    if (size > maximumBytes) {
      await reader.cancel()
      throw new Error(`browser-gateway response exceeds ${maximumBytes} bytes`)
    }
    chunks.push(result.value)
  }
  const bytes = new Uint8Array(size)
  let offset = 0
  for (const chunk of chunks) {
    bytes.set(chunk, offset)
    offset += chunk.byteLength
  }
  return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes))
}

function renderAll() {
  renderStatus()
  renderTranscript()
  renderTools()
  renderSurfaces()
  renderDiagnostics()
  renderComposer()
}

function renderStatus() {
  const labels = {
    idle: 'Idle', connecting: 'Opening stream', running: 'Running', completed: 'Completed',
    failed: 'Failed', disconnected: 'Disconnected', cancelling: 'Cancelling', cancelled: 'Cancelled',
  }
  elements.runState.dataset.state = viewState.status
  elements.runStateLabel.textContent = labels[viewState.status] || viewState.status
  elements.workspaceFact.textContent = connection ? shortID(connection.workspaceID) : 'not connected'
  elements.workspaceFact.title = connection?.workspaceID || ''
  elements.sessionFact.textContent = connection ? shortID(connection.sessionID) : 'not connected'
  elements.sessionFact.title = connection?.sessionID || ''
  elements.runFact.textContent = viewState.runID ? shortID(viewState.runID) : '—'
  elements.runFact.title = viewState.runID
  elements.cursorFact.textContent = viewState.cursor ? `seq ${viewState.cursorSequence}` : '—'
  elements.cursorFact.title = viewState.cursor ? 'Opaque cursor held only in page memory' : ''
  elements.eventCount.textContent = String(viewState.eventCount)
  elements.streamWarning.hidden = viewState.status !== 'disconnected' || !activeRun
  const cancellable = Boolean(connection && activeRun && viewState.runID &&
    ['running', 'connecting', 'disconnected', 'cancelling'].includes(viewState.status))
  elements.cancelButton.hidden = !cancellable
  elements.cancelButton.disabled = cancellationPending || viewState.status === 'cancelling'
  elements.cancelButton.textContent = cancellationPending || viewState.status === 'cancelling' ? 'Cancelling…' : 'Cancel run'
}

function renderTranscript() {
  const nearBottom = elements.transcript.scrollHeight - elements.transcript.scrollTop - elements.transcript.clientHeight < 120
  const fragment = document.createDocumentFragment()
  if (viewState.messages.length === 0) {
    fragment.append(welcomeTemplate.cloneNode(true))
  } else {
    for (const message of viewState.messages) fragment.append(renderMessage(message))
    if (viewState.reasoning.length > 0) fragment.append(renderReasoning())
    if (viewState.error) fragment.append(renderRunError(viewState.error))
  }
  elements.transcript.replaceChildren(fragment)
  if (nearBottom) elements.transcript.scrollTop = elements.transcript.scrollHeight
}

function renderMessage(message) {
  const article = createElement('article', `message message-${message.role === 'user' ? 'user' : 'assistant'}`)
  const avatar = createElement('div', 'message-avatar', message.role === 'user' ? 'You' : 'A')
  const body = createElement('div', 'message-body')
  const meta = createElement('div', 'message-meta', message.role === 'user' ? 'You' : 'Agent')
  if (!message.complete) meta.append(createElement('span', 'typing-indicator', ' streaming'))
  const content = createElement('div', 'message-content', message.text || (message.complete ? '' : '…'))
  body.append(meta, content)
  article.append(avatar, body)
  return article
}

function renderReasoning() {
  const details = createElement('details', 'reasoning-card')
  const summary = createElement('summary', '', `Reasoning summaries · ${viewState.reasoning.length}`)
  const content = createElement('div', 'reasoning-content')
  for (const reasoning of viewState.reasoning) content.append(createElement('p', '', reasoning.text || '…'))
  details.append(summary, content)
  return details
}

function renderRunError(error) {
  const card = createElement('div', 'run-error-card')
  card.append(createElement('strong', '', error.code || 'run_error'), createElement('span', '', error.message || 'Run failed'))
  return card
}

function renderTools() {
  elements.toolsEmpty.hidden = viewState.tools.length !== 0
  const fragment = document.createDocumentFragment()
  for (const tool of viewState.tools) {
    const card = createElement('article', 'tool-card')
    const header = createElement('div', 'tool-header')
    header.append(createElement('strong', '', tool.name), createElement('span', `tool-status status-${tool.status}`, toolStatusLabel(tool.status)))
    card.append(header)
    if (tool.arguments) {
      const block = createElement('div', 'tool-block')
      block.append(createElement('small', '', 'Arguments'), createElement('pre', '', prettyJSON(tool.arguments)))
      card.append(block)
    }
    if (tool.progress) {
      const progress = createElement('div', 'tool-progress')
      if (Number.isFinite(tool.progress.value) && Number.isFinite(tool.progress.total) && tool.progress.total > 0) {
        const meter = document.createElement('progress')
        meter.max = tool.progress.total
        meter.value = Math.min(tool.progress.value, tool.progress.total)
        progress.append(meter)
      }
      if (tool.progress.message) progress.append(createElement('span', '', tool.progress.message))
      card.append(progress)
    }
    if (tool.result) {
      const block = createElement('div', 'tool-block result')
      block.append(createElement('small', '', 'Result'), createElement('pre', '', tool.result))
      card.append(block)
    }
    fragment.append(card)
  }
  elements.toolList.replaceChildren(fragment)
}

function renderSurfaces() {
  const visible = viewState.surfaceOrder.filter((surfaceID) => viewState.surfaces[surfaceID])
  elements.surfacesEmpty.hidden = visible.length !== 0
  const fragment = document.createDocumentFragment()
  for (const surfaceID of visible) {
    const surface = viewState.surfaces[surfaceID]
    const wrapper = createElement('article', 'a2ui-surface')
    const label = surfaceID.startsWith('file-change-') ? 'File changes' : surfaceID.startsWith('command-') ? 'Command result' : 'A2UI surface'
    wrapper.append(createElement('div', 'surface-kicker', label))
    try {
      wrapper.append(renderSurfaceComponent(surface, 'root', new Set(), 0))
    } catch (error) {
      wrapper.append(createElement('div', 'surface-error', safeErrorMessage(error)))
    }
    fragment.append(wrapper)
  }
  elements.surfaceList.replaceChildren(fragment)
}

function renderSurfaceComponent(surface, componentID, ancestors, depth) {
  if (depth > 32 || ancestors.has(componentID)) throw new Error('A2UI component graph contains a cycle or exceeds depth')
  const component = surface.components.find((candidate) => candidate.id === componentID)
  if (!component) throw new Error(`A2UI component ${componentID} is missing`)
  const nextAncestors = new Set(ancestors)
  nextAncestors.add(componentID)
  if (component.component === 'Card') {
    const card = createElement('div', 'a2ui-card')
    card.append(renderSurfaceComponent(surface, component.child, nextAncestors, depth + 1))
    return card
  }
  if (component.component === 'Column') {
    const column = createElement('div', 'a2ui-column')
    for (const child of component.children) column.append(renderSurfaceComponent(surface, child, nextAncestors, depth + 1))
    return column
  }
  if (component.component === 'Text') {
    const raw = component.text && typeof component.text === 'object' && typeof component.text.path === 'string'
      ? resolveJSONPointer(surface.dataModel, component.text.path)
      : component.text
    const text = raw === undefined || raw === null ? '' : typeof raw === 'string' ? raw : JSON.stringify(raw, null, 2)
    const codeLike = /(?:command|output|diff|file-\d+-)/u.test(component.id) || text.includes('\n')
    return createElement(codeLike ? 'pre' : 'p', `a2ui-text a2ui-${codeLike ? 'code' : 'copy'}`, text)
  }
  throw new Error(`unsupported A2UI component ${component.component}`)
}

function renderDiagnostics() {
  elements.diagnosticCount.textContent = `${viewState.eventCount} frame${viewState.eventCount === 1 ? '' : 's'}`
  const fragment = document.createDocumentFragment()
  for (const diagnostic of viewState.diagnostics.slice(-24).reverse()) {
    const item = document.createElement('li')
    const time = new Date(diagnostic.timestamp)
    item.append(
      createElement('time', '', Number.isNaN(time.valueOf()) ? '—' : time.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })),
      createElement('code', '', diagnostic.name ? `${diagnostic.type} · ${diagnostic.name}` : diagnostic.type),
    )
    fragment.append(item)
  }
  elements.eventLog.replaceChildren(fragment)
}

function renderComposer() {
  const busy = Boolean(activeRun)
  elements.prompt.disabled = !connection || busy
  elements.sendButton.disabled = !connection || busy || !elements.prompt.value.trim()
  if (!connection) {
    elements.composerHint.textContent = 'Connect an in-memory development bearer to begin.'
  } else if (viewState.status === 'disconnected') {
    elements.composerHint.textContent = 'Reconnect the existing stream before sending another prompt.'
  } else if (viewState.status === 'cancelling') {
    elements.composerHint.textContent = 'Core accepted explicit cancellation; waiting for bounded attempt cleanup.'
  } else if (busy) {
    elements.composerHint.textContent = `Streaming committed AG-UI events${activeRun.reconnects ? ` · reconnect ${activeRun.reconnects}` : ''}`
  } else {
    elements.composerHint.textContent = 'Enter sends a new run; Shift+Enter adds a line.'
  }
}

elements.prompt.addEventListener('keydown', (event) => {
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
    event.preventDefault()
    elements.composer.requestSubmit()
  }
})

async function checkGatewayHealth() {
  if (!navigator.onLine) {
    setHealth('offline', 'browser offline')
    return
  }
  try {
    const response = await fetch('/readyz', { cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer' })
    setHealth(response.ok ? 'ready' : 'offline', response.ok ? 'gateway ready' : `gateway ${response.status}`)
  } catch {
    setHealth('offline', 'gateway unavailable')
  }
}

function setHealth(state, label) {
  elements.healthPill.dataset.state = state
  elements.healthLabel.textContent = label
}

function toolStatusLabel(status) {
  return ({ arguments: 'preparing', running: 'running', 'awaiting-result': 'finishing', completed: 'complete' })[status] || status
}

function prettyJSON(raw) {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

function randomID() {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('')
}

function shortID(value) {
  if (!value) return '—'
  return value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value
}

function safeErrorMessage(error) {
  if (error instanceof Error && error.message) return error.message
  return String(error || 'unknown error')
}

function createElement(tag, className = '', text = '') {
  const element = document.createElement(tag)
  if (className) element.className = className
  if (text !== '') element.textContent = text
  return element
}

function requiredElement(id) {
  const element = document.getElementById(id)
  if (!element) throw new Error(`reference web is missing #${id}`)
  return element
}
