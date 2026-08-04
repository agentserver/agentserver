import { readAuthorizationCallback, validateTokenResponse } from './auth.js'
import {
  createResourceID,
  executorEnrollmentPath,
  executorPath,
  validateEnrollmentToken,
  validateExecutorList,
  validateExecutorResult,
  validateMemberList,
  validateMemberResult,
  validateRemoveMember,
  validateText,
  validateUUID,
  validateWorkspaceList,
  validateWorkspaceResult,
  workspaceArchivePath,
  workspaceExecutorsPath,
  workspaceMemberPath,
  workspaceMembersPath,
  workspacePath,
  workspacesPath,
} from './resources.js'
import {
  buildCreateGatewayRequest,
  createBrowserBinding,
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

const transactionKey = 'agentserver-v2.platform-pkce.v2'
const maximumTransactionAgeMS = 10 * 60 * 1000

const elements = {
  signedOut: requiredElement('signed-out-view'), platformApp: requiredElement('platform-app'),
  loginButton: requiredElement('login-button'), loginError: requiredElement('login-error'),
  sessionButton: requiredElement('session-button'), sessionLabel: requiredElement('session-label'),
  refreshWorkspaces: requiredElement('refresh-workspaces'), workspaceList: requiredElement('workspace-list'), workspaceEmpty: requiredElement('workspace-empty'),
  createWorkspaceDetails: requiredElement('create-workspace-details'), createWorkspaceForm: requiredElement('create-workspace-form'),
  createWorkspaceName: requiredElement('create-workspace-name'), createWorkspaceButton: requiredElement('create-workspace-button'),
  selectionEmpty: requiredElement('selection-empty'), workspaceView: requiredElement('workspace-view'),
  workspaceTitle: requiredElement('workspace-title'), workspaceStatus: requiredElement('workspace-status'), workspaceSubtitle: requiredElement('workspace-subtitle'),
  openBrowser: requiredElement('open-browser'), archiveWorkspace: requiredElement('archive-workspace'), appError: requiredElement('app-error'),
  reauthorizeBanner: requiredElement('reauthorize-banner'), reauthorizeButton: requiredElement('reauthorize-button'),
  workspaceFacts: requiredElement('workspace-facts'), renameWorkspaceForm: requiredElement('rename-workspace-form'),
  renameWorkspaceName: requiredElement('rename-workspace-name'), renameWorkspaceButton: requiredElement('rename-workspace-button'),
  refreshMembers: requiredElement('refresh-members'), memberList: requiredElement('member-list'), memberEmpty: requiredElement('member-empty'),
  addMemberForm: requiredElement('add-member-form'), addMemberID: requiredElement('add-member-id'), addMemberRole: requiredElement('add-member-role'), addMemberButton: requiredElement('add-member-button'),
  refreshExecutors: requiredElement('refresh-executors'), createExecutor: requiredElement('create-executor'), executorList: requiredElement('executor-list'), executorEmpty: requiredElement('executor-empty'),
  refreshGateways: requiredElement('refresh-gateways'), gatewayList: requiredElement('gateway-list'), gatewayEmpty: requiredElement('gateway-empty'), gatewayError: requiredElement('gateway-error'),
  createGatewayForm: requiredElement('create-gateway-form'), createGatewayButton: requiredElement('create-gateway-button'),
  gatewayName: requiredElement('gateway-name'), gatewayModel: requiredElement('gateway-model'), gatewayResponsesURL: requiredElement('gateway-responses-url'),
  gatewayOIDCIssuer: requiredElement('gateway-oidc-issuer'), gatewayOIDCClientID: requiredElement('gateway-oidc-client-id'), gatewayOIDCScopes: requiredElement('gateway-oidc-scopes'),
  gatewayBearerType: requiredElement('gateway-bearer-type'), gatewayMakeDefault: requiredElement('gateway-make-default'), gatewayCallbackURI: requiredElement('gateway-callback-uri'),
  tokenLayer: requiredElement('token-layer'), tokenValue: requiredElement('token-value'), tokenExpiry: requiredElement('token-expiry'), copyToken: requiredElement('copy-token'), closeToken: requiredElement('close-token'),
}

let config = null
let accessToken = ''
let grantedScopes = []
let workspaces = []
let activeWorkspaceID = ''
let activeTab = 'overview'
let members = []
let executors = []
let gateways = []
let pendingWorkspaceGrant = false
let authorizationPending = false
let gatewayAuthorization = null

wireEvents()
void initialize()

function wireEvents() {
  elements.loginButton.addEventListener('click', () => beginAuthorization())
  elements.sessionButton.addEventListener('click', () => accessToken ? signOut() : beginAuthorization())
  elements.reauthorizeButton.addEventListener('click', () => beginAuthorization())
  elements.refreshWorkspaces.addEventListener('click', () => loadWorkspaces(activeWorkspaceID))
  elements.createWorkspaceForm.addEventListener('submit', createWorkspace)
  elements.renameWorkspaceForm.addEventListener('submit', renameWorkspace)
  elements.archiveWorkspace.addEventListener('click', archiveWorkspace)
  elements.refreshMembers.addEventListener('click', loadMembers)
  elements.addMemberForm.addEventListener('submit', addMember)
  elements.refreshExecutors.addEventListener('click', loadExecutors)
  elements.createExecutor.addEventListener('click', createExecutor)
  elements.refreshGateways.addEventListener('click', loadGateways)
  elements.createGatewayForm.addEventListener('submit', createGateway)
  elements.copyToken.addEventListener('click', copyEnrollmentToken)
  elements.closeToken.addEventListener('click', closeEnrollmentToken)
  elements.tokenLayer.addEventListener('click', (event) => { if (event.target === elements.tokenLayer) closeEnrollmentToken() })
  document.querySelectorAll('.tab').forEach((button) => button.addEventListener('click', () => selectTab(button.dataset.tab)))
  window.addEventListener('message', completeGatewayAuthorization)
}

async function initialize() {
  elements.gatewayCallbackURI.textContent = new URL('/auth/llm-gateway/callback', window.location.origin).href
  if (window.location.hash) history.replaceState(null, '', `${window.location.pathname}${window.location.search}`)
  try {
    config = validateConfig(await fetchConfiguration())
    const callback = readAuthorizationCallback(window.location.search)
    if (!callback) return
    history.replaceState(null, '', window.location.pathname)
    await completeAuthorization(callback)
  } catch (error) {
    showLoginError(error)
  }
}

async function beginAuthorization() {
  if (!config || authorizationPending) return
  authorizationPending = true
  clearLoginError()
  elements.loginButton.disabled = true
  elements.reauthorizeButton.disabled = true
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
    window.sessionStorage.setItem(transactionKey, JSON.stringify({
      version: 2, state, verifier, createdAtMS: Date.now(), redirectURI,
      clientId: config.clientId, returnWorkspaceID: activeWorkspaceID,
    }))
    window.location.assign(authorizationURL.href)
  } catch (error) {
    authorizationPending = false
    elements.loginButton.disabled = false
    elements.reauthorizeButton.disabled = false
    showLoginError(error)
  }
}

async function completeAuthorization(callback) {
  const raw = window.sessionStorage.getItem(transactionKey)
  window.sessionStorage.removeItem(transactionKey)
  if (!raw || raw.length > 16 * 1024) throw new Error('The authorization transaction is missing or expired.')
  const transaction = JSON.parse(raw)
  if (!transaction || transaction.version !== 2 || transaction.state !== callback.state || transaction.clientId !== config.clientId ||
      !validPKCESecret(transaction.verifier) || !Number.isSafeInteger(transaction.createdAtMS) || Date.now() < transaction.createdAtMS ||
      Date.now() - transaction.createdAtMS > maximumTransactionAgeMS || (transaction.returnWorkspaceID && !isUUID(transaction.returnWorkspaceID))) {
    throw new Error('The authorization transaction does not match this callback.')
  }
  const requestedScopes = new Set(config.scopes)
  if (callback.scopes.some((scope) => !requestedScopes.has(scope))) throw new Error('The callback exceeds the requested Platform authority.')
  if (callback.error) throw new Error(`Authorization failed: ${callback.error}`)
  const tokenURL = new URL(config.tokenEndpoint, window.location.origin)
  const response = await fetch(tokenURL.href, {
    method: 'POST', mode: tokenURL.origin === window.location.origin ? 'same-origin' : 'cors', cache: 'no-store',
    credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer',
    headers: { Accept: 'application/json', 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ grant_type: 'authorization_code', code: callback.code, redirect_uri: transaction.redirectURI, client_id: transaction.clientId, code_verifier: transaction.verifier }).toString(),
  })
  if (!response.ok) throw new Error(`Token exchange failed with HTTP ${response.status}.`)
  const token = validateTokenResponse(await boundedJSON(response, 128 * 1024), config.scopes)
  accessToken = token.accessToken
  grantedScopes = token.scopes
  authorizationPending = false
  pendingWorkspaceGrant = false
  renderSession()
  await loadWorkspaces(transaction.returnWorkspaceID)
}

function signOut() {
  clearGatewayAuthorization()
  accessToken = ''
  grantedScopes = []
  workspaces = []
  activeWorkspaceID = ''
  members = []
  executors = []
  gateways = []
  pendingWorkspaceGrant = false
  closeEnrollmentToken()
  renderSession()
}

function renderSession() {
  const signedIn = Boolean(accessToken)
  elements.signedOut.hidden = signedIn
  elements.platformApp.hidden = !signedIn
  elements.sessionButton.textContent = signedIn ? 'Sign out' : 'Sign in'
  elements.sessionLabel.textContent = signedIn ? `${Math.max(0, grantedScopes.length - 1)} permissions granted` : 'Signed out'
  elements.loginButton.disabled = false
  elements.reauthorizeButton.disabled = false
  if (!signedIn) {
    elements.workspaceList.replaceChildren()
    elements.workspaceView.hidden = true
    elements.selectionEmpty.hidden = false
  }
}

async function loadWorkspaces(preferredID = '') {
  if (!accessToken) return
  setBusy(elements.refreshWorkspaces, true, '…')
  clearAppError()
  try {
    const value = await apiJSON(workspacesPath(), { method: 'GET' }, 512 * 1024)
    workspaces = [...validateWorkspaceList(value)]
    renderWorkspaceList()
    const next = workspaces.find((item) => item.workspaceId === preferredID) ||
      workspaces.find((item) => item.workspaceId === activeWorkspaceID) || workspaces.find((item) => item.status === 'active') || workspaces[0]
    if (next) selectWorkspace(next.workspaceId)
    else clearWorkspaceSelection()
  } catch (error) {
    handleAppError(error)
  } finally {
    setBusy(elements.refreshWorkspaces, false, '↻')
  }
}

function renderWorkspaceList() {
  const fragment = document.createDocumentFragment()
  for (const workspace of workspaces) {
    const button = createElement('button', `workspace-item${workspace.workspaceId === activeWorkspaceID ? ' active' : ''}${workspace.status === 'archived' ? ' archived' : ''}`)
    button.type = 'button'
    const copy = createElement('span', 'workspace-item-copy')
    copy.append(createElement('strong', '', workspace.name), createElement('span', '', `${workspace.currentUserRole} · ${shortID(workspace.workspaceId)}`))
    button.append(createElement('span', 'workspace-dot'), copy)
    button.addEventListener('click', () => selectWorkspace(workspace.workspaceId))
    fragment.append(button)
  }
  elements.workspaceList.replaceChildren(fragment)
  elements.workspaceEmpty.hidden = workspaces.length !== 0
}

function selectWorkspace(workspaceID) {
  if (!workspaces.some((item) => item.workspaceId === workspaceID)) return
  clearGatewayAuthorization()
  if (activeWorkspaceID && activeWorkspaceID !== workspaceID) pendingWorkspaceGrant = false
  activeWorkspaceID = workspaceID
  members = []
  executors = []
  gateways = []
  activeTab = 'overview'
  clearAppError()
  renderWorkspaceList()
  renderWorkspace()
  selectTab('overview')
}

function clearWorkspaceSelection() {
  activeWorkspaceID = ''
  elements.workspaceView.hidden = true
  elements.selectionEmpty.hidden = false
  renderWorkspaceList()
}

function currentWorkspace() { return workspaces.find((item) => item.workspaceId === activeWorkspaceID) || null }

function renderWorkspace() {
  const workspace = currentWorkspace()
  if (!workspace) return clearWorkspaceSelection()
  elements.selectionEmpty.hidden = true
  elements.workspaceView.hidden = false
  elements.workspaceTitle.textContent = workspace.name
  elements.workspaceStatus.textContent = workspace.status
  elements.workspaceStatus.className = `badge ${workspace.status}`
  elements.workspaceSubtitle.textContent = `${workspace.currentUserRole} · ${workspace.workspaceId}`
  elements.renameWorkspaceName.value = workspace.name
  elements.openBrowser.href = `https://browser.byted.bps.dev/?workspace=${encodeURIComponent(workspace.workspaceId)}`
  elements.reauthorizeBanner.hidden = !pendingWorkspaceGrant
  const isOwner = workspace.currentUserRole === 'owner' && workspace.status === 'active'
  document.querySelectorAll('.owner-control').forEach((element) => { element.hidden = !isOwner })
  document.querySelectorAll('.tab').forEach((button) => { button.hidden = workspace.currentUserRole === 'viewer' && button.dataset.tab !== 'overview' })
  elements.renameWorkspaceForm.hidden = !isOwner
  elements.archiveWorkspace.hidden = !isOwner
  elements.workspaceFacts.replaceChildren(
    fact('Workspace ID', workspace.workspaceId), fact('Your role', workspace.currentUserRole),
    fact('Status', workspace.status), fact('Version', String(workspace.version)),
    fact('Created', formatDate(workspace.createdAt)), fact('Updated', formatDate(workspace.updatedAt)),
  )
}

function selectTab(name) {
  if (!['overview', 'members', 'executors', 'gateways'].includes(name) || !currentWorkspace()) return
  activeTab = name
  document.querySelectorAll('.tab').forEach((button) => button.classList.toggle('active', button.dataset.tab === name))
  document.querySelectorAll('.tab-panel').forEach((panel) => { panel.hidden = panel.dataset.panel !== name })
  if (pendingWorkspaceGrant || currentWorkspace().status !== 'active') return
  if (name === 'members') void loadMembers()
  if (name === 'executors') void loadExecutors()
  if (name === 'gateways') void loadGateways()
}

async function createWorkspace(event) {
  event.preventDefault()
  if (!accessToken || elements.createWorkspaceButton.disabled) return
  setBusy(elements.createWorkspaceButton, true, 'Creating…')
  clearAppError()
  try {
    const request = { workspaceId: createResourceID(window.crypto), name: validateText('workspace name', elements.createWorkspaceName.value.trim(), 256) }
    const result = validateWorkspaceResult(await apiJSON(workspacesPath(), jsonRequest('POST', request), 128 * 1024), 'create')
    workspaces = [result.workspace, ...workspaces.filter((item) => item.workspaceId !== result.workspace.workspaceId)]
    elements.createWorkspaceForm.reset()
    elements.createWorkspaceDetails.open = false
    pendingWorkspaceGrant = true
    activeWorkspaceID = result.workspace.workspaceId
    renderWorkspaceList()
    renderWorkspace()
    selectTab('overview')
  } catch (error) {
    handleAppError(error)
  } finally {
    setBusy(elements.createWorkspaceButton, false, 'Create workspace')
  }
}

async function renameWorkspace(event) {
  event.preventDefault()
  const workspace = currentWorkspace()
  if (!workspace || elements.renameWorkspaceButton.disabled) return
  setBusy(elements.renameWorkspaceButton, true, 'Saving…')
  try {
    const request = { name: validateText('workspace name', elements.renameWorkspaceName.value.trim(), 256), expectedVersion: workspace.version }
    const result = validateWorkspaceResult(await apiJSON(workspacePath(workspace.workspaceId), jsonRequest('PATCH', request), 128 * 1024), 'update')
    replaceWorkspace(result.workspace)
  } catch (error) {
    handleAppError(error)
  } finally {
    setBusy(elements.renameWorkspaceButton, false, 'Save name')
  }
}

async function archiveWorkspace() {
  const workspace = currentWorkspace()
  if (!workspace || !window.confirm(`Archive ${workspace.name}? New runs and resource changes will be blocked.`)) return
  setBusy(elements.archiveWorkspace, true, 'Archiving…')
  try {
    const result = validateWorkspaceResult(await apiJSON(workspaceArchivePath(workspace.workspaceId), jsonRequest('POST', { expectedVersion: workspace.version }), 128 * 1024), 'archive')
    replaceWorkspace(result.workspace)
  } catch (error) {
    handleAppError(error)
  } finally {
    setBusy(elements.archiveWorkspace, false, 'Archive')
  }
}

function replaceWorkspace(workspace) {
  workspaces = workspaces.map((item) => item.workspaceId === workspace.workspaceId ? workspace : item)
  renderWorkspaceList()
  renderWorkspace()
}

async function loadMembers() {
  const workspace = currentWorkspace()
  if (!workspace || pendingWorkspaceGrant) return
  setBusy(elements.refreshMembers, true, 'Loading…')
  try {
    members = [...validateMemberList(await apiJSON(workspaceMembersPath(workspace.workspaceId), { method: 'GET' }, 512 * 1024))]
    renderMembers()
  } catch (error) {
    handleAppError(error)
  } finally {
    setBusy(elements.refreshMembers, false, 'Refresh')
  }
}

function renderMembers() {
  const workspace = currentWorkspace()
  const fragment = document.createDocumentFragment()
  for (const member of members) {
    const card = resourceCard(member.userId, `Joined ${formatDate(member.createdAt)}`, member.role)
    const actions = card.querySelector('.resource-actions')
    if (workspace?.currentUserRole === 'owner' && workspace.status === 'active') {
      const select = createRoleSelect(member.role)
      select.addEventListener('change', () => updateMember(member, select.value))
      const remove = createButton('Remove', 'button danger small', () => removeMember(member))
      actions.append(select, remove)
    }
    fragment.append(card)
  }
  elements.memberList.replaceChildren(fragment)
  elements.memberEmpty.hidden = members.length !== 0
}

async function addMember(event) {
  event.preventDefault()
  const workspace = currentWorkspace()
  if (!workspace || elements.addMemberButton.disabled) return
  setBusy(elements.addMemberButton, true, 'Adding…')
  try {
    const request = { userId: validateUUID('user ID', elements.addMemberID.value.trim()), role: elements.addMemberRole.value }
    validateMemberResult(await apiJSON(workspaceMembersPath(workspace.workspaceId), jsonRequest('POST', request), 128 * 1024), 'add')
    elements.addMemberForm.reset()
    await loadMembers()
  } catch (error) {
    handleAppError(error)
  } finally {
    setBusy(elements.addMemberButton, false, 'Add member')
  }
}

async function updateMember(member, role) {
  const workspace = currentWorkspace()
  if (!workspace || role === member.role) return
  try {
    validateMemberResult(await apiJSON(workspaceMemberPath(workspace.workspaceId, member.userId), jsonRequest('PATCH', { role, expectedVersion: member.version }), 128 * 1024), 'update')
    await loadMembers()
  } catch (error) {
    handleAppError(error)
    renderMembers()
  }
}

async function removeMember(member) {
  const workspace = currentWorkspace()
  if (!workspace || !window.confirm(`Remove ${member.userId} from ${workspace.name}?`)) return
  try {
    validateRemoveMember(await apiJSON(workspaceMemberPath(workspace.workspaceId, member.userId), { method: 'DELETE' }, 128 * 1024), member.userId)
    await loadMembers()
  } catch (error) {
    handleAppError(error)
  }
}

async function loadExecutors() {
  const workspace = currentWorkspace()
  if (!workspace || pendingWorkspaceGrant) return
  setBusy(elements.refreshExecutors, true, 'Loading…')
  try {
    executors = [...validateExecutorList(await apiJSON(workspaceExecutorsPath(workspace.workspaceId), { method: 'GET' }, 512 * 1024), workspace.workspaceId)]
    renderExecutors()
  } catch (error) {
    handleAppError(error)
  } finally {
    setBusy(elements.refreshExecutors, false, 'Refresh')
  }
}

function renderExecutors() {
  const workspace = currentWorkspace()
  const fragment = document.createDocumentFragment()
  for (const executor of executors) {
    const card = resourceCard(executor.executorId, `Updated ${formatDate(executor.updatedAt)} · v${executor.version}`, executor.status)
    const actions = card.querySelector('.resource-actions')
    if (workspace?.currentUserRole === 'owner' && workspace.status === 'active' && executor.status !== 'revoked') {
      if (executor.status === 'enrolling') actions.append(createButton('Issue enrollment token', 'button primary small', () => issueEnrollmentToken(executor)))
      actions.append(createButton('Archive', 'button danger small', () => archiveExecutor(executor)))
    }
    fragment.append(card)
  }
  elements.executorList.replaceChildren(fragment)
  elements.executorEmpty.hidden = executors.length !== 0
}

async function createExecutor() {
  const workspace = currentWorkspace()
  if (!workspace || elements.createExecutor.disabled) return
  setBusy(elements.createExecutor, true, 'Creating…')
  try {
    const executorID = createResourceID(window.crypto)
    validateExecutorResult(await apiJSON(workspaceExecutorsPath(workspace.workspaceId), jsonRequest('POST', { executorId: executorID }), 128 * 1024), workspace.workspaceId, 'create')
    await loadExecutors()
  } catch (error) {
    handleAppError(error)
  } finally {
    setBusy(elements.createExecutor, false, 'New executor')
  }
}

async function issueEnrollmentToken(executor) {
  const workspace = currentWorkspace()
  if (!workspace) return
  try {
    const result = validateEnrollmentToken(await apiJSON(executorEnrollmentPath(workspace.workspaceId, executor.executorId), {
      method: 'POST', headers: { 'Idempotency-Key': `platform-${createResourceID(window.crypto)}` },
    }, 128 * 1024), executor.executorId)
    elements.tokenValue.value = result.token
    elements.tokenExpiry.textContent = formatDate(result.expiresAt)
    elements.copyToken.textContent = 'Copy token'
    elements.tokenLayer.hidden = false
  } catch (error) {
    handleAppError(error)
  }
}

async function archiveExecutor(executor) {
  const workspace = currentWorkspace()
  if (!workspace || !window.confirm(`Archive executor ${executor.executorId}? Its connection and pending enrollment token will be fenced.`)) return
  try {
    validateExecutorResult(await apiJSON(executorPath(workspace.workspaceId, executor.executorId), { method: 'DELETE' }, 128 * 1024), workspace.workspaceId, 'archive')
    await loadExecutors()
  } catch (error) {
    handleAppError(error)
  }
}

async function copyEnrollmentToken() {
  if (!elements.tokenValue.value) return
  try {
    await navigator.clipboard.writeText(elements.tokenValue.value)
    elements.copyToken.textContent = 'Copied'
  } catch {
    elements.tokenValue.focus()
    elements.tokenValue.select()
    elements.copyToken.textContent = 'Select and copy'
  }
}

function closeEnrollmentToken() {
  elements.tokenValue.value = ''
  elements.tokenExpiry.textContent = ''
  elements.tokenLayer.hidden = true
}

async function loadGateways() {
  const workspace = currentWorkspace()
  if (!workspace || pendingWorkspaceGrant) return
  setBusy(elements.refreshGateways, true, 'Loading…')
  clearGatewayError()
  try {
    gateways = [...validateGatewayList(await apiJSON(workspaceLLMGatewaysPath(workspace.workspaceId), { method: 'GET' }, 512 * 1024), workspace.workspaceId)]
    renderGateways()
  } catch (error) {
    showGatewayError(error)
    handleAuthorizationError(error)
  } finally {
    setBusy(elements.refreshGateways, false, 'Refresh')
  }
}

function renderGateways() {
  const workspace = currentWorkspace()
  const fragment = document.createDocumentFragment()
  for (const gateway of gateways) {
    const status = gateway.status === 'disabled' ? 'disabled' : (gateway.grantStatus || 'unlinked')
    const card = resourceCard(gateway.name, `${gateway.defaultModel} · ${gateway.responsesUrl}`, status)
    const title = card.querySelector('.resource-title')
    title.append(createElement('span', '', `OIDC ${gateway.oidcIssuer} · v${gateway.version}${gateway.default ? ' · default' : ''}`))
    const actions = card.querySelector('.resource-actions')
    if (workspace && workspace.status === 'active' && ['owner', 'developer'].includes(workspace.currentUserRole) && gateway.status === 'active') {
      actions.append(createButton(gateway.grantStatus === 'active' ? 'Reauthorize' : 'Authorize my grant', 'button primary small', () => beginGatewayAuthorization(gateway)))
      if (gateway.grantStatus && gateway.grantStatus !== 'revoked') actions.append(createButton('Revoke my grant', 'button secondary small', () => revokeGatewayGrant(gateway)))
    }
    if (workspace?.currentUserRole === 'owner' && gateway.status === 'active') actions.append(createButton('Disable Gateway', 'button danger small', () => disableGateway(gateway)))
    fragment.append(card)
  }
  elements.gatewayList.replaceChildren(fragment)
  elements.gatewayEmpty.hidden = gateways.length !== 0
}

async function createGateway(event) {
  event.preventDefault()
  const workspace = currentWorkspace()
  if (!workspace || elements.createGatewayButton.disabled) return
  setBusy(elements.createGatewayButton, true, 'Creating…')
  clearGatewayError()
  try {
    const request = buildCreateGatewayRequest({
      name: elements.gatewayName.value.trim(), defaultModel: elements.gatewayModel.value.trim(), responsesUrl: elements.gatewayResponsesURL.value.trim(),
      oidcIssuer: elements.gatewayOIDCIssuer.value.trim(), oidcClientId: elements.gatewayOIDCClientID.value.trim(), oidcScopes: elements.gatewayOIDCScopes.value.trim(),
      bearerTokenType: elements.gatewayBearerType.value, makeDefault: elements.gatewayMakeDefault.checked,
    }, window.crypto)
    validateCreateGateway(await apiJSON(workspaceLLMGatewaysPath(workspace.workspaceId), jsonRequest('POST', request), 512 * 1024), workspace.workspaceId)
    elements.createGatewayForm.reset()
    elements.gatewayOIDCScopes.value = 'openid profile email offline_access'
    elements.gatewayMakeDefault.checked = true
    await loadGateways()
  } catch (error) {
    showGatewayError(error)
    handleAuthorizationError(error)
  } finally {
    setBusy(elements.createGatewayButton, false, 'Create Gateway')
  }
}

async function beginGatewayAuthorization(gateway) {
  const workspace = currentWorkspace()
  if (!workspace || gatewayAuthorization) return
  const popup = window.open('about:blank', `agentserver-llm-gateway-${gateway.gatewayId}`, 'popup,width=560,height=760')
  if (!popup) return showGatewayError(new Error('The browser blocked the Gateway authorization popup.'))
  const transaction = { popup, workspaceID: workspace.workspaceId, gatewayID: gateway.gatewayId, browserBinding: createBrowserBinding(window.crypto), expiresAtMS: 0, monitorID: null }
  gatewayAuthorization = transaction
  clearGatewayError()
  try {
    const result = validateBeginAuthorization(await apiJSON(workspaceLLMGatewayActionPath(transaction.workspaceID, transaction.gatewayID, 'authorize'),
      jsonRequest('POST', { browserBinding: transaction.browserBinding }), 128 * 1024), transaction.gatewayID)
    if (gatewayAuthorization !== transaction || popup.closed) throw new Error('Gateway authorization popup was closed or superseded.')
    transaction.expiresAtMS = Date.parse(result.expiresAt)
    popup.location.replace(result.authorizationUrl)
    transaction.monitorID = window.setInterval(() => monitorGatewayAuthorization(transaction), 500)
  } catch (error) {
    clearGatewayAuthorization()
    showGatewayError(error)
    handleAuthorizationError(error)
  }
}

async function completeGatewayAuthorization(event) {
  const transaction = gatewayAuthorization
  if (!transaction || event.origin !== window.location.origin || event.source !== transaction.popup) return
  gatewayAuthorization = null
  stopGatewayMonitor(transaction)
  try {
    const callback = validateGatewayCallbackMessage(event.data)
    if (transaction.popup && !transaction.popup.closed) transaction.popup.close()
    const result = await apiJSON(workspaceLLMGatewayActionPath(transaction.workspaceID, transaction.gatewayID, 'completeAuthorization'), jsonRequest('POST', {
      state: callback.state, code: callback.code || undefined, providerError: callback.providerError || undefined, browserBinding: transaction.browserBinding,
    }), 128 * 1024)
    validateCompleteAuthorization(result, transaction.gatewayID)
    await loadGateways()
  } catch (error) {
    if (transaction.popup && !transaction.popup.closed) transaction.popup.close()
    showGatewayError(error)
    handleAuthorizationError(error)
  } finally {
    transaction.browserBinding = ''
  }
}

async function revokeGatewayGrant(gateway) {
  const workspace = currentWorkspace()
  if (!workspace || !window.confirm(`Revoke your authorization for ${gateway.name}?`)) return
  try {
    validateRevokeGrant(await apiJSON(workspaceLLMGatewayActionPath(workspace.workspaceId, gateway.gatewayId, 'revoke'), { method: 'POST' }, 128 * 1024), gateway.gatewayId)
    await loadGateways()
  } catch (error) {
    showGatewayError(error)
    handleAuthorizationError(error)
  }
}

async function disableGateway(gateway) {
  const workspace = currentWorkspace()
  if (!workspace || !window.confirm(`Disable ${gateway.name}? Bound runs will fail closed.`)) return
  try {
    validateDisableGateway(await apiJSON(workspaceLLMGatewayActionPath(workspace.workspaceId, gateway.gatewayId, 'disable'), { method: 'POST' }, 128 * 1024), gateway.gatewayId)
    await loadGateways()
  } catch (error) {
    showGatewayError(error)
    handleAuthorizationError(error)
  }
}

function monitorGatewayAuthorization(transaction) {
  if (gatewayAuthorization !== transaction) return stopGatewayMonitor(transaction)
  let closed = true
  try { closed = transaction.popup.closed } catch { closed = true }
  if (!closed && Date.now() < transaction.expiresAtMS && currentWorkspace()?.workspaceId === transaction.workspaceID) return
  clearGatewayAuthorization()
  showGatewayError(new Error(closed ? 'The Gateway authorization popup was closed.' : 'The Gateway authorization transaction expired.'))
}

function stopGatewayMonitor(transaction) {
  if (transaction?.monitorID !== null) window.clearInterval(transaction.monitorID)
  if (transaction) transaction.monitorID = null
}

function clearGatewayAuthorization() {
  const transaction = gatewayAuthorization
  gatewayAuthorization = null
  if (!transaction) return
  stopGatewayMonitor(transaction)
  transaction.browserBinding = ''
  if (transaction.popup && !transaction.popup.closed) transaction.popup.close()
}

async function apiJSON(path, options, maximumBytes) {
  if (!accessToken) throw new Error('Platform session is not active.')
  const response = await fetch(path, {
    mode: 'same-origin', cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer',
    ...options,
    headers: { Accept: 'application/json', Authorization: `Bearer ${accessToken}`, ...(options.headers || {}) },
  })
  if (!response.ok) throw await responseError(response)
  return boundedJSON(response, maximumBytes)
}

function jsonRequest(method, value) { return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(value) } }

async function responseError(response) {
  let code = 'http_error'
  let message = `platform-gateway returned HTTP ${response.status}`
  try {
    const raw = await response.text()
    if (raw.length <= 64 * 1024) {
      const envelope = JSON.parse(raw)
      const detail = envelope?.error && typeof envelope.error === 'object' ? envelope.error : envelope
      if (typeof detail?.code === 'string') code = detail.code
      if (typeof detail?.message === 'string') message = detail.message
    }
  } catch { /* retain bounded HTTP error */ }
  const error = new Error(`${code}: ${message}`)
  error.code = code
  error.status = response.status
  return error
}

function handleAppError(error) {
  elements.appError.textContent = safeError(error)
  elements.appError.hidden = false
  handleAuthorizationError(error)
}

function handleAuthorizationError(error) {
  if (error?.status === 401 && activeWorkspaceID) {
    pendingWorkspaceGrant = true
    elements.reauthorizeBanner.hidden = false
  }
}

function clearAppError() { elements.appError.hidden = true; elements.appError.textContent = '' }
function showGatewayError(error) { elements.gatewayError.textContent = safeError(error); elements.gatewayError.hidden = false }
function clearGatewayError() { elements.gatewayError.hidden = true; elements.gatewayError.textContent = '' }
function showLoginError(error) { elements.loginError.textContent = safeError(error); elements.loginError.hidden = false; elements.loginButton.disabled = false }
function clearLoginError() { elements.loginError.hidden = true; elements.loginError.textContent = '' }

function resourceCard(title, subtitle, status) {
  const card = createElement('article', 'resource-card')
  const heading = createElement('div', 'resource-heading')
  const copy = createElement('div', 'resource-title')
  copy.append(createElement('strong', '', title), createElement('span', '', subtitle))
  heading.append(copy, createElement('span', `resource-badge ${status}`, status.replaceAll('_', ' ')))
  card.append(heading, createElement('div', 'resource-actions'))
  return card
}

function createRoleSelect(selected) {
  const select = createElement('select', 'role-select')
  for (const role of ['owner', 'developer', 'viewer']) {
    const option = document.createElement('option')
    option.value = role
    option.textContent = role
    option.selected = role === selected
    select.append(option)
  }
  return select
}

function createButton(label, className, handler) {
  const button = createElement('button', className, label)
  button.type = 'button'
  button.addEventListener('click', handler)
  return button
}

function fact(label, value) {
  const row = document.createElement('div')
  row.append(createElement('dt', '', label), createElement('dd', '', value))
  return row
}

function setBusy(button, busy, text) { button.disabled = busy; button.textContent = text }
function formatDate(value) { const date = new Date(value); return Number.isNaN(date.valueOf()) ? '—' : date.toLocaleString() }
function shortID(value) { return value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value }
function safeError(error) { return error instanceof Error && error.message ? error.message : String(error || 'Unknown error.') }
function createElement(tag, className = '', text = '') { const element = document.createElement(tag); if (className) element.className = className; if (text) element.textContent = text; return element }
function requiredElement(id) { const value = document.getElementById(id); if (!value) throw new Error(`Missing Platform UI element #${id}.`); return value }
function isUUID(value) { try { validateUUID('workspace ID', value); return true } catch { return false } }

function validateConfig(value) {
  requireExactKeys(value, ['version', 'authorizationEndpoint', 'tokenEndpoint', 'redirectPath', 'clientId', 'scopes', 'audience'])
  if (value.version !== 1 || value.redirectPath !== '/') throw new Error('Unsupported Platform authorization configuration.')
  const authorizationEndpoint = validateOAuthEndpoint('authorization', value.authorizationEndpoint, '/oauth2/auth')
  const tokenEndpoint = validateOAuthEndpoint('token', value.tokenEndpoint, '/oauth2/token')
  if (oauthEndpointAuthority(authorizationEndpoint) !== oauthEndpointAuthority(tokenEndpoint)) throw new Error('Platform authorization endpoints use different authorities.')
  validateText('OAuth client ID', value.clientId, 512)
  validateText('OAuth audience', value.audience, 512)
  if (!Array.isArray(value.scopes) || value.scopes.length < 1 || value.scopes.length > 32 || new Set(value.scopes).size !== value.scopes.length) throw new Error('Platform authorization scopes are invalid.')
  value.scopes.forEach((scope) => { validateText('OAuth scope', scope, 128); if (/\s/u.test(scope)) throw new Error('Platform scope is invalid.') })
  return Object.freeze({ ...value, authorizationEndpoint, tokenEndpoint, scopes: Object.freeze([...value.scopes]) })
}

function validateOAuthEndpoint(name, raw, requiredPath) {
  if (raw === requiredPath) return raw
  if (typeof raw !== 'string') throw new Error(`Platform OAuth ${name} endpoint is invalid.`)
  let parsed
  try { parsed = new URL(raw) } catch { throw new Error(`Platform OAuth ${name} endpoint is invalid.`) }
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.pathname !== requiredPath || parsed.search || parsed.hash || parsed.href !== raw) throw new Error(`Platform OAuth ${name} endpoint is invalid.`)
  return parsed.href
}

function oauthEndpointAuthority(endpoint) { return endpoint.startsWith('/') ? '' : new URL(endpoint).origin }
async function fetchConfiguration() { const response = await fetch('/auth/config', { method: 'GET', cache: 'no-store', credentials: 'omit', redirect: 'error', referrerPolicy: 'no-referrer', headers: { Accept: 'application/json' } }); if (!response.ok) throw new Error(`Configuration request failed with HTTP ${response.status}.`); return boundedJSON(response, 32 * 1024) }
async function boundedJSON(response, maximumBytes) { const raw = await response.text(); if (new TextEncoder().encode(raw).length > maximumBytes) throw new Error('Response exceeded its size limit.'); return JSON.parse(raw) }
function requireExactKeys(value, expected) { if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).sort().join('\0') !== [...expected].sort().join('\0')) throw new Error('Configuration fields are invalid.') }
function validPKCESecret(value) { return typeof value === 'string' && value.length >= 43 && value.length <= 128 && /^[A-Za-z0-9._~-]+$/u.test(value) }
function randomBase64URL() { const value = new Uint8Array(32); window.crypto.getRandomValues(value); return encodeBase64URL(value) }
function encodeBase64URL(bytes) { let binary = ''; for (const value of bytes) binary += String.fromCharCode(value); return btoa(binary).replace(/\+/gu, '-').replace(/\//gu, '_').replace(/=+$/gu, '') }
