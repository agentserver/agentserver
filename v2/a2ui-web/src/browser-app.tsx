import { Archive, ArrowDown, CheckCircle2, ChevronDown, CircleStop, Clock3, Code2, FolderOpen, KeyRound, LoaderCircle, MessageSquare, Pencil, Plus, RefreshCw, Search, Send, ShieldCheck, Sparkles, SquareTerminal, XCircle } from "lucide-react"
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { useLocation, useNavigate } from "react-router-dom"
import {
  APIError,
  AppShell,
  Badge,
  Button,
  EdgeAPI,
  EVENT_CURSOR_NAME,
  Input,
  MAXIMUM_EVENT_STREAM_BYTES,
  NativeSelect,
  ResourceAPI,
  SSEDecoder,
  SidebarBrand,
  SidebarSection,
  SignedOutShell,
  Textarea,
  appendUserMessage,
  browserConfig,
  canonicalID,
  cloneConversationState,
  conversationFromTranscript,
  createConversationState,
  newID,
  randomSecret,
  reduceAGUIEvent,
  resolveJSONPointer,
  safeError,
  shortID,
  titleFromPrompt,
  useAuth,
  type A2UIComponent,
  type A2UISurface,
  type ApprovalView,
  type CommandItem,
  type ConversationMessage,
  type ConversationState,
  type PermissionMode,
  type ReasoningMessage,
  type SessionTrajectory,
  type SessionTrajectoryRecord,
  type ToolView,
  type UserSession,
} from "@agentserver/v2-web-shared"
import { MarkdownText } from "./markdown-text"
import { TrajectoryView } from "./trajectory-view"

interface ActiveRun {
  sessionId: string
  idempotencyKey: string
  clientRunId: string
  messageId: string
  prompt: string
  permissionMode: PermissionMode
  permissionModeVersion: number
  workingDirectory: string
  workingDirectoryVersion: number
  environmentId?: string
  cursor: string
  checkpoint: ConversationState
  controller: AbortController | null
}

type BrowserView = "conversation" | "trajectory"

export function BrowserApp() {
  const { t } = useTranslation()
  const auth = useAuth()
  const location = useLocation()
  const navigate = useNavigate()
  const routeWorkspace = workspaceFromLocation(location.pathname, location.search)
  const [workspaceInput, setWorkspaceInput] = useState(routeWorkspace)
  const [workspaceError, setWorkspaceError] = useState("")

  useEffect(() => {
    if (auth.status === "signed-in" && auth.workspaceId && location.pathname !== `/workspaces/${auth.workspaceId}`) {
      navigate(`/workspaces/${auth.workspaceId}`, { replace: true })
    }
  }, [auth.status, auth.workspaceId, location.pathname, navigate])

  if (auth.status === "loading") return <SignedOutShell title={t("auth.preparing")} description={t("auth.browserDescription")}><div className="skeleton skeleton-auth" /></SignedOutShell>
  if (auth.status !== "signed-in") return <SignedOutShell title={t("auth.browserTitle")} description={t("auth.browserDescription")} error={auth.error || workspaceError || undefined}>
    <form onSubmit={(event) => { event.preventDefault(); try { const id = canonicalID("workspace ID", workspaceInput.trim()); setWorkspaceError(""); void auth.signIn(id, `/workspaces/${id}`) } catch { setWorkspaceError(t("auth.invalidWorkspace")) } }}>
      <LabelledInput label={t("auth.workspaceId")} value={workspaceInput} onChange={setWorkspaceInput} placeholder="00000000-0000-4000-8000-000000000000" />
      <Button size="lg" type="submit" disabled={!auth.config || !workspaceInput.trim()}>{t("auth.continue")}</Button>
    </form>
  </SignedOutShell>
  if (!auth.config) return null
  const workspaceId = auth.workspaceId
  const config = browserConfig(auth.config)
  const apiOrigin = config.apiOrigin || window.location.origin
  return <AuthenticatedBrowser workspaceId={workspaceId} token={auth.token} apiOrigin={apiOrigin} onSignOut={auth.signOut} />
}

function AuthenticatedBrowser({ workspaceId, token, apiOrigin, onSignOut }: { workspaceId: string; token: string; apiOrigin: string; onSignOut: () => void }) {
  const { t } = useTranslation()
  const api = useMemo(() => new ResourceAPI(apiOrigin, token), [apiOrigin, token])
  const edge = useMemo(() => new EdgeAPI(apiOrigin, token), [apiOrigin, token])
  const [sessions, setSessions] = useState<UserSession[]>([])
  const [selectedId, setSelectedId] = useState("")
  const [sessionLoading, setSessionLoading] = useState(true)
  const [sessionError, setSessionError] = useState("")
  const [permissionModeUpdating, setPermissionModeUpdating] = useState(false)
  const [workingDirectoryUpdating, setWorkingDirectoryUpdating] = useState(false)
  const [transcriptLoading, setTranscriptLoading] = useState(false)
  const [transcriptTruncated, setTranscriptTruncated] = useState(false)
  const [view, setView] = useState<BrowserView>("conversation")
  const [trajectory, setTrajectory] = useState<SessionTrajectory | null>(null)
  const [trajectoryLoading, setTrajectoryLoading] = useState(false)
  const [trajectoryLoadingEarlier, setTrajectoryLoadingEarlier] = useState(false)
  const [trajectoryError, setTrajectoryError] = useState("")
  const [selectedTrajectoryRecord, setSelectedTrajectoryRecord] = useState("")
  const [query, setQuery] = useState("")
  const [prompt, setPrompt] = useState("")
  const [conversation, setConversation] = useState<ConversationState>(createConversationState)
  const stateRef = useRef(conversation)
  const activeRun = useRef<ActiveRun | null>(null)
  const selectedIdRef = useRef(selectedId)
  const selectionRevisionRef = useRef(0)
  const transcriptRevisionRef = useRef(0)
  const trajectoryRevisionRef = useRef(0)
  const trajectoryRef = useRef<SessionTrajectory | null>(null)
  const composerRef = useRef<HTMLTextAreaElement | null>(null)

  const commit = useCallback((next: ConversationState | ((current: ConversationState) => ConversationState)) => {
    const value = typeof next === "function" ? next(stateRef.current) : next
    stateRef.current = value; setConversation(value); return value
  }, [])

  const commitTrajectory = useCallback((next: SessionTrajectory | null | ((current: SessionTrajectory | null) => SessionTrajectory | null)) => {
    const value = typeof next === "function" ? next(trajectoryRef.current) : next
    trajectoryRef.current = value; setTrajectory(value); return value
  }, [])

  const loadTranscript = useCallback(async (sessionId: string) => {
    const revision = ++transcriptRevisionRef.current
    if (!sessionId) { setTranscriptLoading(false); setTranscriptTruncated(false); commit(createConversationState()); return }
    setTranscriptLoading(true)
    setTranscriptTruncated(false)
    commit({ ...createConversationState(), status: "connecting" })
    try {
      const transcript = await api.getSessionTranscript(workspaceId, sessionId)
      if (transcriptRevisionRef.current !== revision || selectedIdRef.current !== sessionId || activeRun.current) return
      setTranscriptTruncated(transcript.truncated)
      commit(conversationFromTranscript(transcript))
    } catch (error) {
      if (transcriptRevisionRef.current !== revision || selectedIdRef.current !== sessionId || activeRun.current) return
      commit({ ...createConversationState(), error: { code: "transcript_load_failed", message: safeError(error) } })
    } finally {
      if (transcriptRevisionRef.current === revision) setTranscriptLoading(false)
    }
  }, [api, commit, workspaceId])

  const loadTrajectoryTail = useCallback(async (sessionId: string, preserveLoadedHistory: boolean) => {
    if (!sessionId) return
    const revision = trajectoryRevisionRef.current
    if (!trajectoryRef.current || trajectoryRef.current.sessionId !== sessionId) setTrajectoryLoading(true)
    setTrajectoryError("")
    try {
      const loaded = await api.getSessionTrajectory(workspaceId, sessionId, undefined, 100)
      if (trajectoryRevisionRef.current !== revision || selectedIdRef.current !== sessionId) return
      const next = commitTrajectory((current) => {
        if (!preserveLoadedHistory || !current || current.sessionId !== sessionId) return loaded
        const preservePageBoundary = current.records.length > 0 && trajectoryWindowsOverlap(current.records, loaded.records)
        const merged: SessionTrajectory = {
          ...loaded,
          records: mergeTrajectoryTailRecords(current.records, loaded.records),
          hasMore: preservePageBoundary ? current.hasMore : loaded.hasMore,
          truncated: current.truncated || loaded.truncated,
        }
        if (preservePageBoundary) {
          if (current.nextBefore !== undefined) merged.nextBefore = current.nextBefore
          else delete merged.nextBefore
        }
        return merged
      })
      setSelectedTrajectoryRecord((current) => current && next?.records.some((record) => record.id === current) ? current : "")
    } catch (error) {
      if (trajectoryRevisionRef.current === revision && selectedIdRef.current === sessionId) setTrajectoryError(safeError(error))
    } finally {
      if (trajectoryRevisionRef.current === revision) setTrajectoryLoading(false)
    }
  }, [api, commitTrajectory, workspaceId])

  const loadEarlierTrajectory = useCallback(async () => {
    const current = trajectoryRef.current
    const sessionId = selectedIdRef.current
    if (!current?.hasMore || !current.nextBefore || !sessionId || trajectoryLoadingEarlier) return
    const revision = trajectoryRevisionRef.current
    setTrajectoryLoadingEarlier(true); setTrajectoryError("")
    try {
      const loaded = await api.getSessionTrajectory(workspaceId, sessionId, current.nextBefore, 100)
      if (trajectoryRevisionRef.current !== revision || selectedIdRef.current !== sessionId) return
      commitTrajectory((latest) => {
        if (!latest || latest.sessionId !== sessionId) return latest
        const merged: SessionTrajectory = {
          ...latest,
          records: prependTrajectoryRecords(loaded.records, latest.records),
          hasMore: loaded.hasMore,
          truncated: latest.truncated || loaded.truncated,
          readAt: loaded.readAt,
        }
        if (loaded.nextBefore !== undefined) merged.nextBefore = loaded.nextBefore
        else delete merged.nextBefore
        if (loaded.activeRunId !== undefined) merged.activeRunId = loaded.activeRunId
        else delete merged.activeRunId
        return merged
      })
    } catch (error) {
      if (trajectoryRevisionRef.current === revision && selectedIdRef.current === sessionId) setTrajectoryError(safeError(error))
    } finally {
      if (trajectoryRevisionRef.current === revision) setTrajectoryLoadingEarlier(false)
    }
  }, [api, commitTrajectory, trajectoryLoadingEarlier, workspaceId])

  const loadSessions = useCallback(async (preferred = "") => {
    const selectionRevision = selectionRevisionRef.current
    setSessionLoading(true); setSessionError("")
    try {
      const loaded = [...(await api.listSessions(workspaceId))]
      if (selectionRevisionRef.current !== selectionRevision) {
        setSessions((current) => {
          const selected = current.find((item) => item.sessionId === selectedIdRef.current)
          return selected && !loaded.some((item) => item.sessionId === selected.sessionId) ? [selected, ...loaded] : loaded
        })
        return
      }
      setSessions(loaded)
      const selected = loaded.find((item) => item.sessionId === (preferred || selectedIdRef.current)) ?? loaded[0]
      setSelectedId(selected?.sessionId ?? "")
      selectedIdRef.current = selected?.sessionId ?? ""
    } catch (error) { setSessionError(safeError(error)) } finally { setSessionLoading(false) }
  }, [api, workspaceId])

  useEffect(() => { void loadSessions() }, [loadSessions])
  useEffect(() => { void loadTranscript(selectedId) }, [loadTranscript, selectedId])
  useEffect(() => {
    trajectoryRevisionRef.current += 1
    commitTrajectory(null); setTrajectoryError(""); setTrajectoryLoading(false); setTrajectoryLoadingEarlier(false); setSelectedTrajectoryRecord("")
  }, [commitTrajectory, selectedId])
  useEffect(() => {
    if (view === "trajectory" && selectedId) void loadTrajectoryTail(selectedId, Boolean(trajectoryRef.current))
  }, [loadTrajectoryTail, selectedId, view])
  useEffect(() => {
    if (view !== "trajectory" || !selectedId || trajectoryLoadingEarlier) return
    const live = Boolean(trajectory?.activeRunId) || ["connecting", "running", "cancelling"].includes(conversation.status)
    if (!live) return
    let cancelled = false
    let timer = 0
    const poll = async () => {
      await loadTrajectoryTail(selectedId, true)
      if (!cancelled) timer = window.setTimeout(() => { void poll() }, 1200)
    }
    timer = window.setTimeout(() => { void poll() }, 1200)
    return () => { cancelled = true; window.clearTimeout(timer) }
  }, [conversation.status, loadTrajectoryTail, selectedId, trajectory?.activeRunId, trajectoryLoadingEarlier, view])
  useEffect(() => () => activeRun.current?.controller?.abort(), [])

  const selectSession = (id: string) => {
    if (activeRun.current) return
    selectionRevisionRef.current += 1
    setSelectedId(id); selectedIdRef.current = id; commit({ ...createConversationState(), status: "connecting" }); setPrompt("")
    requestAnimationFrame(() => composerRef.current?.focus())
  }

  const createSession = useCallback(async (title = t("browser.newChat")) => {
    setSessionError("")
    try {
      const result = await api.createSession(workspaceId, { sessionId: newID(), title })
      activeRun.current?.controller?.abort()
      activeRun.current = null
      setSessions((current) => [result.session, ...current.filter((item) => item.sessionId !== result.session.sessionId)])
      selectionRevisionRef.current += 1
      setSelectedId(result.session.sessionId); selectedIdRef.current = result.session.sessionId; commit(createConversationState())
      requestAnimationFrame(() => composerRef.current?.focus())
      return result.session
    } catch (error) { setSessionError(safeError(error)); throw error }
  }, [api, commit, t, workspaceId])

  const refreshSession = useCallback(async (id: string) => {
    try {
      const loaded = [...(await api.listSessions(workspaceId))]
      setSessions(loaded)
      if (!loaded.some((item) => item.sessionId === id)) { setSelectedId(loaded[0]?.sessionId ?? ""); selectedIdRef.current = loaded[0]?.sessionId ?? "" }
    } catch { /* the stream result remains visible */ }
  }, [api, workspaceId])

  const updatePermissionMode = useCallback(async (mode: PermissionMode) => {
    const session = sessions.find((item) => item.sessionId === selectedIdRef.current)
    if (!session || session.permissionMode === mode || permissionModeUpdating) return
    if (mode === "full-access" && !window.confirm(t("browser.permissionFullAccessConfirm"))) return
    setSessionError("")
    setPermissionModeUpdating(true)
    try {
      const result = await api.updateSessionPermissionMode(workspaceId, session.sessionId, {
        permissionMode: mode,
        expectedPermissionModeVersion: session.permissionModeVersion,
      })
      setSessions((current) => current.map((item) => item.sessionId === result.session.sessionId ? result.session : item))
    } catch (error) {
      if (error instanceof APIError && error.status === 409) {
        await loadSessions(session.sessionId)
        setSessionError(t("browser.permissionModeConflict"))
      } else {
        setSessionError(`${t("browser.permissionModeUpdateFailed")} ${safeError(error)}`)
      }
    } finally {
      setPermissionModeUpdating(false)
    }
  }, [api, loadSessions, permissionModeUpdating, sessions, t, workspaceId])

  const updateWorkingDirectory = useCallback(async (environmentId: string, workingDirectory: string) => {
    const session = sessions.find((item) => item.sessionId === selectedIdRef.current)
    if (!session || workingDirectoryUpdating) return
    const normalizedDirectory = workingDirectory.trim() || "."
    const normalizedEnvironment = environmentId.trim()
    setSessionError("")
    setWorkingDirectoryUpdating(true)
    try {
      const result = await api.updateSessionWorkingDirectory(workspaceId, session.sessionId, {
        ...(normalizedEnvironment ? { environmentId: normalizedEnvironment } : {}),
        workingDirectory: normalizedDirectory,
        expectedWorkingDirectoryVersion: session.workingDirectoryVersion,
      })
      setSessions((current) => current.map((item) => item.sessionId === result.session.sessionId ? result.session : item))
    } catch (error) {
      if (error instanceof APIError && error.status === 409) {
        await loadSessions(session.sessionId)
        setSessionError(t("browser.workingDirectoryConflict"))
      } else {
        setSessionError(`${t("browser.workingDirectoryUpdateFailed")} ${safeError(error)}`)
      }
    } finally {
      setWorkingDirectoryUpdating(false)
    }
  }, [api, loadSessions, sessions, t, workingDirectoryUpdating, workspaceId])

  const applyEvent = useCallback((run: ActiveRun, event: Record<string, unknown>) => {
    if (activeRun.current !== run) return
    const next = commit((current) => reduceAGUIEvent(current, event))
    if (event.type === "CUSTOM" && event.name === EVENT_CURSOR_NAME) { run.cursor = next.cursor; run.checkpoint = cloneConversationState(next) }
  }, [commit])

  const stream = useCallback(async (reconnect: boolean) => {
    const run = activeRun.current
    const sessionId = run?.sessionId ?? ""
    if (!run || run.controller || !sessionId) return
    if (reconnect) commit({ ...cloneConversationState(run.checkpoint), status: "connecting", error: null })
    const controller = new AbortController(); run.controller = controller
    try {
      const body = {
        threadId: sessionId,
        runId: run.clientRunId,
        messages: [{ id: run.messageId, role: "user" as const, content: run.prompt }],
        tools: [],
        context: [],
        ...((run.cursor || run.permissionModeVersion > 0 || run.workingDirectoryVersion > 0) ? {
          forwardedProps: { agentserver: {
            ...(run.cursor ? { eventCursor: run.cursor } : {}),
            ...(run.permissionModeVersion > 0 ? { expectedPermissionModeVersion: run.permissionModeVersion } : {}),
            ...(run.workingDirectoryVersion > 0 ? { expectedWorkingDirectoryVersion: run.workingDirectoryVersion } : {}),
          } },
        } : {}),
      }
      const response = await edge.streamRun(workspaceId, sessionId, run.idempotencyKey, body, controller.signal)
      if (!response) throw new Error("The browser did not expose the AG-UI response stream.")
      const reader = response.getReader(); const text = new TextDecoder("utf-8", { fatal: true }); const decoder = new SSEDecoder(); let bytes = 0
      while (true) {
        const result = await reader.read(); if (result.done) break
        bytes += result.value.byteLength; if (bytes > MAXIMUM_EVENT_STREAM_BYTES) throw new Error("The AG-UI event stream exceeded 16 MiB.")
        for (const event of decoder.push(text.decode(result.value, { stream: true }))) applyEvent(run, event)
      }
      for (const event of decoder.push(text.decode())) applyEvent(run, event)
      for (const event of decoder.finish()) applyEvent(run, event)
      const terminal = ["completed", "failed", "cancelled"].includes(stateRef.current.status)
      if (!terminal) throw new Error("The AG-UI stream ended without a terminal event.")
      run.controller = null; if (activeRun.current === run) activeRun.current = null
      await refreshSession(sessionId)
    } catch (error) {
      run.controller = null
      if (activeRun.current !== run || controller.signal.aborted) return
      if (["completed", "failed", "cancelled"].includes(stateRef.current.status)) { activeRun.current = null; return }
      if (error instanceof APIError && error.status === 409 && error.code === "version_conflict" && (run.permissionModeVersion > 0 || run.workingDirectoryVersion > 0)) {
        // CreateRun checks both session authorities atomically. If another
        // tab changed either one between the optimistic session snapshot and
        // this request, no run exists and the optimistic user message must
        // not be left in the conversation. Keep the exact prompt so it can be
        // sent again with freshly loaded versions.
        const promptToRestore = run.prompt
        activeRun.current = null
        transcriptRevisionRef.current += 1
        setPrompt(promptToRestore)
        await loadSessions(sessionId)
        if (activeRun.current !== null || selectedIdRef.current !== sessionId) return
        await loadTranscript(sessionId)
        if (activeRun.current === null && selectedIdRef.current === sessionId) setSessionError(t("browser.sessionAuthorityConflict"))
        return
      }
      const message = safeError(error)
      commit((current) => ({ ...current, status: "disconnected", error: { code: error instanceof APIError ? error.code : "stream_disconnected", message } }))
    }
  }, [applyEvent, commit, edge, loadSessions, loadTranscript, refreshSession, t, workspaceId])

  const sendPrompt = async (event: FormEvent) => {
    event.preventDefault()
    if (activeRun.current || !prompt.trim()) return
    const canonicalPrompt = prompt.trim()
    let sessionId = selectedIdRef.current
    let session = sessions.find((item) => item.sessionId === sessionId)
    if (!sessionId) {
      try {
        session = await createSession(titleFromPrompt(canonicalPrompt))
        sessionId = session.sessionId
      } catch { return }
    }
    if (!session || session.sessionId !== sessionId) return
    const nonce = randomSecret("turn")
    const messageId = `user-${nonce}`
    let next: ConversationState = { ...stateRef.current, status: "connecting", runId: "", cursor: "", cursorSequence: 0, error: null }
    next = appendUserMessage(next, messageId, canonicalPrompt)
    commit(next)
    transcriptRevisionRef.current += 1
    setTranscriptLoading(false)
    activeRun.current = { sessionId,
      idempotencyKey: randomSecret("run"), clientRunId: `browser-${nonce}`, messageId,
      prompt: canonicalPrompt, permissionMode: session.permissionMode, permissionModeVersion: session.permissionModeVersion,
      workingDirectory: session.workingDirectory, workingDirectoryVersion: session.workingDirectoryVersion,
      ...(session.environmentId ? { environmentId: session.environmentId } : {}),
      cursor: "", checkpoint: cloneConversationState(next), controller: null,
    }
    setPrompt("")
    void stream(false)
  }

  const cancelRun = async () => {
    if (!activeRun.current || !stateRef.current.runId) return
    try { await api.cancelRun(workspaceId, stateRef.current.runId) } catch (error) { commit((current) => ({ ...current, error: { code: "cancel_failed", message: safeError(error) } })) }
  }

  const decide = async (approval: ApprovalView, decision: "approve" | "deny") => {
    try {
      await api.decideApproval(workspaceId, approval.approvalId, { decision, nonce: approval.nonce, contextDigest: approval.contextDigest, expectedApprovalVersion: approval.version })
    } catch (error) { commit((current) => ({ ...current, error: { code: "approval_failed", message: safeError(error) } })); throw error }
  }

  const renameSession = async (session: UserSession) => {
    const title = window.prompt(t("browser.rename"), session.title)?.trim()
    if (!title) return
    try { const result = await api.updateSession(workspaceId, session.sessionId, { title, expectedVersion: session.version }); setSessions((current) => current.map((item) => item.sessionId === session.sessionId ? result.session : item)) } catch (error) { setSessionError(safeError(error)) }
  }
  const archiveSession = async (session: UserSession) => {
    if (!window.confirm(t("browser.archiveConfirm"))) return
    try { await api.archiveSession(workspaceId, session.sessionId, session.version); if (selectedIdRef.current === session.sessionId) commit(createConversationState()); await loadSessions() } catch (error) { setSessionError(safeError(error)) }
  }

  const filtered = sessions.filter((session) => !query.trim() || session.title.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))
  const sidebar = <>
    <SidebarBrand mark="A" title={t("browser.title")} subtitle="Browser" />
    <div className="browser-workspace"><span className="workspace-avatar">W</span><span className="sidebar-copy"><small>{t("browser.workspace")}</small><strong>{shortID(workspaceId)}</strong></span></div>
    <button className="browser-new-chat" type="button" onClick={() => void createSession()}><Plus size={17} /><span className="sidebar-copy">{t("browser.newChat")}</span></button>
    <div className="session-search sidebar-copy"><Search size={14} /><input id="session-search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("browser.searchChats")} /></div>
    <SidebarSection label={t("browser.today")}>
      {sessionLoading ? <div className="session-loading sidebar-copy">{t("common.loading")}</div> : filtered.map((session) => <SessionButton key={session.sessionId} session={session} active={session.sessionId === selectedId} onSelect={() => selectSession(session.sessionId)} onRename={() => void renameSession(session)} onArchive={() => void archiveSession(session)} />)}
      {!sessionLoading && filtered.length === 0 ? <div className="session-empty sidebar-copy">{t("browser.noSessions")}</div> : null}
    </SidebarSection>
    {sessionError ? <div className="sidebar-error sidebar-copy">{sessionError}</div> : null}
  </>

  const commands = useMemo<CommandItem[]>(() => [
    { id: "new", label: t("browser.newChat"), icon: <Plus size={16} />, run: (): void => { void createSession() } },
    ...sessions.map((session) => ({ id: session.sessionId, label: session.title, keywords: session.sessionId, icon: <MessageSquare size={16} />, run: () => selectSession(session.sessionId) })),
  ], [createSession, sessions, t])

  const emptyConversation = view === "conversation" && !transcriptLoading && !conversation.error && conversation.timeline.length === 0
  const selectedSessionTitle = sessions.find((item) => item.sessionId === selectedId)?.title ?? t("browser.newChat")
  const selectedSession = sessions.find((item) => item.sessionId === selectedId) ?? null
  const pendingApproval = conversation.approvalOrder.map((id) => conversation.approvals[id]).find((approval) => approval?.status === "pending") ?? null

  return <AppShell className="browser-shell" sidebar={sidebar} commands={commands} accountLabel={shortID(workspaceId)} onSignOut={onSignOut}>
    <div className={`browser-main${emptyConversation ? " browser-main-empty" : ""}${view === "trajectory" ? " browser-main-trajectory" : ""}`} data-run-status={conversation.status}>
      <header className="conversation-header">
        <div className="conversation-identity"><strong>{selectedSessionTitle}</strong><small>{shortID(workspaceId)}</small></div>
        <div className="browser-view-tabs" role="tablist" aria-label={t("browser.sessionViews")}>
          <button type="button" role="tab" aria-selected={view === "conversation"} onClick={() => setView("conversation")}>{t("browser.conversation")}</button>
          <button type="button" role="tab" aria-selected={view === "trajectory"} onClick={() => setView("trajectory")}>{t("browser.trajectory")}</button>
        </div>
        <Button variant="ghost" size="icon" onClick={() => {
          void loadSessions(selectedId)
          if (selectedId) {
            if (view === "conversation") void loadTranscript(selectedId)
            else void loadTrajectoryTail(selectedId, true)
          }
        }} aria-label={t("common.refresh")}><RefreshCw size={16} /></Button>
      </header>
      {view === "conversation" ? <>
        <ConversationView state={conversation} loading={transcriptLoading} truncated={transcriptTruncated} onConfigure={() => { window.location.href = `https://agent.byted.bps.dev/workspaces/${workspaceId}/gateways` }} />
        <Composer centered={emptyConversation} value={prompt} onChange={setPrompt} onSubmit={sendPrompt} onCancel={cancelRun} onReconnect={() => void stream(true)} state={conversation} approval={pendingApproval} onDecision={decide} inputRef={composerRef} session={selectedSession} activeRun={activeRun.current} permissionModeUpdating={permissionModeUpdating} onPermissionModeChange={(mode) => void updatePermissionMode(mode)} workingDirectoryUpdating={workingDirectoryUpdating} onWorkingDirectoryChange={(environmentId, directory) => void updateWorkingDirectory(environmentId, directory)} />
      </> : <TrajectoryView
        key={selectedId}
        records={trajectory?.records ?? []}
        activeRunId={trajectory?.activeRunId ?? ""}
        readAt={trajectory?.readAt ?? ""}
        hasMore={trajectory?.hasMore ?? false}
        truncated={trajectory?.truncated ?? false}
        loading={trajectoryLoading}
        loadingEarlier={trajectoryLoadingEarlier}
        error={trajectoryError}
        selectedId={selectedTrajectoryRecord}
        onSelect={setSelectedTrajectoryRecord}
        onLoadEarlier={() => void loadEarlierTrajectory()}
        onRetry={() => { if (selectedId) void loadTrajectoryTail(selectedId, true) }}
        permissionControl={<PermissionModeSelector session={selectedSession} activeRun={activeRun.current} updating={permissionModeUpdating} onChange={(mode) => void updatePermissionMode(mode)} />}
      />}
    </div>
  </AppShell>
}

function SessionButton({ session, active, onSelect, onRename, onArchive }: { session: UserSession; active: boolean; onSelect: () => void; onRename: () => void; onArchive: () => void }) {
  const { t } = useTranslation()
  return <div className={`session-row${active ? " active" : ""}`}><button type="button" className="session-select" onClick={onSelect}><MessageSquare size={15} /><span className="sidebar-copy">{session.title}</span></button><div className="session-actions sidebar-copy"><Button variant="ghost" size="icon" aria-label={t("common.actions")} onClick={(event) => { event.stopPropagation(); onRename() }}><Pencil size={13} /></Button><Button variant="ghost" size="icon" aria-label={t("browser.archive")} onClick={(event) => { event.stopPropagation(); onArchive() }}><Archive size={13} /></Button></div></div>
}

function ConversationView({ state, loading, truncated, onConfigure }: { state: ConversationState; loading: boolean; truncated: boolean; onConfigure: () => void }) {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const atBottomRef = useRef(true)
  const [atBottom, setAtBottom] = useState(true)
  useLayoutEffect(() => {
    const scroll = scrollRef.current
    if (!scroll) return
    if (state.eventCount === 0) atBottomRef.current = true
    if (!atBottomRef.current) return
    scroll.scrollTop = scroll.scrollHeight
    setAtBottom(true)
  }, [loading, state.eventCount, state.timeline.length])
  const onScroll = () => {
    const scroll = scrollRef.current
    if (!scroll) return
    const next = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight <= 28
    atBottomRef.current = next
    setAtBottom(next)
  }
  const scrollToBottom = () => {
    const scroll = scrollRef.current
    if (!scroll) return
    scroll.scrollTop = scroll.scrollHeight
    atBottomRef.current = true
    setAtBottom(true)
  }
  if (loading) return <div className="conversation-scroll"><div className="conversation-loading"><LoaderCircle size={16} />{t("common.loading")}</div></div>
  const empty = !state.error && state.timeline.length === 0
  const missingGrant = Boolean(state.error && /gateway|grant|credential|model_authority/iu.test(`${state.error.code} ${state.error.message}`))
  const messages = new Map(state.messages.map((message) => [message.id, message]))
  const reasoning = new Map(state.reasoning.map((message) => [message.id, message]))
  const tools = new Map(state.tools.map((tool) => [tool.id, tool]))
  const busy = ["connecting", "running", "cancelling"].includes(state.status)
  return <div ref={scrollRef} className="conversation-scroll" data-conversation-scroll onScroll={onScroll}><div className="conversation-timeline" data-conversation-timeline>
    {truncated ? <div className="notice-banner">{t("browser.historyTruncated")}</div> : null}
    {empty ? <div className="browser-welcome"><div className="welcome-mark"><Sparkles size={22} /></div><h1>{t("browser.welcomeTitle")}</h1><p>{t("browser.welcomeDescription")}</p></div> : null}
    {state.timeline.map((item) => {
      const key = `${item.kind}:${item.id}`
      if (item.kind === "message") {
        const message = messages.get(item.id)
        return message ? <ConversationMessageItem key={key} message={message} running={busy} /> : null
      }
      if (item.kind === "reasoning") {
        const message = reasoning.get(item.id)
        return message ? <ReasoningItem key={key} message={message} /> : null
      }
      if (item.kind === "tool") {
        const tool = tools.get(item.id)
        return tool ? <ToolCallItem key={key} tool={tool} runStatus={state.status} /> : null
      }
      if (item.kind === "approval") {
        const approval = state.approvals[item.id]
        return approval ? <ApprovalAuditItem key={key} approval={approval} /> : null
      }
      if (item.kind === "tool-result") {
        const tool = tools.get(item.id)
        return tool ? <ToolResultItem key={key} tool={tool} surfaces={ownedSurfaces(state, "tool", item.id)} /> : null
      }
      const surface = state.surfaces[item.id]
      return surface ? <StandaloneSurfaceItem key={key} surface={surface} /> : null
    })}
    {busy && state.timeline.length > 0 ? <div className="turn-activity" role="status"><LoaderCircle size={14} />{t("browser.deepDiving")}</div> : null}
    {state.error ? <div className="run-error"><div className="run-error-title"><XCircle size={15} /><strong>{state.error.code}</strong></div><p>{state.error.message}</p>{missingGrant ? <Button variant="outline" onClick={onConfigure}><KeyRound size={14} />{t("browser.configureGateway")}</Button> : null}</div> : null}
  </div>{!atBottom ? <div className="conversation-to-bottom-slot"><button type="button" onClick={scrollToBottom} aria-label={t("browser.toBottom")}><ArrowDown size={16} /></button></div> : null}</div>
}

function ConversationMessageItem({ message, running }: { message: ConversationMessage; running: boolean }) {
  const { t } = useTranslation()
  const user = message.role === "user"
  const streaming = !message.complete && running
  return <article className={`message ${user ? "message-user" : "message-assistant"}`} data-conversation-kind="message" data-message-role={message.role}>
    <div className="message-body">
      <span className="sr-only">{user ? t("browser.you") : t("browser.assistant")}</span>
      <div className="message-copy"><MarkdownText text={message.text} streaming={streaming} copyLabel={t("common.copy")} copiedLabel={t("common.copied")} /></div>
    </div>
  </article>
}

function ReasoningItem({ message }: { message: ReasoningMessage }) {
  const { t } = useTranslation()
  return <details className="reasoning-card" data-conversation-kind="reasoning" open={!message.complete || undefined}>
    <summary><span className="reasoning-leading"><Sparkles size={14} /></span><span>{message.complete ? t("browser.reasoning") : t("browser.reasoningActive")}</span><ChevronDown className="reasoning-chevron" size={14} /></summary>
    <div className="reasoning-body"><MarkdownText text={message.text} streaming={!message.complete} copyLabel={t("common.copy")} copiedLabel={t("common.copied")} /></div>
  </details>
}

function ToolCallItem({ tool, runStatus }: { tool: ToolView; runStatus: ConversationState["status"] }) {
  const { t } = useTranslation()
  const unsettled = tool.status !== "completed" && tool.status !== "failed"
  const active = unsettled && ["connecting", "running", "cancelling"].includes(runStatus)
  const paused = unsettled && runStatus === "disconnected"
  const interrupted = unsettled && (runStatus === "failed" || runStatus === "cancelled")
  return <details className="tool-card" data-conversation-kind="tool" data-state={active ? "running" : paused ? "paused" : interrupted ? "interrupted" : tool.status} open={active || paused || undefined}>
    <summary className="tool-header">
      <span className="tool-leading"><SquareTerminal size={15} /></span>
      <strong>{friendlyToolName(tool.name)}</strong><span className="tool-separator" />
      <span className="tool-summary">{toolSummary(tool) || t("browser.preparingExecution")}</span>
      <span className="tool-state">{active ? <LoaderCircle size={13} /> : paused ? <Clock3 size={13} /> : interrupted ? <XCircle size={13} /> : <CheckCircle2 size={13} />}<span className="sr-only">{tool.status}</span></span>
      <ChevronDown className="tool-chevron" size={14} />
    </summary>
    <div className="tool-content">
      {tool.arguments ? <div className="tool-io-section"><span>{t("browser.input")}</span><pre>{prettyJSON(tool.arguments)}</pre></div> : null}
      {tool.progress ? <div className="tool-progress">{tool.progress.total && tool.progress.value !== null ? <progress max={tool.progress.total} value={tool.progress.value} /> : <LoaderCircle size={13} />}<span>{tool.progress.message || t("browser.running")}</span></div> : null}
    </div>
  </details>
}

function ToolResultItem({ tool, surfaces }: { tool: ToolView; surfaces: A2UISurface[] }) {
  const { t } = useTranslation()
  const summary = executionSummary(tool, surfaces)
  return <details className="execution-node" data-conversation-kind="execution">
    <summary className="execution-header"><span className="execution-leading"><CheckCircle2 size={15} /></span><strong>{t("browser.execution")}</strong><span className="tool-separator" /><span className="tool-summary">{summary}</span><Badge tone="success">{t("browser.completed")}</Badge><ChevronDown className="tool-chevron" size={14} /></summary>
    <div className="execution-body">
      {surfaces.length ? surfaces.map((surface) => <ExecutionSurface key={surface.id} surface={surface} />) : <RawExecutionResult result={tool.result} />}
    </div>
  </details>
}

function ApprovalAuditItem({ approval }: { approval: ApprovalView }) {
  const { t } = useTranslation()
  if (approval.status === "pending") return null
  const success = approval.status === "approved" || approval.status === "consumed"
  const danger = approval.status === "denied" || approval.status === "expired" || approval.status === "cancelled"
  return <div className="approval-audit" data-conversation-kind="approval" data-state={approval.status}>
    <span className="approval-audit-icon">{success ? <ShieldCheck size={15} /> : <XCircle size={15} />}</span>
    <strong>{t("browser.approval")}</strong><span className="tool-separator" /><span className="approval-audit-summary">{friendlyToolName(approval.toolName)} · {shortID(approval.executionId)}</span>
    <Badge tone={success ? "success" : danger ? "danger" : "neutral"}>{approvalStatusLabel(t, approval.status)}</Badge>
  </div>
}

function StandaloneSurfaceItem({ surface }: { surface: A2UISurface }) {
  const { t } = useTranslation()
  return <details className="execution-node" data-conversation-kind="execution">
    <summary className="execution-header"><span className="execution-leading"><Code2 size={15} /></span><strong>{t("browser.execution")}</strong><span className="tool-separator" /><span className="tool-summary">{surfaceSummary(surface) || t("browser.a2ui")}</span><ChevronDown className="tool-chevron" size={14} /></summary>
    <div className="execution-body"><ExecutionSurface surface={surface} /></div>
  </details>
}

function RawExecutionResult({ result }: { result: string }) {
  const { t } = useTranslation()
  return <div className="terminal-card"><div className="terminal-toolbar"><span className="terminal-dots"><i /><i /><i /></span><span>{t("browser.output")}</span></div><pre>{result || t("browser.noOutput")}</pre></div>
}

function ExecutionSurface({ surface }: { surface: A2UISurface }) {
  const { t } = useTranslation()
  const presentation = commandPresentation(surface)
  if (presentation) return <div className="terminal-card">
    <div className="terminal-toolbar"><span className="terminal-dots"><i /><i /><i /></span><code>{presentation.command || t("browser.command")}</code></div>
    <pre>{presentation.output || t("browser.noOutput")}</pre>
    {presentation.status ? <div className="terminal-status"><CheckCircle2 size={12} />{presentation.status}</div> : null}
  </div>
  return <div className="a2ui-generic"><div className="surface-label"><Code2 size={14} />{t("browser.a2ui")}</div><Surface surface={surface} componentId="root" ancestors={new Set()} depth={0} /></div>
}

function ownedSurfaces(state: ConversationState, kind: "tool" | "approval", id: string): A2UISurface[] {
  return state.surfaceOrder.map((surfaceId) => state.surfaces[surfaceId]).filter((surface): surface is A2UISurface => surface?.owner?.kind === kind && surface.owner.id === id)
}

function commandPresentation(surface: A2UISurface): { command: string; output: string; status: string } | null {
  if (!surface.dataModel || typeof surface.dataModel !== "object" || Array.isArray(surface.dataModel)) return null
  const model = surface.dataModel as Record<string, unknown>
  const command = typeof model.command === "string" ? model.command : ""
  const output = typeof model.output === "string" ? model.output : ""
  const status = typeof model.status === "string" ? model.status : ""
  return command || output || status ? { command, output, status } : null
}

function surfaceSummary(surface: A2UISurface): string {
  const presentation = commandPresentation(surface)
  return presentation?.status || presentation?.command || ""
}

function executionSummary(tool: ToolView, surfaces: A2UISurface[]): string {
  for (const surface of surfaces) {
    const summary = surfaceSummary(surface)
    if (summary) return summary
  }
  const first = tool.result.trim().split("\n", 1)[0] ?? ""
  return first || friendlyToolName(tool.name)
}

function friendlyToolName(name: string): string {
  const leaf = name.split(/[./]/u).filter(Boolean).at(-1) ?? name
  return leaf.replace(/[_-]+/gu, " ").replace(/\b\w/gu, (value) => value.toLocaleUpperCase())
}

function toolSummary(tool: ToolView): string {
  if (tool.progress?.message) return tool.progress.message
  if (!tool.arguments) return ""
  try {
    const value = JSON.parse(tool.arguments) as unknown
    if (value && typeof value === "object" && !Array.isArray(value)) {
      const command = (value as Record<string, unknown>).command
      if (typeof command === "string") return command
      if (Array.isArray(command)) return command.filter((part): part is string => typeof part === "string").join(" ")
    }
  } catch { /* preserve the bounded raw preview below */ }
  return tool.arguments.length > 160 ? `${tool.arguments.slice(0, 157)}…` : tool.arguments
}

function approvalStatusLabel(t: (key: string) => string, status: ApprovalView["status"]): string {
  const keys: Record<ApprovalView["status"], string> = {
    pending: "browser.approvalPending", approved: "browser.approvalApproved", denied: "browser.approvalDenied",
    expired: "browser.approvalExpired", cancelled: "browser.approvalCancelled", consumed: "browser.approvalConsumed",
  }
  return t(keys[status])
}

function Surface({ surface, componentId, ancestors, depth }: { surface: A2UISurface; componentId: string; ancestors: Set<string>; depth: number }): ReactNode {
  const { t } = useTranslation()
  if (depth > 32 || ancestors.has(componentId)) return <div>{t("browser.invalidSurface")}</div>
  const component = surface.components.find((item) => item.id === componentId)
  if (!component) return null
  const next = new Set(ancestors); next.add(componentId)
  if (component.component === "Card") return <div className="a2ui-card"><Surface surface={surface} componentId={component.child ?? ""} ancestors={next} depth={depth + 1} /></div>
  if (component.component === "Column") return <div className="a2ui-column">{component.children?.map((child) => <Surface key={child} surface={surface} componentId={child} ancestors={next} depth={depth + 1} />)}</div>
  const raw = component.text && typeof component.text === "object" && "path" in component.text && typeof (component.text as { path?: unknown }).path === "string" ? resolveJSONPointer(surface.dataModel, (component.text as { path: string }).path) : component.text
  const value = raw == null ? "" : typeof raw === "string" ? raw : JSON.stringify(raw, null, 2)
  return value.includes("\n") ? <pre>{value}</pre> : <p>{value}</p>
}

function Composer({ centered, value, onChange, onSubmit, onCancel, onReconnect, state, approval, onDecision, inputRef, session, activeRun, permissionModeUpdating, onPermissionModeChange, workingDirectoryUpdating, onWorkingDirectoryChange }: { centered: boolean; value: string; onChange: (value: string) => void; onSubmit: (event: FormEvent) => Promise<void>; onCancel: () => Promise<void>; onReconnect: () => void; state: ConversationState; approval: ApprovalView | null; onDecision: (approval: ApprovalView, decision: "approve" | "deny") => Promise<void>; inputRef: React.RefObject<HTMLTextAreaElement | null>; session: UserSession | null; activeRun: ActiveRun | null; permissionModeUpdating: boolean; onPermissionModeChange: (mode: PermissionMode) => void; workingDirectoryUpdating: boolean; onWorkingDirectoryChange: (environmentId: string, directory: string) => void }) {
  const { t } = useTranslation()
  const busy = ["connecting", "running", "cancelling"].includes(state.status)
  useEffect(() => {
    const input = inputRef.current
    if (!input) return
    input.style.height = "auto"
    input.style.height = `${Math.min(input.scrollHeight, 280)}px`
  }, [inputRef, value])
  const keyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => { if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); event.currentTarget.form?.requestSubmit() } }
  const selector = <PermissionModeSelector session={session} activeRun={activeRun} updating={permissionModeUpdating} onChange={onPermissionModeChange} />
  const directoryControl = <WorkingDirectoryControl session={session} activeRun={activeRun} updating={workingDirectoryUpdating} onSave={onWorkingDirectoryChange} />
  if (approval) return <div className="composer-wrap approval-composer-wrap"><ApprovalPanel approval={approval} onDecision={onDecision} /><div className="approval-session-controls">{directoryControl}{selector}</div><p className="composer-note">{t("browser.disclaimer")}</p></div>
  return <div className={`composer-wrap${centered ? " composer-centered" : ""}`}><form className="composer" onSubmit={(event) => void onSubmit(event)}><Textarea className="composer-input" ref={inputRef} rows={1} value={value} disabled={busy} onChange={(event) => onChange(event.target.value)} onKeyDown={keyDown} placeholder={t("browser.prompt")} aria-label={t("browser.prompt")} /><div className="composer-footer"><span className={`run-status status-${state.status}`}>{statusLabel(t, state.status)}</span><div className="composer-footer-actions">{directoryControl}{selector}{state.status === "disconnected" ? <Button type="button" variant="outline" size="sm" onClick={onReconnect}><RefreshCw size={14} />{t("browser.reconnect")}</Button> : null}{busy && state.runId ? <Button type="button" size="icon" onClick={() => void onCancel()} aria-label={t("browser.stop")}><CircleStop size={17} /></Button> : <Button type="submit" size="icon" disabled={!value.trim() || busy} aria-label={t("browser.send")}><Send size={17} /></Button>}</div></div></form><p className="composer-note">{t("browser.disclaimer")}</p></div>
}

function WorkingDirectoryControl({ session, activeRun, updating, onSave }: { session: UserSession | null; activeRun: ActiveRun | null; updating: boolean; onSave: (environmentId: string, directory: string) => void }) {
  const { t } = useTranslation()
  const [environmentId, setEnvironmentId] = useState("")
  const [directory, setDirectory] = useState(".")
  useEffect(() => {
    setEnvironmentId(session?.environmentId ?? "")
    setDirectory(session?.workingDirectory ?? ".")
  }, [session?.sessionId, session?.workingDirectoryVersion, session?.environmentId, session?.workingDirectory])
  const canSave = Boolean(session && !updating && directory.trim())
  const save = () => { if (canSave) onSave(environmentId, directory) }
  const keyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Enter" && event.target instanceof HTMLInputElement && !event.nativeEvent.isComposing) { event.preventDefault(); save() }
  }
  const summary = session?.environmentId ? `${shortID(session.environmentId)}:${session.workingDirectory}` : (session?.workingDirectory ?? ".")
  return <details className="working-directory-control">
    <summary title={t("browser.workingDirectoryHelp")}><FolderOpen size={14} aria-hidden="true" /><span>{summary}</span></summary>
    <div className="working-directory-popover" onKeyDown={keyDown}>
      <label><span>{t("browser.environmentId")}</span><Input value={environmentId} onChange={(event) => setEnvironmentId(event.target.value)} placeholder={t("browser.environmentIdPlaceholder")} disabled={!session || updating} /></label>
      <label><span>{t("browser.workingDirectory")}</span><Input value={directory} onChange={(event) => setDirectory(event.target.value)} placeholder="." disabled={!session || updating} /></label>
      <div className="working-directory-actions"><small>{activeRun ? t("browser.workingDirectoryNextRun") : t("browser.workingDirectoryHelp")}</small><Button type="button" size="sm" disabled={!canSave} onClick={save}>{updating ? <LoaderCircle className="spin" size={13} /> : null}{t("common.save")}</Button></div>
    </div>
  </details>
}

function PermissionModeSelector({ session, activeRun, updating, onChange }: { session: UserSession | null; activeRun: ActiveRun | null; updating: boolean; onChange: (mode: PermissionMode) => void }) {
  const { t } = useTranslation()
  const mode = session?.permissionMode ?? "read-only"
  const activeMode = activeRun?.permissionMode
  const label = (value: PermissionMode) => value === "auto" ? t("browser.permissionAuto") : value === "full-access" ? t("browser.permissionFullAccess") : t("browser.permissionReadOnly")
  return <div className="permission-mode-control" title={t("browser.permissionModeHelp")}>
    <ShieldCheck size={14} aria-hidden="true" />
    <label className="permission-mode-label" htmlFor="permission-mode-select"><span>{t("browser.permissionMode")}</span>{activeMode && activeMode !== mode ? <small>{t("browser.permissionCurrentRun")}: {label(activeMode)} · {t("browser.permissionNextTurn")}: {label(mode)}</small> : null}</label>
    <NativeSelect id="permission-mode-select" aria-label={t("browser.permissionMode")} value={mode} disabled={!session || updating} onChange={(event) => onChange(event.target.value as PermissionMode)}>
      <option value="read-only">{label("read-only")}</option>
      <option value="auto">{label("auto")}</option>
      <option value="full-access">{label("full-access")}</option>
    </NativeSelect>
    {updating ? <LoaderCircle className="spin" size={14} aria-label={t("common.loading")} /> : null}
  </div>
}

function ApprovalPanel({ approval, onDecision }: { approval: ApprovalView; onDecision: (approval: ApprovalView, decision: "approve" | "deny") => Promise<void> }) {
  const { t } = useTranslation()
  const [submitting, setSubmitting] = useState<"approve" | "deny" | "">("")
  useEffect(() => { setSubmitting("") }, [approval.approvalId, approval.version])
  const decideApproval = async (decision: "approve" | "deny") => {
    setSubmitting(decision)
    try { await onDecision(approval, decision) } catch { setSubmitting("") }
  }
  return <section className="approval-panel" aria-live="polite">
    <div className="approval-strip"><Clock3 size={14} /><span>{t("browser.approvalWaiting")}</span></div>
    <div className="approval-panel-body"><div className="approval-panel-title"><span className="approval-panel-icon"><KeyRound size={17} /></span><div><h2>{t("browser.approval")}</h2><p>{t("browser.approvalDescription", { tool: friendlyToolName(approval.toolName) })}</p></div></div><code>{approval.toolName} · {shortID(approval.executionId)}</code></div>
    <div className="approval-panel-actions"><Button variant="outline" disabled={Boolean(submitting)} onClick={() => void decideApproval("deny")}>{submitting === "deny" ? <LoaderCircle className="spin" size={14} /> : null}{t("browser.deny")}</Button><Button disabled={Boolean(submitting)} onClick={() => void decideApproval("approve")}>{submitting === "approve" ? <LoaderCircle className="spin" size={14} /> : <ShieldCheck size={14} />}{t("browser.approve")}</Button></div>
  </section>
}

function LabelledInput({ label, value, onChange, placeholder }: { label: string; value: string; onChange: (value: string) => void; placeholder?: string }) {
  return <label className="auth-input-label"><span>{label}</span><Input value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} required /></label>
}

function workspaceFromLocation(pathname: string, search: string): string {
  const route = /^\/workspaces\/([0-9a-f-]{36})\/?$/u.exec(pathname)?.[1]
  const query = new URLSearchParams(search).get("workspace") ?? ""
  for (const value of [route, query]) { if (value) { try { return canonicalID("workspace ID", value) } catch { /* try the next source */ } } }
  return ""
}

function prettyJSON(raw: string): string { try { return JSON.stringify(JSON.parse(raw), null, 2) } catch { return raw } }
export function mergeTrajectoryTailRecords(current: SessionTrajectoryRecord[], tail: SessionTrajectoryRecord[]): SessionTrajectoryRecord[] {
  const tailIDs = new Set(tail.map((record) => record.id))
  return [...current.filter((record) => !tailIDs.has(record.id)), ...tail]
}
function trajectoryWindowsOverlap(current: SessionTrajectoryRecord[], tail: SessionTrajectoryRecord[]): boolean {
  const currentIDs = new Set(current.map((record) => record.id))
  return tail.some((record) => currentIDs.has(record.id))
}
export function prependTrajectoryRecords(earlier: SessionTrajectoryRecord[], current: SessionTrajectoryRecord[]): SessionTrajectoryRecord[] {
  const currentIDs = new Set(current.map((record) => record.id))
  return [...earlier.filter((record) => !currentIDs.has(record.id)), ...current]
}
function statusLabel(t: (key: string) => string, status: ConversationState["status"]): string {
  if (status === "idle") return ""
  const key: Record<string, string> = { connecting: "browser.connecting", running: "browser.running", cancelling: "browser.running", completed: "browser.completed", failed: "browser.failed", cancelled: "browser.cancelled", disconnected: "browser.disconnected" }
  return t(key[status] ?? "")
}
