import { Archive, Bot, Boxes, ChevronRight, CircleUserRound, Home, KeyRound, Network, Pencil, Plus, RefreshCw, Search, Settings2, Users, Workflow } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import { Navigate, Route, Routes, useLocation, useNavigate, useParams } from "react-router-dom"
import {
  AppShell,
  Badge,
  Button,
  Card,
  Dialog,
  DialogClose,
  DialogContent,
  DialogTrigger,
  EmptyState,
  Input,
  Label,
  NativeSelect,
  PageHeader,
  ResourceAPI,
  SidebarBrand,
  SidebarNavButton,
  SidebarSearchButton,
  SidebarSection,
  SignedOutShell,
  WorkspaceSwitcher,
  boundedText,
  canonicalID,
  formatDate,
  newID,
  randomSecret,
  safeError,
  shortID,
  useAuth,
  useLocale,
  type EnrollmentToken,
  type Executor,
  type LLMGateway,
  type Workspace,
  type WorkspaceMember,
} from "@agentserver/v2-web-shared"
import { buildGatewayRequest, buildGatewayUpdateRequest, callbackState, gatewayBrowserBinding, gatewayCallbackChannelName, gatewayTone, validateGatewayCallback } from "./gateway-oauth"

type WorkspaceSection = "overview" | "members" | "executors" | "gateways"

export function PlatformApp() {
  const { t } = useTranslation()
  const auth = useAuth()
  if (auth.status === "loading") return <SignedOutShell title={t("auth.preparing")} description={t("auth.platformDescription")}><div className="skeleton skeleton-auth" /></SignedOutShell>
  if (auth.status !== "signed-in") return <SignedOutShell title={t("auth.platformTitle")} description={t("auth.platformDescription")} error={auth.error || undefined}>
    <Button size="lg" disabled={!auth.config} onClick={() => void auth.signIn("", window.location.pathname)}>{t("auth.continue")}<ChevronRight size={16} /></Button>
  </SignedOutShell>
  return <AuthenticatedPlatform token={auth.token} onSignOut={auth.signOut} onReauthorize={(returnPath) => auth.signIn("", returnPath)} />
}

function AuthenticatedPlatform({ token, onSignOut, onReauthorize }: { token: string; onSignOut: () => void; onReauthorize: (returnPath: string) => Promise<void> }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const location = useLocation()
  const api = useMemo(() => new ResourceAPI(window.location.origin, token), [token])
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [pendingWorkspaceGrant, setPendingWorkspaceGrant] = useState("")
  const routeWorkspaceID = /^\/workspaces\/([0-9a-f-]{36})(?:\/|$)/u.exec(location.pathname)?.[1] ?? ""
  const activeWorkspace = workspaces.find((workspace) => workspace.workspaceId === routeWorkspaceID) ?? null

  const loadWorkspaces = useCallback(async () => {
    setLoading(true)
    setError("")
    try { setWorkspaces([...(await api.listWorkspaces())]) } catch (requestError) { setError(safeError(requestError)) } finally { setLoading(false) }
  }, [api])

  useEffect(() => { void loadWorkspaces() }, [loadWorkspaces])

  const replaceWorkspace = useCallback((workspace: Workspace) => {
    setWorkspaces((current) => current.map((item) => item.workspaceId === workspace.workspaceId ? workspace : item))
  }, [])

  const commands = useMemo(() => [
    { id: "home", label: t("nav.home"), keywords: "dashboard", icon: <Home size={16} />, run: () => navigate("/") },
    { id: "workspaces", label: t("nav.workspaces"), icon: <Boxes size={16} />, run: () => navigate("/workspaces") },
    ...workspaces.map((workspace) => ({ id: workspace.workspaceId, label: workspace.name, keywords: workspace.workspaceId, icon: <Workflow size={16} />, run: () => navigate(`/workspaces/${workspace.workspaceId}/overview`) })),
  ], [navigate, t, workspaces])

  const sidebar = <>
    <SidebarBrand title={t("platform.title")} subtitle="Platform" onClick={() => navigate("/")} />
    <WorkspaceSwitcher value={activeWorkspace?.workspaceId ?? ""} label={t("platform.workspaceSwitcher")} items={workspaces.map((workspace) => ({ id: workspace.workspaceId, name: workspace.name }))} onChange={(id) => navigate(`/workspaces/${id}/overview`)} />
    <SidebarSearchButton />
    <SidebarSection>
      <SidebarNavButton icon={<Home size={17} />} label={t("nav.home")} active={location.pathname === "/"} onClick={() => navigate("/")} />
      <SidebarNavButton icon={<Boxes size={17} />} label={t("nav.workspaces")} active={location.pathname === "/workspaces"} onClick={() => navigate("/workspaces")} />
    </SidebarSection>
    {activeWorkspace ? <SidebarSection label={activeWorkspace.name}>
      <SidebarNavButton icon={<Settings2 size={17} />} label={t("nav.overview")} active={location.pathname.endsWith("/overview") || location.pathname === `/workspaces/${activeWorkspace.workspaceId}`} onClick={() => navigate(`/workspaces/${activeWorkspace.workspaceId}/overview`)} />
      {activeWorkspace.currentUserRole !== "viewer" ? <><SidebarNavButton icon={<Users size={17} />} label={t("nav.members")} active={location.pathname.endsWith("/members")} onClick={() => navigate(`/workspaces/${activeWorkspace.workspaceId}/members`)} />
      <SidebarNavButton icon={<Network size={17} />} label={t("nav.executors")} active={location.pathname.endsWith("/executors")} onClick={() => navigate(`/workspaces/${activeWorkspace.workspaceId}/executors`)} />
      <SidebarNavButton icon={<KeyRound size={17} />} label={t("nav.gateways")} active={location.pathname.endsWith("/gateways")} onClick={() => navigate(`/workspaces/${activeWorkspace.workspaceId}/gateways`)} /></> : null}
    </SidebarSection> : null}
  </>

  return <AppShell sidebar={sidebar} commands={commands} accountLabel={`${workspaces.length} ${t("nav.workspaces")}`} onSignOut={onSignOut}>
    <Routes>
      <Route path="/" element={<WorkspaceHome workspaces={workspaces} loading={loading} error={error} api={api} onCreated={(workspace) => {
        setWorkspaces((current) => [workspace, ...current.filter((item) => item.workspaceId !== workspace.workspaceId)])
        setPendingWorkspaceGrant(workspace.workspaceId)
        navigate(`/workspaces/${workspace.workspaceId}/overview`)
      }} onRefresh={loadWorkspaces} />} />
      <Route path="/workspaces" element={<WorkspaceDirectory workspaces={workspaces} loading={loading} error={error} onSelect={(id) => navigate(`/workspaces/${id}/overview`)} onRefresh={loadWorkspaces} />} />
      <Route path="/workspaces/:workspaceId" element={<WorkspaceRedirect />} />
      <Route path="/workspaces/:workspaceId/:section" element={<WorkspaceRoute workspaces={workspaces} loading={loading} api={api} pendingGrant={pendingWorkspaceGrant === routeWorkspaceID} onReauthorize={() => onReauthorize(location.pathname)} replaceWorkspace={replaceWorkspace} onArchived={loadWorkspaces} />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  </AppShell>
}

function WorkspaceRedirect() {
  const { workspaceId = "" } = useParams()
  return <Navigate to={`/workspaces/${workspaceId}/overview`} replace />
}

function WorkspaceHome({ workspaces, loading, error, api, onCreated, onRefresh }: { workspaces: Workspace[]; loading: boolean; error: string; api: ResourceAPI; onCreated: (workspace: Workspace) => void; onRefresh: () => Promise<void> }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  return <div className="page"><PageHeader title={t("platform.homeTitle")} description={t("platform.homeDescription")} actions={<><Button variant="outline" size="icon" onClick={() => void onRefresh()} aria-label={t("common.refresh")}><RefreshCw size={16} /></Button><CreateWorkspaceDialog api={api} onCreated={onCreated} /></>} />
    {error ? <div className="error-banner">{error}</div> : null}
    {loading ? <WorkspaceSkeleton /> : workspaces.length === 0 ? <EmptyState icon={<Boxes size={20} />} title={t("platform.noWorkspaces")} description={t("platform.noWorkspacesDescription")} action={<CreateWorkspaceDialog api={api} onCreated={onCreated} />} /> : <div className="workspace-grid">{workspaces.map((workspace) => <button type="button" className="workspace-card" key={workspace.workspaceId} onClick={() => navigate(`/workspaces/${workspace.workspaceId}/overview`)}>
      <span className="workspace-card-mark">{workspace.name.slice(0, 1).toUpperCase()}</span><span className="workspace-card-copy"><strong>{workspace.name}</strong><small>{workspace.currentUserRole} · {shortID(workspace.workspaceId)}</small></span><Badge tone={workspace.status === "active" ? "success" : "neutral"}>{workspace.status}</Badge><ChevronRight size={16} />
    </button>)}</div>}
  </div>
}

function WorkspaceDirectory({ workspaces, loading, error, onSelect, onRefresh }: { workspaces: Workspace[]; loading: boolean; error: string; onSelect: (id: string) => void; onRefresh: () => Promise<void> }) {
  const { t } = useTranslation()
  return <div className="page"><PageHeader title={t("nav.workspaces")} description={t("platform.homeDescription")} actions={<Button variant="outline" onClick={() => void onRefresh()}><RefreshCw size={15} />{t("common.refresh")}</Button>} />
    {error ? <div className="error-banner">{error}</div> : null}
    {loading ? <WorkspaceSkeleton /> : <div className="resource-list">{workspaces.map((workspace) => <Card className="resource-row" key={workspace.workspaceId}><div className="resource-main"><h3>{workspace.name}</h3><p>{workspace.workspaceId}</p><div className="resource-meta"><Badge tone={workspace.status === "active" ? "success" : "neutral"}>{workspace.status}</Badge><Badge>{workspace.currentUserRole}</Badge></div></div><Button variant="outline" onClick={() => onSelect(workspace.workspaceId)}>{t("nav.overview")}<ChevronRight size={14} /></Button></Card>)}</div>}
  </div>
}

function CreateWorkspaceDialog({ api, onCreated }: { api: ResourceAPI; onCreated: (workspace: Workspace) => void }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setBusy(true); setError("")
    try {
      const form = new FormData(event.currentTarget)
      const result = await api.createWorkspace({ workspaceId: newID(), name: boundedText("workspace name", String(form.get("name") ?? ""), 256) })
      setOpen(false); onCreated(result.workspace)
    } catch (requestError) { setError(safeError(requestError)) } finally { setBusy(false) }
  }
  return <Dialog open={open} onOpenChange={setOpen}><DialogTrigger asChild><Button><Plus size={16} />{t("platform.createWorkspace")}</Button></DialogTrigger><DialogContent title={t("platform.createWorkspace")} description={t("platform.homeDescription")}><form onSubmit={(event) => void submit(event)}><Label htmlFor="workspace-name">{t("platform.workspaceName")}</Label><Input id="workspace-name" name="name" autoFocus maxLength={256} required />{error ? <div className="error-banner inline-error">{error}</div> : null}<div className="form-actions"><DialogClose asChild><Button type="button" variant="ghost">{t("common.cancel")}</Button></DialogClose><Button type="submit" disabled={busy}>{busy ? t("common.loading") : t("common.create")}</Button></div></form></DialogContent></Dialog>
}

function WorkspaceRoute({ workspaces, loading, api, pendingGrant, onReauthorize, replaceWorkspace, onArchived }: { workspaces: Workspace[]; loading: boolean; api: ResourceAPI; pendingGrant: boolean; onReauthorize: () => Promise<void>; replaceWorkspace: (workspace: Workspace) => void; onArchived: () => Promise<void> }) {
  const { workspaceId = "", section = "overview" } = useParams()
  const { t } = useTranslation()
  if (loading) return <div className="page"><WorkspaceSkeleton /></div>
  try { canonicalID("workspace ID", workspaceId) } catch { return <Navigate to="/workspaces" replace /> }
  const workspace = workspaces.find((item) => item.workspaceId === workspaceId)
  if (!workspace) return <div className="page"><EmptyState title={t("platform.noWorkspaces")} action={<Button asChild><a href="/workspaces">{t("nav.workspaces")}</a></Button>} /></div>
  if (!(["overview", "members", "executors", "gateways"] as string[]).includes(section)) return <Navigate to={`/workspaces/${workspaceId}/overview`} replace />
  const shared = { workspace, api }
  return <div className="page">
    {pendingGrant ? <div className="notice-banner"><span>{t("platform.newGrantNotice")}</span><Button size="sm" variant="outline" onClick={() => void onReauthorize()}>{t("common.reauthorize")}</Button></div> : null}
    {section === "overview" ? <WorkspaceOverview {...shared} onChanged={replaceWorkspace} onArchived={onArchived} /> : null}
    {section === "members" ? <MembersPage {...shared} /> : null}
    {section === "executors" ? <ExecutorsPage {...shared} /> : null}
    {section === "gateways" ? <GatewaysPage {...shared} /> : null}
  </div>
}

function WorkspaceOverview({ workspace, api, onChanged, onArchived }: { workspace: Workspace; api: ResourceAPI; onChanged: (workspace: Workspace) => void; onArchived: () => Promise<void> }) {
  const { t } = useTranslation()
  const { locale } = useLocale()
  const navigate = useNavigate()
  const [name, setName] = useState(workspace.name)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  useEffect(() => setName(workspace.name), [workspace.name])
  const rename = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError("")
    try { onChanged((await api.updateWorkspace(workspace.workspaceId, { name: boundedText("workspace name", name, 256), expectedVersion: workspace.version })).workspace) } catch (requestError) { setError(safeError(requestError)) } finally { setBusy(false) }
  }
  const archive = async () => {
    if (!window.confirm(t("platform.archiveWorkspaceConfirm"))) return
    setBusy(true); setError("")
    try { await api.archiveWorkspace(workspace.workspaceId, workspace.version); await onArchived(); navigate("/workspaces") } catch (requestError) { setError(safeError(requestError)); setBusy(false) }
  }
  return <><PageHeader eyebrow={shortID(workspace.workspaceId)} title={workspace.name} description={`${workspace.currentUserRole} · ${workspace.status}`} actions={<Button onClick={() => { window.location.href = `https://browser.byted.bps.dev/workspaces/${workspace.workspaceId}` }}><Bot size={16} />{t("platform.openBrowser")}</Button>} />
    {error ? <div className="error-banner">{error}</div> : null}
    <div className="facts-grid"><Fact label={t("platform.workspaceId")} value={workspace.workspaceId} /><Fact label={t("platform.yourRole")} value={workspace.currentUserRole} /><Fact label={t("common.status")} value={workspace.status} /><Fact label={t("common.version")} value={String(workspace.version)} /><Fact label={t("common.created")} value={formatDate(workspace.createdAt, locale)} /><Fact label={t("common.updated")} value={formatDate(workspace.updatedAt, locale)} /></div>
    {workspace.currentUserRole === "owner" && workspace.status === "active" ? <Card className="settings-card"><div><h2>{t("platform.renameWorkspace")}</h2><p>{t("platform.homeDescription")}</p></div><form onSubmit={(event) => void rename(event)}><Input value={name} maxLength={256} onChange={(event) => setName(event.target.value)} /><Button type="submit" disabled={busy || name === workspace.name}>{t("common.save")}</Button></form><div className="danger-zone"><div><h3>{t("platform.archiveWorkspace")}</h3><p>{t("platform.archiveWorkspaceConfirm")}</p></div><Button variant="destructive" disabled={busy} onClick={() => void archive()}><Archive size={15} />{t("common.archive")}</Button></div></Card> : null}
  </>
}

function MembersPage({ workspace, api }: { workspace: Workspace; api: ResourceAPI }) {
  const { t } = useTranslation()
  const [members, setMembers] = useState<WorkspaceMember[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const load = useCallback(async () => { setLoading(true); setError(""); try { setMembers([...(await api.listMembers(workspace.workspaceId))]) } catch (requestError) { setError(safeError(requestError)) } finally { setLoading(false) } }, [api, workspace.workspaceId])
  useEffect(() => { void load() }, [load])
  const add = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setError("")
    const formElement = event.currentTarget
    try { const form = new FormData(formElement); await api.addMember(workspace.workspaceId, { userId: canonicalID("user ID", String(form.get("userId") ?? "").trim()), role: String(form.get("role")) as "owner" | "developer" | "viewer" }); formElement.reset(); await load() } catch (requestError) { setError(safeError(requestError)) }
  }
  const update = async (member: WorkspaceMember, role: WorkspaceMember["role"]) => { try { await api.updateMember(workspace.workspaceId, member.userId, { role, expectedVersion: member.version }); await load() } catch (requestError) { setError(safeError(requestError)) } }
  const remove = async (member: WorkspaceMember) => { if (!window.confirm(t("members.removeConfirm"))) return; try { await api.removeMember(workspace.workspaceId, member.userId); await load() } catch (requestError) { setError(safeError(requestError)) } }
  const owner = workspace.currentUserRole === "owner" && workspace.status === "active"
  return <><PageHeader title={t("members.title")} description={t("members.description")} actions={<Button variant="outline" onClick={() => void load()}><RefreshCw size={15} />{t("common.refresh")}</Button>} />{error ? <div className="error-banner">{error}</div> : null}
    {owner ? <Card className="inline-create"><form onSubmit={(event) => void add(event)}><div><Label htmlFor="member-id">{t("members.userId")}</Label><Input id="member-id" name="userId" placeholder="00000000-0000-4000-8000-000000000000" required /></div><div><Label htmlFor="member-role">{t("members.role")}</Label><NativeSelect id="member-role" name="role" defaultValue="developer"><option value="owner">{t("members.owner")}</option><option value="developer">{t("members.developer")}</option><option value="viewer">{t("members.viewer")}</option></NativeSelect></div><Button type="submit"><Plus size={15} />{t("members.add")}</Button></form></Card> : null}
    {loading ? <WorkspaceSkeleton /> : members.length === 0 ? <EmptyState icon={<Users size={20} />} title={t("members.empty")} /> : <div className="resource-list">{members.map((member) => <Card className="resource-row" key={member.userId}><div className="resource-main"><h3>{shortID(member.userId)}</h3><p>{member.userId}</p><div className="resource-meta"><Badge>{member.role}</Badge><span className="resource-version">v{member.version}</span></div></div>{owner ? <div className="resource-actions"><NativeSelect aria-label={t("members.role")} value={member.role} onChange={(event) => void update(member, event.target.value as WorkspaceMember["role"])}><option value="owner">{t("members.owner")}</option><option value="developer">{t("members.developer")}</option><option value="viewer">{t("members.viewer")}</option></NativeSelect><Button variant="ghost" size="sm" onClick={() => void remove(member)}>{t("common.remove")}</Button></div> : null}</Card>)}</div>}
  </>
}

function ExecutorsPage({ workspace, api }: { workspace: Workspace; api: ResourceAPI }) {
  const { t } = useTranslation()
  const { locale } = useLocale()
  const [executors, setExecutors] = useState<Executor[]>([])
  const [token, setToken] = useState<EnrollmentToken | null>(null)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const load = useCallback(async () => { setLoading(true); setError(""); try { setExecutors([...(await api.listExecutors(workspace.workspaceId))]) } catch (requestError) { setError(safeError(requestError)) } finally { setLoading(false) } }, [api, workspace.workspaceId])
  useEffect(() => { void load() }, [load])
  const create = async () => { setError(""); try { await api.createExecutor(workspace.workspaceId, newID()); await load() } catch (requestError) { setError(safeError(requestError)) } }
  const issue = async (executor: Executor) => { setError(""); try { setToken(await api.issueEnrollmentToken(workspace.workspaceId, executor.executorId, randomSecret("enroll"))) } catch (requestError) { setError(safeError(requestError)) } }
  const archive = async (executor: Executor) => { if (!window.confirm(t("executors.archiveConfirm"))) return; try { await api.archiveExecutor(workspace.workspaceId, executor.executorId); await load() } catch (requestError) { setError(safeError(requestError)) } }
  const owner = workspace.currentUserRole === "owner" && workspace.status === "active"
  return <><PageHeader title={t("executors.title")} description={t("executors.description")} actions={<>{owner ? <Button onClick={() => void create()}><Plus size={15} />{t("executors.create")}</Button> : null}<Button variant="outline" size="icon" onClick={() => void load()} aria-label={t("common.refresh")}><RefreshCw size={15} /></Button></>} />{error ? <div className="error-banner">{error}</div> : null}
    {loading ? <WorkspaceSkeleton /> : executors.length === 0 ? <EmptyState icon={<Network size={20} />} title={t("executors.empty")} /> : <div className="resource-list">{executors.map((executor) => <Card className="resource-row" key={executor.executorId}><div className="resource-main"><h3>{shortID(executor.executorId)}</h3><p>{executor.executorId}</p><div className="resource-meta"><Badge tone={executor.status === "online" ? "success" : executor.status === "revoked" ? "danger" : "neutral"}>{executor.status}</Badge><span className="resource-version">v{executor.version}</span></div></div>{owner && executor.status !== "revoked" ? <div className="resource-actions"><Button variant="outline" size="sm" onClick={() => void issue(executor)}><KeyRound size={14} />{t("executors.issueToken")}</Button><Button variant="ghost" size="sm" onClick={() => void archive(executor)}>{t("common.archive")}</Button></div> : null}</Card>)}</div>}
    <Dialog open={Boolean(token)} onOpenChange={(open) => { if (!open) setToken(null) }}><DialogContent title={t("executors.enrollmentToken")} description={t("executors.tokenWarning")}><div className="token-box">{token?.token}</div>{token ? <p className="form-help">{t("executors.expires", { date: formatDate(token.expiresAt, locale) })}</p> : null}<div className="form-actions"><Button variant="outline" onClick={() => token && void navigator.clipboard.writeText(token.token)}>{t("common.copy")}</Button><DialogClose asChild><Button>{t("common.close")}</Button></DialogClose></div></DialogContent></Dialog>
  </>
}

interface GatewayTransaction { popup: Window; workspaceId: string; gatewayId: string; browserBinding: string; state: string; expiresAt: number; monitor: number }

function GatewaysPage({ workspace, api }: { workspace: Workspace; api: ResourceAPI }) {
  const { t } = useTranslation()
  const [gateways, setGateways] = useState<LLMGateway[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [creating, setCreating] = useState(false)
  const [waiting, setWaiting] = useState(false)
  const transaction = useRef<GatewayTransaction | null>(null)
  const load = useCallback(async () => { setLoading(true); setError(""); try { setGateways([...(await api.listGateways(workspace.workspaceId))]) } catch (requestError) { setError(safeError(requestError)) } finally { setLoading(false) } }, [api, workspace.workspaceId])
  useEffect(() => { void load() }, [load])

  const clearTransaction = useCallback(() => {
    const current = transaction.current; transaction.current = null; setWaiting(false)
    if (!current) return
    window.clearInterval(current.monitor)
    current.browserBinding = ""; current.state = ""
    try { if (!current.popup.closed) current.popup.close() } catch { /* COOP can sever the window proxy */ }
  }, [])

  const complete = useCallback(async (raw: unknown) => {
    const current = transaction.current
    if (!current) return
    let callback
    try { callback = validateGatewayCallback(raw) } catch { return }
    if (callback.state !== current.state) return
    if ("protocolError" in callback) {
      transaction.current = null; window.clearInterval(current.monitor); setWaiting(false)
      try { if (!current.popup.closed) current.popup.close() } catch { /* COOP can sever the window proxy */ }
      current.browserBinding = ""; current.state = ""; setError(t("gateways.invalidCallback"))
      return
    }
    const providerFailure = callback.providerError ? t("gateways.providerDenied", {
      detail: callback.providerErrorDescription ? `${callback.providerError} — ${callback.providerErrorDescription}` : callback.providerError,
    }) : ""
    transaction.current = null; window.clearInterval(current.monitor); setWaiting(false)
    try {
      try { if (!current.popup.closed) current.popup.close() } catch { /* COOP can sever the window proxy */ }
      await api.completeGatewayAuthorization(current.workspaceId, current.gatewayId, {
        state: callback.state,
        ...(callback.code ? { code: callback.code } : { providerError: callback.providerError }),
        browserBinding: current.browserBinding,
      })
      await load()
    } catch (requestError) { setError(providerFailure || safeError(requestError)) } finally { current.browserBinding = ""; current.state = "" }
  }, [api, load, t])

  useEffect(() => {
    const message = (event: MessageEvent) => { const current = transaction.current; if (current && event.origin === window.location.origin && event.source === current.popup) void complete(event.data) }
    window.addEventListener("message", message)
    let channel: BroadcastChannel | null = null
    try { channel = new BroadcastChannel(gatewayCallbackChannelName); channel.addEventListener("message", (event) => void complete(event.data)) } catch { /* opener messaging remains available */ }
    return () => { window.removeEventListener("message", message); channel?.close(); clearTransaction() }
  }, [clearTransaction, complete])

  const create = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setCreating(true); setError("")
    const formElement = event.currentTarget
    try { await api.createGateway(workspace.workspaceId, buildGatewayRequest(new FormData(formElement), newID())); formElement.reset(); await load() } catch (requestError) { setError(safeError(requestError)) } finally { setCreating(false) }
  }
  const update = async (gateway: LLMGateway, form: FormData) => {
    await api.updateGateway(workspace.workspaceId, gateway.gatewayId, buildGatewayUpdateRequest(form, gateway.version))
    await load()
  }

  const authorize = async (gateway: LLMGateway) => {
    if (transaction.current) return
    const popup = window.open("about:blank", `agentserver-llm-gateway-${gateway.gatewayId}`, "popup,width=560,height=760")
    if (!popup) { setError(t("gateways.popupBlocked")); return }
    const browserBinding = gatewayBrowserBinding()
    setWaiting(true); setError("")
    try {
      const result = await api.beginGatewayAuthorization(workspace.workspaceId, gateway.gatewayId, browserBinding)
      const state = callbackState(result.authorizationUrl, result.expiresAt)
      const current: GatewayTransaction = { popup, workspaceId: workspace.workspaceId, gatewayId: gateway.gatewayId, browserBinding, state, expiresAt: Date.parse(result.expiresAt), monitor: 0 }
      transaction.current = current
      popup.location.replace(result.authorizationUrl)
      current.monitor = window.setInterval(() => { if (transaction.current === current && Date.now() >= current.expiresAt) { clearTransaction(); setError(t("gateways.expired")) } }, 500)
    } catch (requestError) { try { popup.close() } catch { /* ignored */ }; setWaiting(false); setError(safeError(requestError)) }
  }
  const revoke = async (gateway: LLMGateway) => { if (!window.confirm(t("gateways.revokeConfirm"))) return; try { await api.revokeGatewayGrant(workspace.workspaceId, gateway.gatewayId); await load() } catch (requestError) { setError(safeError(requestError)) } }
  const disable = async (gateway: LLMGateway) => { if (!window.confirm(t("gateways.disableConfirm"))) return; try { await api.disableGateway(workspace.workspaceId, gateway.gatewayId); await load() } catch (requestError) { setError(safeError(requestError)) } }
  const owner = workspace.currentUserRole === "owner" && workspace.status === "active"
  const developer = ["owner", "developer"].includes(workspace.currentUserRole) && workspace.status === "active"
  return <><PageHeader title={t("gateways.title")} description={t("gateways.description")} actions={<><AddGatewayDialog owner={owner} busy={creating} onSubmit={create} /><Button variant="outline" size="icon" onClick={() => void load()} aria-label={t("common.refresh")}><RefreshCw size={15} /></Button></>} />
    {error ? <div className="error-banner">{error}</div> : null}{waiting ? <div className="notice-banner"><span>{t("gateways.waiting")}</span><Button size="sm" variant="ghost" onClick={clearTransaction}>{t("common.cancel")}</Button></div> : null}
    {loading ? <WorkspaceSkeleton /> : gateways.length === 0 ? <EmptyState icon={<KeyRound size={20} />} title={t("gateways.empty")} /> : <div className="resource-list">{gateways.map((gateway) => <Card className="resource-row" key={gateway.gatewayId}><div className="resource-main"><h3>{gateway.name} {gateway.default ? <Badge tone="info">{t("gateways.default")}</Badge> : null}</h3><p>{gateway.defaultModel} · {gateway.responsesUrl}</p><div className="resource-meta"><Badge tone={gatewayTone(gateway)}>{gateway.status === "disabled" ? gateway.status : gateway.grantStatus || t("gateways.unlinked")}</Badge><span className="resource-version">v{gateway.version}</span><span className="resource-version">{gateway.oidcIssuer}</span></div></div><div className="resource-actions">{developer && gateway.status === "active" ? <Button size="sm" onClick={() => void authorize(gateway)}>{gateway.grantStatus === "active" ? t("common.reauthorize") : t("common.authorize")}</Button> : null}{developer && gateway.grantStatus && gateway.grantStatus !== "revoked" ? <Button variant="outline" size="sm" onClick={() => void revoke(gateway)}>{t("common.revoke")}</Button> : null}{owner && gateway.status === "active" ? <EditGatewayDialog gateway={gateway} onSave={update} /> : null}{owner && gateway.status === "active" ? <Button variant="ghost" size="sm" onClick={() => void disable(gateway)}>{t("common.disable")}</Button> : null}</div></Card>)}</div>}
  </>
}

function AddGatewayDialog({ owner, busy, onSubmit }: { owner: boolean; busy: boolean; onSubmit: (event: FormEvent<HTMLFormElement>) => void }) {
  const { t } = useTranslation()
  if (!owner) return null
  return <Dialog><DialogTrigger asChild><Button><Plus size={15} />{t("gateways.create")}</Button></DialogTrigger><DialogContent title={t("gateways.create")} description={t("gateways.description")} className="gateway-dialog"><form onSubmit={(event) => onSubmit(event)}><div className="form-grid">
    <Field name="name" label={t("common.name")} required />
    <Field name="model" label={t("gateways.model")} placeholder="gpt-5.4" required />
    <Field name="responsesUrl" label={t("gateways.responsesUrl")} placeholder="https://gateway.example/v1/responses" full required />
    <Field name="issuer" label={t("gateways.issuer")} placeholder="https://gateway.example" full required />
    <Field name="clientId" label={t("gateways.clientId")} required />
    <Field name="scopes" label={t("gateways.scopes")} defaultValue="openid offline_access project:inference" required />
    <div className="form-field"><Label htmlFor="gateway-bearer">{t("gateways.bearer")}</Label><NativeSelect id="gateway-bearer" name="bearer" defaultValue="id_token"><option value="id_token">id_token</option><option value="access_token">access_token</option></NativeSelect></div>
    <label className="checkbox-row"><input type="checkbox" name="makeDefault" defaultChecked />{t("gateways.makeDefault")}</label>
  </div><p className="form-help">{t("gateways.callback")} <code>{new URL("/auth/llm-gateway/callback", window.location.origin).href}</code></p><div className="form-actions"><DialogClose asChild><Button type="button" variant="ghost">{t("common.cancel")}</Button></DialogClose><Button type="submit" disabled={busy}>{busy ? t("common.loading") : t("common.create")}</Button></div></form></DialogContent></Dialog>
}

function EditGatewayDialog({ gateway, onSave }: { gateway: LLMGateway; onSave: (gateway: LLMGateway, form: FormData) => Promise<void> }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const prefix = `gateway-edit-${gateway.gatewayId}`
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setBusy(true); setError("")
    try { await onSave(gateway, new FormData(event.currentTarget)); setOpen(false) } catch (requestError) { setError(safeError(requestError)) } finally { setBusy(false) }
  }
  return <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (!next) setError("") }}><DialogTrigger asChild><Button variant="outline" size="sm"><Pencil size={14} />{t("common.edit")}</Button></DialogTrigger><DialogContent title={`${t("common.edit")} · ${gateway.name}`} description={t("gateways.updateWarning")} className="gateway-dialog"><form onSubmit={(event) => void submit(event)}><div className="form-grid">
    <Field prefix={prefix} name="name" label={t("common.name")} defaultValue={gateway.name} required />
    <Field prefix={prefix} name="model" label={t("gateways.model")} defaultValue={gateway.defaultModel} required />
    <Field prefix={prefix} name="responsesUrl" label={t("gateways.responsesUrl")} defaultValue={gateway.responsesUrl} full required />
    <Field prefix={prefix} name="issuer" label={t("gateways.issuer")} defaultValue={gateway.oidcIssuer} full required />
    <Field prefix={prefix} name="clientId" label={t("gateways.clientId")} defaultValue={gateway.oidcClientId} required />
    <Field prefix={prefix} name="scopes" label={t("gateways.scopes")} defaultValue={gateway.oidcScopes.join(" ")} required />
    <div className="form-field"><Label htmlFor={`${prefix}-bearer`}>{t("gateways.bearer")}</Label><NativeSelect id={`${prefix}-bearer`} name="bearer" defaultValue={gateway.bearerTokenType}><option value="id_token">id_token</option><option value="access_token">access_token</option></NativeSelect></div>
    <label className="checkbox-row"><input type="checkbox" name="makeDefault" defaultChecked={gateway.default} />{t("gateways.makeDefault")}</label>
  </div><p className="form-help">{t("gateways.updateWarning")}</p>{error ? <div className="error-banner inline-error">{error}</div> : null}<div className="form-actions"><DialogClose asChild><Button type="button" variant="ghost">{t("common.cancel")}</Button></DialogClose><Button type="submit" disabled={busy}>{busy ? t("common.loading") : t("common.save")}</Button></div></form></DialogContent></Dialog>
}

function Field({ name, label, full, prefix = "gateway", ...props }: { name: string; label: string; full?: boolean; prefix?: string } & React.InputHTMLAttributes<HTMLInputElement>) {
  const id = `${prefix}-${name}`
  return <div className={`form-field${full ? " full" : ""}`}><Label htmlFor={id}>{label}</Label><Input id={id} name={name} {...props} /></div>
}

function Fact({ label, value }: { label: string; value: string }) { return <Card className="fact-card"><small>{label}</small><strong title={value}>{value}</strong></Card> }
function WorkspaceSkeleton() { return <div className="resource-list"><div className="skeleton skeleton-row" /><div className="skeleton skeleton-row" /><div className="skeleton skeleton-row" /></div> }
