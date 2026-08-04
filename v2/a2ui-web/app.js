import {
  APPROVAL_NAME,
  EVENT_CURSOR_NAME,
  MAXIMUM_EVENT_STREAM_BYTES,
  SSEDecoder,
  appendUserMessage,
  buildRunEndpoint,
  buildRunRequest,
  cloneViewState,
  createViewState,
  isTerminalRunStatus,
  reduceAGUIEvent,
  resolveJSONPointer,
  validateBearer,
  validateScopeID,
} from './protocol.js'

import {
  buildTokenExchangeBody,
  consumeAuthorizationTransaction,
  createAuthorizationTransaction,
  readAuthorizationCallback,
  storeAuthorizationTransaction,
  validateAuthorizationConfig,
  validateTokenResponse,
} from './auth.js'

import {
  buildCreateGatewayRequest,
  createBrowserBinding,
  createGatewayCallbackChannel,
  validateBeginAuthorization,
  validateCompleteAuthorization,
  validateCreateGateway,
  validateDisableGateway,
  validateGatewayCallbackMessage,
  validateGatewayList,
  validateRevokeGrant,
  workspaceLLMGatewayActionPath,
  workspaceLLMGatewaysPath,
} from './llm-gateways.js'

import {
  archiveUserSessionPath,
  newSessionID,
  sessionTitleFromPrompt,
  userSessionPath,
  userSessionsPath,
  validateSessionTitle,
  validateUserSessionList,
  validateUserSessionMutation,
  validateUserSessionState,
} from './sessions.js'

const developmentWorkspaceID = '40000000-0000-4000-8000-000000000004'

const elements = {
  healthPill: requiredElement('health-pill'),
  healthLabel: requiredElement('health-label'),
  connectionButton: requiredElement('connection-button'),
  gatewayButton: requiredElement('gateway-button'),
  connectionLayer: requiredElement('connection-layer'),
  connectionForm: requiredElement('connection-form'),
  connectionError: requiredElement('connection-error'),
  loginButton: requiredElement('login-button'),
  workspaceID: requiredElement('workspace-id'),
  sessionTitle: requiredElement('session-title'),
  sessionRefresh: requiredElement('session-refresh'),
  newSessionButton: requiredElement('new-session-button'),
  sessionList: requiredElement('session-list'),
  sessionEmpty: requiredElement('session-empty'),
  sessionError: requiredElement('session-error'),
  sessionCount: requiredElement('session-count'),
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
  approvalsEmpty: requiredElement('approvals-empty'),
  approvalList: requiredElement('approval-list'),
  toolsEmpty: requiredElement('tools-empty'),
  toolList: requiredElement('tool-list'),
  surfacesEmpty: requiredElement('surfaces-empty'),
  surfaceList: requiredElement('surface-list'),
  diagnosticCount: requiredElement('diagnostic-count'),
  eventLog: requiredElement('event-log'),
  gatewayLayer: requiredElement('gateway-layer'),
  gatewayClose: requiredElement('gateway-close'),
  gatewayRefresh: requiredElement('gateway-refresh'),
  gatewayStatus: requiredElement('gateway-status'),
  gatewayError: requiredElement('gateway-error'),
  gatewayList: requiredElement('gateway-list'),
  gatewayEmpty: requiredElement('gateway-empty'),
  gatewayCreateForm: requiredElement('gateway-create-form'),
  gatewayCreateButton: requiredElement('gateway-create-button'),
  gatewayName: requiredElement('gateway-name'),
  gatewayModel: requiredElement('gateway-model'),
  gatewayResponsesURL: requiredElement('gateway-responses-url'),
  gatewayOIDCIssuer: requiredElement('gateway-oidc-issuer'),
  gatewayOIDCClientID: requiredElement('gateway-oidc-client-id'),
  gatewayOIDCScopes: requiredElement('gateway-oidc-scopes'),
  gatewayBearerType: requiredElement('gateway-bearer-type'),
  gatewayMakeDefault: requiredElement('gateway-make-default'),
  gatewayCallbackURI: requiredElement('gateway-callback-uri'),
}

const welcomeTemplate = elements.welcomeCard.cloneNode(true)
let connection = null
let authorizationConfig = null
let viewState = createViewState()
let activeRun = null
let cancellationPending = false
const approvalCommands = new Map()
let gatewayView = { phase: 'idle', gateways: [], error: '' }
let gatewayAuthorization = null
let sessionView = { phase: 'signed-out', sessions: [], selectedID: '', error: '' }
const gatewayCallbackChannel = createGatewayCallbackChannel(window.BroadcastChannel)

void initialize()

async function initialize() {
  let initialWorkspaceID = developmentWorkspaceID
  const requestedWorkspaceID = new URL(window.location.href).searchParams.get('workspace')
  if (requestedWorkspaceID) {
    try { initialWorkspaceID = validateScopeID('workspace ID', requestedWorkspaceID) } catch { /* keep the safe development default */ }
  }
  elements.workspaceID.value = initialWorkspaceID
  elements.gatewayCallbackURI.textContent = new URL('/auth/llm-gateway/callback', window.location.origin).href
  elements.connectionForm.addEventListener('submit', beginAuthorization)
  elements.connectionButton.addEventListener('click', toggleConnection)
  elements.gatewayButton.addEventListener('click', openGatewaySettings)
  elements.gatewayClose.addEventListener('click', closeGatewaySettings)
  elements.gatewayRefresh.addEventListener('click', loadWorkspaceGateways)
  elements.gatewayCreateForm.addEventListener('submit', createWorkspaceGateway)
  elements.gatewayLayer.addEventListener('click', (event) => {
    if (event.target === elements.gatewayLayer) closeGatewaySettings()
  })
  window.addEventListener('message', receiveWorkspaceGatewayAuthorizationWindowMessage)
  gatewayCallbackChannel?.addEventListener('message', receiveWorkspaceGatewayAuthorizationBroadcast)
  window.addEventListener('pagehide', () => gatewayCallbackChannel?.close(), { once: true })
  elements.composer.addEventListener('submit', sendPrompt)
  elements.newSessionButton.addEventListener('click', () => createAndSelectSession('New conversation'))
  elements.sessionRefresh.addEventListener('click', () => loadUserSessions(sessionView.selectedID))
  elements.reconnectButton.addEventListener('click', () => streamRun(true))
  elements.cancelButton.addEventListener('click', cancelActiveRun)
  elements.prompt.addEventListener('input', renderComposer)
  window.addEventListener('online', checkGatewayHealth)
  window.addEventListener('offline', () => setHealth('offline', 'browser offline'))

  if (window.location.hash) {
    history.replaceState(null, '', `${window.location.pathname}${window.location.search}`)
  }
  renderAll()
  checkGatewayHealth()
  window.setInterval(checkGatewayHealth, 10_000)

  let callback = null
  try {
    callback = readAuthorizationCallback(window.location.search)
  } catch (error) {
    history.replaceState(null, '', window.location.pathname)
    showConnectionLayer(error)
    return
  }
  if (callback) history.replaceState(null, '', window.location.pathname)
  try {
    authorizationConfig = await loadAuthorizationConfig()
    if (callback) {
      await completeAuthorization(callback)
    } else {
      showConnectionLayer()
    }
  } catch (error) {
    showConnectionLayer(error)
  }
}

async function loadAuthorizationConfig() {
  const response = await fetch('/auth/config', {
    method: 'GET', mode: 'same-origin', cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer',
    headers: { Accept: 'application/json' },
  })
  if (!response.ok) throw await responseError(response)
  return validateAuthorizationConfig(await boundedJSONResponse(response, 32 * 1024))
}

async function beginAuthorization(event) {
  event.preventDefault()
  if (!authorizationConfig || connection || elements.loginButton.disabled) return
  elements.loginButton.disabled = true
  elements.loginButton.textContent = 'Preparing PKCE…'
  elements.connectionError.hidden = true
  try {
    const workspaceID = validateScopeID('workspace ID', elements.workspaceID.value.trim())
    const generated = await createAuthorizationTransaction({
      config: authorizationConfig,
      origin: window.location.origin,
      workspaceID,
      cryptoAPI: window.crypto,
    })
    storeAuthorizationTransaction(window.sessionStorage, generated.transaction)
    window.location.assign(generated.authorizationURL)
  } catch (error) {
    elements.connectionError.textContent = safeErrorMessage(error)
    elements.connectionError.hidden = false
    elements.loginButton.disabled = false
    elements.loginButton.textContent = 'Continue with SSO →'
  }
}

async function completeAuthorization(callback) {
  const transaction = consumeAuthorizationTransaction(window.sessionStorage, authorizationConfig, callback)
  elements.workspaceID.value = validateScopeID('workspace ID', transaction.workspaceID)
  if (callback.error) {
    throw new Error(`identity provider returned ${callback.error}${callback.errorDescription ? `: ${callback.errorDescription}` : ''}`)
  }
  const tokenURL = new URL(authorizationConfig.tokenEndpoint, window.location.origin)
  const response = await fetch(tokenURL.href, {
    method: 'POST', mode: tokenURL.origin === window.location.origin ? 'same-origin' : 'cors',
    cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer',
    headers: { Accept: 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
    body: buildTokenExchangeBody(authorizationConfig, transaction, callback.code),
  })
  if (!response.ok) throw await responseError(response)
  const token = validateTokenResponse(await boundedJSONResponse(response, 128 * 1024), authorizationConfig.scopes)
  establishConnection(validateBearer(token.accessToken), transaction.workspaceID)
  await loadUserSessions()
}

function establishConnection(token, workspaceID) {
  clearGatewayAuthorization()
  connection = { token, workspaceID, sessionID: '' }
  elements.connectionError.hidden = true
  elements.connectionLayer.hidden = true
  elements.connectionButton.textContent = 'Disconnect'
  elements.gatewayButton.hidden = authorizationConfig.apiOrigin !== ''
  viewState = createViewState()
  activeRun = null
  cancellationPending = false
  approvalCommands.clear()
  gatewayView = { phase: 'idle', gateways: [], error: '' }
  sessionView = { phase: 'idle', sessions: [], selectedID: '', error: '' }
  renderAll()
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
  approvalCommands.clear()
  if (previous?.controller) previous.controller.abort()
  connection.token = ''
  connection = null
  clearGatewayAuthorization()
  closeGatewaySettings()
  gatewayView = { phase: 'idle', gateways: [], error: '' }
  sessionView = { phase: 'signed-out', sessions: [], selectedID: '', error: '' }
  elements.gatewayButton.hidden = true
  viewState = createViewState()
  elements.connectionButton.textContent = 'Sign in'
  elements.prompt.value = ''
  showConnectionLayer()
  renderAll()
}

async function loadUserSessions(preferredSessionID = '') {
  if (!connection || sessionView.phase === 'loading') return
  const currentConnection = connection
  sessionView = { ...sessionView, phase: 'loading', error: '' }
  renderSessions()
  try {
    const response = await sessionAPIRequest(userSessionsPath(currentConnection.workspaceID), currentConnection, { method: 'GET' })
    if (!response.ok) throw await responseError(response)
    const sessions = [...validateUserSessionList(await boundedJSONResponse(response, 1024 * 1024), currentConnection.workspaceID)]
    if (connection !== currentConnection) return
    const requested = preferredSessionID || currentConnection.sessionID
    const selected = sessions.find((session) => session.sessionId === requested) || sessions[0] || null
    sessionView = { phase: 'ready', sessions, selectedID: selected?.sessionId || '', error: '' }
    currentConnection.sessionID = selected?.sessionId || ''
    resetConversationView()
    if (selected) elements.prompt.focus()
  } catch (error) {
    if (connection !== currentConnection) return
    sessionView = { ...sessionView, phase: 'error', error: safeErrorMessage(error) }
  }
  renderAll()
}

async function createAndSelectSession(title, throwOnError = false) {
  if (!connection || activeRun || ['loading', 'creating', 'archiving', 'renaming'].includes(sessionView.phase)) return null
  const currentConnection = connection
  const sessionID = newSessionID(window.crypto)
  const canonicalTitle = validateSessionTitle(title)
  sessionView = { ...sessionView, phase: 'creating', error: '' }
  renderSessions()
  try {
    const response = await sessionAPIRequest(userSessionsPath(currentConnection.workspaceID), currentConnection, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId: sessionID, title: canonicalTitle }),
    })
    if (!response.ok) throw await responseError(response)
    const result = validateUserSessionMutation(
      await boundedJSONResponse(response, 256 * 1024), currentConnection.workspaceID, sessionID, 'created',
    )
    if (connection !== currentConnection) return null
    const sessions = [result.session, ...sessionView.sessions.filter((session) => session.sessionId !== sessionID)]
    sessionView = { phase: 'ready', sessions, selectedID: sessionID, error: '' }
    currentConnection.sessionID = sessionID
    resetConversationView()
    renderAll()
    elements.prompt.focus()
    return result.session
  } catch (error) {
    if (connection === currentConnection) {
      sessionView = { ...sessionView, phase: 'error', error: safeErrorMessage(error) }
      renderAll()
    }
    if (throwOnError) throw error
    return null
  }
}

async function refreshUserSessionMetadata(sessionID) {
  if (!connection) return
  const currentConnection = connection
  try {
    const response = await sessionAPIRequest(
      userSessionPath(currentConnection.workspaceID, sessionID), currentConnection, { method: 'GET' },
    )
    if (!response.ok) throw await responseError(response)
    const session = validateUserSessionState(
      await boundedJSONResponse(response, 256 * 1024), currentConnection.workspaceID, sessionID,
    )
    if (connection !== currentConnection) return
    const remaining = sessionView.sessions.filter((item) => item.sessionId !== sessionID)
    const sessions = session.status === 'active' ? [session, ...remaining] : remaining
    const selectedID = session.status === 'active' ? currentConnection.sessionID
      : currentConnection.sessionID === sessionID ? sessions[0]?.sessionId || '' : currentConnection.sessionID
    currentConnection.sessionID = selectedID
    sessionView = { phase: 'ready', sessions, selectedID, error: '' }
  } catch (error) {
    if (connection !== currentConnection) return
    sessionView = { ...sessionView, phase: 'error', error: `Conversation metadata refresh failed: ${safeErrorMessage(error)}` }
  }
  renderSessions()
  renderStatus()
}

function selectUserSession(sessionID) {
  if (!connection || sessionID === connection.sessionID) return
  const selected = sessionView.sessions.find((session) => session.sessionId === sessionID)
  if (!selected) return
  if (activeRun && !window.confirm('Switch conversations? The current server-side run will continue, but this page will stop following its stream.')) return
  if (activeRun?.controller) activeRun.controller.abort()
  activeRun = null
  cancellationPending = false
  approvalCommands.clear()
  connection.sessionID = selected.sessionId
  sessionView = { ...sessionView, selectedID: selected.sessionId, error: '' }
  resetConversationView()
  renderAll()
  elements.prompt.focus()
}

async function renameUserSession(session) {
  if (!connection || activeRun || sessionView.phase !== 'ready') return
  const proposed = window.prompt('Rename conversation', session.title)
  if (proposed === null) return
  let title
  try {
    title = validateSessionTitle(proposed.trim())
  } catch (error) {
    sessionView = { ...sessionView, error: safeErrorMessage(error) }
    renderSessions()
    return
  }
  const currentConnection = connection
  sessionView = { ...sessionView, phase: 'renaming', error: '' }
  renderSessions()
  try {
    const response = await sessionAPIRequest(userSessionPath(currentConnection.workspaceID, session.sessionId), currentConnection, {
      method: 'PATCH', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title, expectedVersion: session.version }),
    })
    if (!response.ok) throw await responseError(response)
    const result = validateUserSessionMutation(
      await boundedJSONResponse(response, 256 * 1024), currentConnection.workspaceID, session.sessionId, 'changed',
    )
    if (connection !== currentConnection) return
    sessionView = {
      ...sessionView, phase: 'ready', error: '',
      sessions: sessionView.sessions.map((item) => item.sessionId === result.session.sessionId ? result.session : item),
    }
  } catch (error) {
    if (connection !== currentConnection) return
    sessionView = { ...sessionView, phase: 'error', error: safeErrorMessage(error) }
  }
  renderAll()
}

async function archiveUserSession(session) {
  if (!connection || activeRun || sessionView.phase !== 'ready' ||
      !window.confirm(`Archive “${session.title}”? Its durable run history will be retained.`)) return
  const currentConnection = connection
  sessionView = { ...sessionView, phase: 'archiving', error: '' }
  renderSessions()
  try {
    const response = await sessionAPIRequest(archiveUserSessionPath(currentConnection.workspaceID, session.sessionId), currentConnection, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ expectedVersion: session.version }),
    })
    if (!response.ok) throw await responseError(response)
    const result = validateUserSessionMutation(
      await boundedJSONResponse(response, 256 * 1024), currentConnection.workspaceID, session.sessionId, 'changed',
    )
    if (connection !== currentConnection || result.session.status !== 'archived') return
    const sessions = sessionView.sessions.filter((item) => item.sessionId !== session.sessionId)
    const selected = currentConnection.sessionID === session.sessionId ? sessions[0] || null
      : sessions.find((item) => item.sessionId === currentConnection.sessionID) || null
    currentConnection.sessionID = selected?.sessionId || ''
    sessionView = { phase: 'ready', sessions, selectedID: selected?.sessionId || '', error: '' }
    resetConversationView()
  } catch (error) {
    if (connection !== currentConnection) return
    sessionView = { ...sessionView, phase: 'error', error: safeErrorMessage(error) }
  }
  renderAll()
}

function resetConversationView() {
  viewState = createViewState()
  activeRun = null
  cancellationPending = false
  approvalCommands.clear()
}

function sessionAPIRequest(path, currentConnection, options) {
  return fetch(browserAPIEndpoint(path), {
    mode: 'cors', cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer',
    ...options,
    headers: { Accept: 'application/json', Authorization: `Bearer ${currentConnection.token}`, ...(options.headers || {}) },
  })
}

function openGatewaySettings() {
  if (!connection) return
  elements.gatewayLayer.hidden = false
  renderGatewaySettings()
  void loadWorkspaceGateways()
}

function closeGatewaySettings() {
  elements.gatewayLayer.hidden = true
}

async function loadWorkspaceGateways() {
  if (!connection || gatewayView.phase === 'loading') return
  const currentConnection = connection
  gatewayView = { ...gatewayView, phase: 'loading', error: '' }
  renderGatewaySettings()
  try {
    const response = await gatewayAPIRequest(workspaceLLMGatewaysPath(currentConnection.workspaceID), currentConnection, { method: 'GET' })
    if (!response.ok) throw await responseError(response)
    const gateways = validateGatewayList(await boundedJSONResponse(response, 512 * 1024), currentConnection.workspaceID)
    if (connection !== currentConnection) return
    gatewayView = { phase: 'ready', gateways, error: '' }
  } catch (error) {
    if (connection !== currentConnection) return
    gatewayView = { ...gatewayView, phase: 'error', error: safeErrorMessage(error) }
  }
  renderGatewaySettings()
}

async function createWorkspaceGateway(event) {
  event.preventDefault()
  if (!connection || gatewayView.phase === 'creating') return
  const currentConnection = connection
  gatewayView = { ...gatewayView, phase: 'creating', error: '' }
  renderGatewaySettings()
  try {
    const request = buildCreateGatewayRequest({
      name: elements.gatewayName.value.trim(),
      responsesUrl: elements.gatewayResponsesURL.value.trim(),
      oidcIssuer: elements.gatewayOIDCIssuer.value.trim(),
      oidcClientId: elements.gatewayOIDCClientID.value.trim(),
      oidcScopes: elements.gatewayOIDCScopes.value.trim(),
      bearerTokenType: elements.gatewayBearerType.value,
      defaultModel: elements.gatewayModel.value.trim(),
      makeDefault: elements.gatewayMakeDefault.checked,
    }, window.crypto)
    const response = await gatewayAPIRequest(workspaceLLMGatewaysPath(currentConnection.workspaceID), currentConnection, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(request),
    })
    if (!response.ok) throw await responseError(response)
    validateCreateGateway(await boundedJSONResponse(response, 512 * 1024), currentConnection.workspaceID)
    if (connection !== currentConnection) return
    elements.gatewayCreateForm.reset()
    elements.gatewayOIDCScopes.value = 'openid offline_access'
    elements.gatewayMakeDefault.checked = true
    gatewayView = { ...gatewayView, phase: 'idle', error: '' }
    await loadWorkspaceGateways()
  } catch (error) {
    if (connection !== currentConnection) return
    gatewayView = { ...gatewayView, phase: 'error', error: safeErrorMessage(error) }
    renderGatewaySettings()
  }
}

async function beginWorkspaceGatewayAuthorization(gateway) {
  if (!connection || gatewayAuthorization) return
  const currentConnection = connection
  const popup = window.open('about:blank', `agentserver-llm-gateway-${gateway.gatewayId}`, 'popup,width=560,height=760')
  if (!popup) {
    gatewayView = { ...gatewayView, phase: 'error', error: 'The browser blocked the OIDC authorization popup.' }
    renderGatewaySettings()
    return
  }
  const transaction = {
    popup,
    connection: currentConnection,
    workspaceID: currentConnection.workspaceID,
    gatewayID: gateway.gatewayId,
    browserBinding: createBrowserBinding(window.crypto),
    callbackState: '',
    expiresAtMS: 0,
    monitorID: null,
  }
  gatewayAuthorization = transaction
  gatewayView = { ...gatewayView, phase: 'authorizing', error: '' }
  renderGatewaySettings()
  try {
    const response = await gatewayAPIRequest(
      workspaceLLMGatewayActionPath(transaction.workspaceID, transaction.gatewayID, 'authorize'),
      currentConnection,
      { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ browserBinding: transaction.browserBinding }) },
    )
    if (!response.ok) throw await responseError(response)
    const authorization = validateBeginAuthorization(await boundedJSONResponse(response, 128 * 1024), transaction.gatewayID)
    if (gatewayAuthorization !== transaction || connection !== currentConnection || popup.closed) throw new Error('Gateway authorization popup was closed or superseded')
    transaction.callbackState = authorization.callbackState
    transaction.expiresAtMS = Date.parse(authorization.expiresAt)
    popup.location.replace(authorization.authorizationUrl)
    transaction.monitorID = window.setInterval(() => monitorWorkspaceGatewayAuthorization(transaction), 500)
  } catch (error) {
    if (gatewayAuthorization === transaction) clearGatewayAuthorization()
    if (connection === currentConnection) {
      gatewayView = { ...gatewayView, phase: 'error', error: safeErrorMessage(error) }
      renderGatewaySettings()
    }
  }
}

function receiveWorkspaceGatewayAuthorizationWindowMessage(event) {
  const transaction = gatewayAuthorization
  if (!transaction || event.origin !== window.location.origin || event.source !== transaction.popup) return
  void completeWorkspaceGatewayAuthorization(event.data)
}

function receiveWorkspaceGatewayAuthorizationBroadcast(event) {
  if (!gatewayAuthorization) return
  void completeWorkspaceGatewayAuthorization(event.data)
}

async function completeWorkspaceGatewayAuthorization(payload) {
  const transaction = gatewayAuthorization
  if (!transaction) return
  let callback
  try {
    callback = validateGatewayCallbackMessage(payload)
  } catch {
    return
  }
  if (callback.state !== transaction.callbackState) return
  gatewayAuthorization = null
  stopGatewayAuthorizationMonitor(transaction)
  try {
    if (transaction.popup && !transaction.popup.closed) transaction.popup.close()
    if (connection !== transaction.connection) throw new Error('AgentServer session changed during Gateway authorization')
    gatewayView = { ...gatewayView, phase: 'completing', error: '' }
    renderGatewaySettings()
    const response = await gatewayAPIRequest(
      workspaceLLMGatewayActionPath(transaction.workspaceID, transaction.gatewayID, 'completeAuthorization'),
      transaction.connection,
      {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          state: callback.state,
          code: callback.code || undefined,
          providerError: callback.providerError || undefined,
          browserBinding: transaction.browserBinding,
        }),
      },
    )
    if (!response.ok) {
      const failure = await responseError(response)
      if (callback.providerErrorDescription) failure.message += `: ${callback.providerErrorDescription}`
      throw failure
    }
    validateCompleteAuthorization(await boundedJSONResponse(response, 128 * 1024), transaction.gatewayID)
    gatewayView = { ...gatewayView, phase: 'idle', error: '' }
    await loadWorkspaceGateways()
  } catch (error) {
    if (transaction.popup && !transaction.popup.closed) transaction.popup.close()
    if (connection === transaction.connection) {
      gatewayView = { ...gatewayView, phase: 'error', error: safeErrorMessage(error) }
      renderGatewaySettings()
    }
  } finally {
    transaction.browserBinding = ''
    transaction.callbackState = ''
  }
}

async function revokeWorkspaceGatewayGrant(gateway) {
  if (!connection || gatewayAuthorization || !window.confirm(`Revoke your authorization for ${gateway.name}? Active runs using it will fail closed.`)) return
  const currentConnection = connection
  gatewayView = { ...gatewayView, phase: 'revoking', error: '' }
  renderGatewaySettings()
  try {
    const response = await gatewayAPIRequest(
      workspaceLLMGatewayActionPath(currentConnection.workspaceID, gateway.gatewayId, 'revoke'),
      currentConnection,
      { method: 'POST' },
    )
    if (!response.ok) throw await responseError(response)
    validateRevokeGrant(await boundedJSONResponse(response, 128 * 1024), gateway.gatewayId)
    if (connection !== currentConnection) return
    gatewayView = { ...gatewayView, phase: 'idle', error: '' }
    await loadWorkspaceGateways()
  } catch (error) {
    if (connection !== currentConnection) return
    gatewayView = { ...gatewayView, phase: 'error', error: safeErrorMessage(error) }
    renderGatewaySettings()
  }
}

async function disableWorkspaceGateway(gateway) {
  if (!connection || gatewayAuthorization || gateway.status !== 'active' ||
      !window.confirm(`Disable ${gateway.name}? Existing runs bound to v${gateway.version} will fail closed, and this cannot be undone in the current version.`)) return
  const currentConnection = connection
  gatewayView = { ...gatewayView, phase: 'disabling', error: '' }
  renderGatewaySettings()
  try {
    const response = await gatewayAPIRequest(
      workspaceLLMGatewayActionPath(currentConnection.workspaceID, gateway.gatewayId, 'disable'),
      currentConnection,
      { method: 'POST' },
    )
    if (!response.ok) throw await responseError(response)
    validateDisableGateway(await boundedJSONResponse(response, 128 * 1024), gateway.gatewayId)
    if (connection !== currentConnection) return
    gatewayView = { ...gatewayView, phase: 'idle', error: '' }
    await loadWorkspaceGateways()
  } catch (error) {
    if (connection !== currentConnection) return
    gatewayView = { ...gatewayView, phase: 'error', error: safeErrorMessage(error) }
    renderGatewaySettings()
  }
}

function monitorWorkspaceGatewayAuthorization(transaction) {
  if (gatewayAuthorization !== transaction) {
    stopGatewayAuthorizationMonitor(transaction)
    return
  }
  const sessionChanged = connection !== transaction.connection
  const expired = !Number.isSafeInteger(transaction.expiresAtMS) || Date.now() >= transaction.expiresAtMS
  // A provider's Cross-Origin-Opener-Policy can sever the WindowProxy and make
  // an open popup look closed. Keep the in-memory PKCE transaction until its
  // authoritative expiry so the same-origin callback broadcast can complete.
  if (!sessionChanged && !expired) return
  clearGatewayAuthorization()
  if (sessionChanged) return
  gatewayView = {
    ...gatewayView,
    phase: 'error',
    error: 'The third-party Gateway authorization transaction expired.',
  }
  renderGatewaySettings()
}

function stopGatewayAuthorizationMonitor(transaction) {
  if (!transaction || transaction.monitorID === null) return
  window.clearInterval(transaction.monitorID)
  transaction.monitorID = null
}

function clearGatewayAuthorization() {
  const transaction = gatewayAuthorization
  gatewayAuthorization = null
  if (!transaction) return
  stopGatewayAuthorizationMonitor(transaction)
  transaction.browserBinding = ''
  transaction.callbackState = ''
  if (transaction.popup && !transaction.popup.closed) transaction.popup.close()
}

function gatewayAPIRequest(path, currentConnection, options) {
  return fetch(browserAPIEndpoint(path), {
    mode: 'cors', cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer',
    ...options,
    headers: { Accept: 'application/json', Authorization: `Bearer ${currentConnection.token}`, ...(options.headers || {}) },
  })
}

function renderGatewaySettings() {
  const busy = ['loading', 'creating', 'authorizing', 'completing', 'revoking', 'disabling'].includes(gatewayView.phase)
  const labels = {
    loading: 'Loading workspace Gateways…', creating: 'Creating and verifying OIDC discovery…',
    authorizing: 'Waiting for third-party authorization…', completing: 'Verifying and sealing your OIDC grant…',
    revoking: 'Revoking your grant…', disabling: 'Disabling the workspace Gateway and fencing bound runs…',
    ready: `${gatewayView.gateways.length} Gateway${gatewayView.gateways.length === 1 ? '' : 's'}`,
    error: 'Gateway operation failed', idle: 'Workspace Gateway configuration',
  }
  elements.gatewayStatus.textContent = labels[gatewayView.phase] || gatewayView.phase
  elements.gatewayRefresh.disabled = busy
  elements.gatewayCreateButton.disabled = busy
  elements.gatewayCreateButton.textContent = gatewayView.phase === 'creating' ? 'Creating…' : 'Create Gateway'
  elements.gatewayError.hidden = !gatewayView.error
  elements.gatewayError.textContent = gatewayView.error
  elements.gatewayEmpty.hidden = gatewayView.phase === 'loading' || gatewayView.gateways.length !== 0
  const fragment = document.createDocumentFragment()
  for (const gateway of gatewayView.gateways) {
    const card = createElement('article', 'gateway-item')
    const heading = createElement('div', 'gateway-item-heading')
    const title = createElement('div')
    title.append(createElement('strong', '', gateway.name), createElement('span', '', `${gateway.defaultModel} · v${gateway.version}`))
    const badges = createElement('div', 'gateway-badges')
    if (gateway.default) badges.append(createElement('span', 'gateway-badge gateway-default', 'default'))
    if (gateway.status === 'disabled') badges.append(createElement('span', 'gateway-badge gateway-disabled', 'disabled'))
    badges.append(createElement('span', `gateway-badge gateway-${gateway.grantStatus || 'unlinked'}`, gateway.grantStatus || 'not authorized'))
    heading.append(title, badges)
    const facts = createElement('dl', 'gateway-facts')
    for (const [label, value] of [['Responses', gateway.responsesUrl], ['Issuer', gateway.oidcIssuer], ['Bearer', gateway.bearerTokenType]]) {
      const row = document.createElement('div')
      row.append(createElement('dt', '', label), createElement('dd', '', value))
      facts.append(row)
    }
    const actions = createElement('div', 'gateway-actions')
    const authorize = createElement('button', 'secondary-button', gateway.grantStatus === 'active' ? 'Reauthorize' : 'Authorize')
    authorize.type = 'button'
    authorize.disabled = busy || gateway.status !== 'active'
    authorize.addEventListener('click', () => beginWorkspaceGatewayAuthorization(gateway))
    actions.append(authorize)
    if (gateway.grantStatus) {
      const revoke = createElement('button', 'gateway-revoke', 'Revoke my grant')
      revoke.type = 'button'
      revoke.disabled = busy || gateway.grantStatus === 'revoked'
      revoke.addEventListener('click', () => revokeWorkspaceGatewayGrant(gateway))
      actions.append(revoke)
    }
    if (gateway.status === 'active') {
      const disable = createElement('button', 'gateway-disable', 'Disable Gateway')
      disable.type = 'button'
      disable.disabled = busy
      disable.addEventListener('click', () => disableWorkspaceGateway(gateway))
      actions.append(disable)
    }
    card.append(heading, facts, actions)
    fragment.append(card)
  }
  elements.gatewayList.replaceChildren(fragment)
}

function showConnectionLayer(error = null) {
  elements.connectionLayer.hidden = false
  elements.loginButton.disabled = !authorizationConfig
  elements.loginButton.textContent = authorizationConfig ? 'Continue with SSO →' : 'Authorization unavailable'
  if (error) {
    elements.connectionError.textContent = safeErrorMessage(error)
    elements.connectionError.hidden = false
  } else {
    elements.connectionError.hidden = true
  }
  window.setTimeout(() => (authorizationConfig ? elements.loginButton : elements.workspaceID).focus(), 0)
}

async function sendPrompt(event) {
  event.preventDefault()
  if (!connection || activeRun) return
  const prompt = elements.prompt.value.trim()
  if (!prompt) return
  if (!connection.sessionID) {
    try {
      const created = await createAndSelectSession(sessionTitleFromPrompt(prompt), true)
      if (!created || !connection?.sessionID) return
    } catch {
      return
    }
  }
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
  approvalCommands.clear()
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
    const response = await fetch(browserAPIEndpoint(buildRunEndpoint(connection.workspaceID, connection.sessionID)), {
      method: 'POST',
      mode: 'cors',
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
    await refreshUserSessionMetadata(connection.sessionID)
  } catch (error) {
    run.controller = null
    if (activeRun !== run || controller.signal.aborted) return
    if (isTerminalRunStatus(viewState.status)) {
      activeRun = null
      cancellationPending = false
      renderAll()
      await refreshUserSessionMetadata(connection.sessionID)
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
    const response = await fetch(browserAPIEndpoint(`/v2/workspaces/${encodeURIComponent(currentConnection.workspaceID)}/runs/${encodeURIComponent(runID)}:cancel`), {
      method: 'POST',
      mode: 'cors',
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

async function decideApproval(approval, decision) {
  if (!connection || approval.status !== 'pending' || approvalCommands.get(approval.approvalId)?.phase === 'sending' ||
      approvalCommands.get(approval.approvalId)?.phase === 'committed') return
  const currentConnection = connection
  approvalCommands.set(approval.approvalId, { phase: 'sending', decision, message: '' })
  renderAll()
  try {
    const response = await fetch(browserAPIEndpoint(`/v2/workspaces/${encodeURIComponent(currentConnection.workspaceID)}/approvals/${encodeURIComponent(approval.approvalId)}:decide`), {
      method: 'POST',
      mode: 'cors',
      cache: 'no-store',
      credentials: 'omit',
      redirect: 'error',
      referrerPolicy: 'no-referrer',
      headers: {
        Accept: 'application/json',
        Authorization: `Bearer ${currentConnection.token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        decision,
        nonce: approval.nonce,
        contextDigest: approval.contextDigest,
        expectedApprovalVersion: approval.version,
      }),
    })
    if (!response.ok) throw await responseError(response)
    const result = await boundedJSONResponse(response, 128 * 1024)
    validateApprovalDecisionResponse(result, approval, decision, currentConnection.workspaceID)
    if (connection !== currentConnection) return
    const canonical = viewState.approvals[approval.approvalId]
    if (canonical?.status === 'pending') {
      const label = result.approval.status === 'expired'
        ? 'Core committed expiry; awaiting canonical event.'
        : `Core committed ${result.approval.status}; awaiting canonical event.`
      approvalCommands.set(approval.approvalId, { phase: 'committed', decision, message: label })
    } else {
      approvalCommands.delete(approval.approvalId)
    }
  } catch (error) {
    if (connection !== currentConnection) return
    approvalCommands.set(approval.approvalId, { phase: 'error', decision, message: safeErrorMessage(error) })
  } finally {
    if (connection === currentConnection) renderAll()
  }
}

function validateApprovalDecisionResponse(result, expected, decision, workspaceID) {
  const approval = result?.approval
  const statuses = new Set(['approved', 'denied', 'expired', 'consumed'])
  const executionStatuses = new Set(['pending_approval', 'approved', 'denied', 'expired'])
  if (!result || typeof result !== 'object' || result.workspaceId !== workspaceID ||
      result.executionId !== expected.executionId || !executionStatuses.has(result.executionStatus) ||
      !Number.isSafeInteger(result.executionVersion) || result.executionVersion < 1 || typeof result.changed !== 'boolean' ||
      !approval || typeof approval !== 'object' || approval.approvalId !== expected.approvalId ||
      approval.executionId !== expected.executionId || approval.runId !== expected.runId ||
      approval.runAttemptId !== expected.runAttemptId || approval.runAttemptGeneration !== expected.runAttemptGeneration ||
      approval.nonce !== expected.nonce || approval.expiresAt !== expected.expiresAt ||
      !sameApprovalDigest(approval.contextDigest, expected.contextDigest) ||
      !statuses.has(approval.status) || !Number.isSafeInteger(approval.version) || approval.version < expected.version) {
    throw new Error('browser-gateway returned an invalid approval decision result')
  }
  const expectedOutcome = (decision === 'approve' && ['approved', 'consumed'].includes(approval.status) && approval.decision === 'approve') ||
    (decision === 'deny' && approval.status === 'denied' && approval.decision === 'deny') ||
    (approval.status === 'expired' && !approval.decision)
  if (!expectedOutcome) throw new Error('approval decision result contradicted the requested decision')
}

function sameApprovalDigest(left, right) {
  return left && right && left.domain === right.domain &&
    left.canonicalizerVersion === right.canonicalizerVersion && left.sha256 === right.sha256
}

function applyServerEvent(run, event) {
  if (activeRun !== run) return
  viewState = reduceAGUIEvent(viewState, event)
  if (event.type === 'CUSTOM' && event.name === APPROVAL_NAME && event.value?.status !== 'pending') {
    approvalCommands.delete(event.value.approvalId)
  }
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
        const detail = envelope.error && typeof envelope.error === 'object' ? envelope.error : envelope
        if (typeof detail.code === 'string') code = detail.code
        if (typeof detail.message === 'string') message = detail.message
        if (typeof detail.currentRunId === 'string') message += ` (active run ${shortID(detail.currentRunId)})`
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
  renderSessions()
  renderStatus()
  renderTranscript()
  renderApprovals()
  renderTools()
  renderSurfaces()
  renderDiagnostics()
  renderComposer()
}

function renderSessions() {
  const connected = Boolean(connection)
  const busy = ['loading', 'creating', 'renaming', 'archiving'].includes(sessionView.phase)
  elements.newSessionButton.disabled = !connected || busy || Boolean(activeRun)
  elements.newSessionButton.textContent = sessionView.phase === 'creating' ? 'Creating…' : '＋ New conversation'
  elements.sessionRefresh.disabled = !connected || busy
  elements.sessionError.hidden = !sessionView.error
  elements.sessionError.textContent = sessionView.error
  elements.sessionEmpty.hidden = !connected || sessionView.phase === 'loading' || sessionView.sessions.length !== 0
  const count = sessionView.sessions.length
  elements.sessionCount.textContent = !connected ? 'Sign in to load history'
    : sessionView.phase === 'loading' ? 'Loading conversations…'
      : `${count} conversation${count === 1 ? '' : 's'}`
  const fragment = document.createDocumentFragment()
  for (const session of sessionView.sessions) {
    const item = createElement('div', `session-item${session.sessionId === sessionView.selectedID ? ' selected' : ''}`)
    const select = createElement('button', 'session-select')
    select.type = 'button'
    select.disabled = busy
    select.title = session.sessionId
    select.append(
      createElement('strong', '', session.title),
      createElement('span', '', formatSessionActivity(session.updatedAt)),
    )
    select.addEventListener('click', () => selectUserSession(session.sessionId))
    const actions = createElement('div', 'session-actions')
    const rename = createElement('button', 'session-action', 'Rename')
    rename.type = 'button'
    rename.disabled = busy || Boolean(activeRun)
    rename.addEventListener('click', () => renameUserSession(session))
    const archive = createElement('button', 'session-action danger', 'Archive')
    archive.type = 'button'
    archive.disabled = busy || Boolean(activeRun) || Boolean(session.activeRunId)
    archive.addEventListener('click', () => archiveUserSession(session))
    actions.append(rename, archive)
    item.append(select, actions)
    fragment.append(item)
  }
  elements.sessionList.replaceChildren(fragment)
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
  const selectedSession = connection ? sessionView.sessions.find((session) => session.sessionId === connection.sessionID) : null
  elements.sessionTitle.textContent = selectedSession?.title || 'New conversation'
  elements.sessionFact.textContent = connection?.sessionID ? shortID(connection.sessionID) : connectedSessionLabel()
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

function renderApprovals() {
  const visible = viewState.approvalOrder.map((approvalID) => viewState.approvals[approvalID]).filter(Boolean)
  elements.approvalsEmpty.hidden = visible.length !== 0
  const fragment = document.createDocumentFragment()
  for (const approval of visible) {
    const command = approvalCommands.get(approval.approvalId)
    const card = createElement('article', `approval-card approval-${approval.status}`)
    const header = createElement('div', 'approval-header')
    header.append(
      createElement('strong', '', approval.toolName),
      createElement('span', `approval-status approval-status-${approval.status}`, approval.status),
    )
    const facts = createElement('div', 'approval-facts')
    facts.append(
      createElement('span', '', `Approval ${shortID(approval.approvalId)}`),
      createElement('span', '', `Expires ${formatTimestamp(approval.expiresAt)}`),
    )
    card.append(header, facts)
    if (approval.status === 'pending') {
      const actions = createElement('div', 'approval-actions')
      const deny = createElement('button', 'approval-deny', command?.phase === 'sending' && command.decision === 'deny' ? 'Denying…' : 'Deny')
      const approve = createElement('button', 'approval-approve', command?.phase === 'sending' && command.decision === 'approve' ? 'Approving…' : 'Approve')
      deny.type = 'button'
      approve.type = 'button'
      const blocked = command?.phase === 'sending' || command?.phase === 'committed'
      deny.disabled = blocked
      approve.disabled = blocked
      deny.addEventListener('click', () => decideApproval(approval, 'deny'))
      approve.addEventListener('click', () => decideApproval(approval, 'approve'))
      actions.append(deny, approve)
      card.append(actions)
    }
    if (command?.message) {
      card.append(createElement('div', command.phase === 'error' ? 'approval-command-error' : 'approval-command-note', command.message))
    }
    fragment.append(card)
  }
  elements.approvalList.replaceChildren(fragment)
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
    const label = surfaceID.startsWith('file-change-') ? 'File changes'
      : surfaceID.startsWith('command-') ? 'Command result'
        : surfaceID.startsWith('approval-') ? 'Approval audit' : 'A2UI surface'
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
  const sessionsBusy = ['loading', 'creating', 'renaming', 'archiving'].includes(sessionView.phase)
  elements.prompt.disabled = !connection || busy || sessionsBusy
  elements.sendButton.disabled = !connection || busy || sessionsBusy || !elements.prompt.value.trim()
  if (!connection) {
    elements.composerHint.textContent = 'Sign in with Authorization Code + PKCE to begin.'
  } else if (viewState.status === 'disconnected') {
    elements.composerHint.textContent = 'Reconnect the existing stream before sending another prompt.'
  } else if (viewState.status === 'cancelling') {
    elements.composerHint.textContent = 'Core accepted explicit cancellation; waiting for bounded attempt cleanup.'
  } else if (busy) {
    elements.composerHint.textContent = `Streaming committed AG-UI events${activeRun.reconnects ? ` · reconnect ${activeRun.reconnects}` : ''}`
  } else if (sessionsBusy) {
    elements.composerHint.textContent = 'Preparing conversation history…'
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

function browserAPIEndpoint(path) {
  if (!authorizationConfig || typeof path !== 'string' || !path.startsWith('/v2/')) {
    throw new Error('browser API endpoint is unavailable')
  }
  return new URL(path, authorizationConfig.apiOrigin || window.location.origin).href
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

function formatTimestamp(value) {
  const timestamp = new Date(value)
  return Number.isNaN(timestamp.valueOf()) ? '—' : timestamp.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function formatSessionActivity(value) {
  const timestamp = new Date(value)
  if (Number.isNaN(timestamp.valueOf())) return 'Unknown activity'
  const today = new Date()
  const sameDay = timestamp.getFullYear() === today.getFullYear() && timestamp.getMonth() === today.getMonth() && timestamp.getDate() === today.getDate()
  return sameDay
    ? timestamp.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : timestamp.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

function connectedSessionLabel() {
  return connection ? 'not selected' : 'not connected'
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
