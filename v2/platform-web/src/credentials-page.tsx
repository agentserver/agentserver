import { CheckCircle2, Copy, ExternalLink, KeyRound, Plus, RefreshCw, RotateCw, ShieldCheck, Star, Trash2, Unplug } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import {
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
  boundedText,
  formatDate,
  safeError,
  shortID,
  useLocale,
  type CredentialAuthorization,
  type CredentialProvider,
  type ResourceAPI,
  type Workspace,
  type WorkspaceCredential,
} from "@agentserver/v2-web-shared"
import {
  clearCredentialAuthorization,
  credentialAuthorizationPollDelay,
  credentialAuthorizationTerminal,
  persistCredentialAuthorization,
  restoreCredentialAuthorization,
} from "./credential-flow"

export function CredentialsPage({ workspace, api }: { workspace: Workspace; api: ResourceAPI }) {
  const { t } = useTranslation()
  const { locale } = useLocale()
  const [providers, setProviders] = useState<CredentialProvider[]>([])
  const [bindings, setBindings] = useState<Record<string, WorkspaceCredential[]>>({})
  const [authorization, setAuthorization] = useState<CredentialAuthorization | null>(null)
  const [flowOpen, setFlowOpen] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [busyBinding, setBusyBinding] = useState("")
  const [retryNotBefore, setRetryNotBefore] = useState(0)
  const polling = useRef(false)
  const owner = workspace.currentUserRole === "owner" && workspace.status === "active"

  const load = useCallback(async () => {
    setLoading(true); setError("")
    try {
      const catalog = await api.listCredentialProviders()
      const listed = await Promise.all(catalog.map(async (provider) => [provider.kind, await api.listCredentials(workspace.workspaceId, provider.kind)] as const))
      setProviders(catalog)
      setBindings(Object.fromEntries(listed))
    } catch (requestError) { setError(safeError(requestError)) } finally { setLoading(false) }
  }, [api, workspace.workspaceId])

  useEffect(() => { void load() }, [load])

  useEffect(() => {
    let active = true
    try {
      const reference = restoreCredentialAuthorization(window.sessionStorage, workspace.workspaceId)
      if (!reference) return () => { active = false }
      void api.getCredentialAuthorization(reference.workspaceId, reference.kind, reference.authorizationId).then((result) => {
        if (!active) return
        setAuthorization(result.authorization); setFlowOpen(true)
        if (credentialAuthorizationTerminal(result.authorization.status)) {
          clearCredentialAuthorization(window.sessionStorage, workspace.workspaceId)
          void load()
        }
      }).catch(() => {
        try { clearCredentialAuthorization(window.sessionStorage, workspace.workspaceId) } catch { /* storage is best effort */ }
      })
    } catch { /* storage is best effort */ }
    return () => { active = false }
  }, [api, load, workspace.workspaceId])

  useEffect(() => {
    if (!authorization || credentialAuthorizationTerminal(authorization.status)) return
    const serverDelay = credentialAuthorizationPollDelay(authorization)
    const delay = Math.max(serverDelay, retryNotBefore - Date.now(), 0)
    const timer = window.setTimeout(() => {
      if (polling.current) return
      polling.current = true
      void api.pollCredentialAuthorization(workspace.workspaceId, authorization.kind, authorization.id).then((result) => {
        setRetryNotBefore(0)
        setAuthorization(result.authorization)
        if (credentialAuthorizationTerminal(result.authorization.status)) {
          try { clearCredentialAuthorization(window.sessionStorage, workspace.workspaceId) } catch { /* storage is best effort */ }
          void load()
        }
      }).catch((requestError) => {
        setError(safeError(requestError))
        setRetryNotBefore(Date.now() + 5_000)
      }).finally(() => { polling.current = false })
    }, delay)
    return () => window.clearTimeout(timer)
  }, [api, authorization, load, retryNotBefore, workspace.workspaceId])

  const begin = useCallback(async (provider: CredentialProvider, input: {
    displayName: string; ownerScope: "workspace" | "user"; makeDefault: boolean; binding?: WorkspaceCredential
  }) => {
    if (authorization?.status === "pending") throw new Error(t("credentials.pendingExists"))
    const binding = input.binding
    const result = await api.beginCredentialAuthorization(workspace.workspaceId, provider.kind, {
      displayName: binding?.displayName ?? boundedText("credential name", input.displayName, 256),
      ownerScope: binding?.ownerScope ?? input.ownerScope,
      ...(binding?.ownerUserId ? { ownerUserId: binding.ownerUserId } : {}),
      makeDefault: binding?.isDefault ?? input.makeDefault,
      ...(binding ? {
        bindingId: binding.id,
        expectedAuthorityVersion: binding.authorityVersion,
        expectedCredentialVersion: binding.credentialVersion,
      } : {}),
    })
    setAuthorization(result.authorization); setFlowOpen(true); setRetryNotBefore(0)
    try {
      persistCredentialAuthorization(window.sessionStorage, {
        workspaceId: workspace.workspaceId, kind: provider.kind, authorizationId: result.authorization.id,
      })
    } catch { /* storage is best effort; Core still owns the transaction */ }
  }, [api, authorization?.status, t, workspace.workspaceId])

  const providerByKind = useMemo(() => new Map(providers.map((provider) => [provider.kind, provider])), [providers])
  const mutate = async (binding: WorkspaceCredential, operation: () => Promise<unknown>) => {
    setBusyBinding(binding.id); setError("")
    try { await operation(); await load() } catch (requestError) { setError(safeError(requestError)) } finally { setBusyBinding("") }
  }
  const setDefault = (binding: WorkspaceCredential) => mutate(binding, () => api.setDefaultCredential(workspace.workspaceId, binding.kind, binding.id, binding.authorityVersion))
  const revoke = (binding: WorkspaceCredential) => {
    if (!window.confirm(t("credentials.revokeConfirm", { name: binding.displayName }))) return Promise.resolve()
    return mutate(binding, () => api.revokeCredential(workspace.workspaceId, binding.kind, binding.id, binding.authorityVersion))
  }
  const remove = (binding: WorkspaceCredential) => {
    if (!window.confirm(t("credentials.deleteConfirm", { name: binding.displayName }))) return Promise.resolve()
    return mutate(binding, () => api.deleteCredential(workspace.workspaceId, binding.kind, binding.id, binding.authorityVersion))
  }
  const reauthorize = async (binding: WorkspaceCredential) => {
    const provider = providerByKind.get(binding.kind)
    if (!provider?.authorizationMethods.includes("device_flow")) return
    setBusyBinding(binding.id); setError("")
    try { await begin(provider, { displayName: binding.displayName, ownerScope: binding.ownerScope, makeDefault: binding.isDefault, binding }) } catch (requestError) { setError(safeError(requestError)) } finally { setBusyBinding("") }
  }
  const cancel = async () => {
    if (!authorization || authorization.status !== "pending") return
    setError("")
    try {
      const result = await api.cancelCredentialAuthorization(workspace.workspaceId, authorization.kind, authorization.id, authorization.version)
      setAuthorization(result.authorization)
      try { clearCredentialAuthorization(window.sessionStorage, workspace.workspaceId) } catch { /* storage is best effort */ }
    } catch (requestError) { setError(safeError(requestError)) }
  }

  return <><PageHeader title={t("credentials.title")} description={t("credentials.description")} actions={<Button variant="outline" onClick={() => void load()}><RefreshCw size={15} />{t("common.refresh")}</Button>} />
    {error ? <div className="error-banner">{error}</div> : null}
    {authorization?.status === "pending" && !flowOpen ? <div className="notice-banner"><span>{t("credentials.authorizationPending", { provider: providerByKind.get(authorization.kind)?.displayName ?? authorization.kind })}</span><Button size="sm" onClick={() => setFlowOpen(true)}>{t("credentials.continueAuthorization")}</Button></div> : null}
    {!owner ? <EmptyState icon={<ShieldCheck size={20} />} title={t("credentials.ownerRequired")} description={t("credentials.ownerRequiredDescription")} /> :
      loading ? <CredentialSkeleton /> : providers.length === 0 ? <EmptyState icon={<KeyRound size={20} />} title={t("credentials.noProviders")} /> :
      <div className="credential-provider-list">{providers.map((provider) => <Card className="credential-provider" key={provider.kind}>
        <div className="credential-provider-header"><div className="credential-provider-title"><span className="credential-provider-mark"><KeyRound size={17} /></span><div><h2>{provider.displayName}</h2><p>{provider.kind} · {provider.allowedHosts.join(", ")}</p></div></div>
          {provider.authorizationMethods.includes("device_flow") ? <ConnectCredentialDialog provider={provider} disabled={authorization?.status === "pending"} onBegin={begin} /> : <Badge>{t("credentials.manualOnly")}</Badge>}
        </div>
        <div className="credential-provider-meta"><Badge tone={provider.authorizationMethods.includes("device_flow") ? "info" : "neutral"}>{provider.authorizationMethods.includes("device_flow") ? t("credentials.deviceFlow") : t("credentials.manual")}</Badge><span>{t("credentials.bindingCount", { count: bindings[provider.kind]?.length ?? 0 })}</span></div>
        {(bindings[provider.kind]?.length ?? 0) === 0 ? <div className="credential-empty">{t("credentials.emptyProvider", { provider: provider.displayName })}</div> : <div className="credential-binding-list">{(bindings[provider.kind] ?? []).map((binding) => <div className="credential-binding" key={binding.id}>
          <div className="credential-binding-main"><div className="credential-binding-name"><strong>{binding.displayName}</strong>{binding.isDefault ? <Badge tone="info"><Star size={11} />{t("credentials.default")}</Badge> : null}</div><p>{credentialIdentity(binding) || shortID(binding.id)}</p><div className="resource-meta"><Badge tone={credentialTone(binding)}>{credentialStatusLabel(t, binding.status)}</Badge><Badge>{binding.ownerScope === "workspace" ? t("credentials.workspaceOwned") : t("credentials.userOwned")}</Badge><span className="resource-version">v{binding.authorityVersion}.{binding.credentialVersion}</span>{binding.accessExpiresAt ? <span className="resource-version">{t("credentials.accessExpires", { date: formatDate(binding.accessExpiresAt, locale) })}</span> : null}</div></div>
          <div className="resource-actions">{!binding.isDefault && binding.status === "active" ? <Button variant="outline" size="sm" disabled={busyBinding === binding.id} onClick={() => void setDefault(binding)}><Star size={13} />{t("credentials.makeDefault")}</Button> : null}
            {provider.authorizationMethods.includes("device_flow") && binding.status !== "revoked" ? <Button size="sm" disabled={busyBinding === binding.id || authorization?.status === "pending"} onClick={() => void reauthorize(binding)}><RotateCw size={13} />{t("common.reauthorize")}</Button> : null}
            <RenameCredentialDialog binding={binding} disabled={busyBinding === binding.id} onRename={(name) => mutate(binding, () => api.renameCredential(workspace.workspaceId, binding.kind, binding.id, name, binding.authorityVersion))} />
            {binding.status !== "revoked" ? <Button variant="ghost" size="sm" disabled={busyBinding === binding.id} onClick={() => void revoke(binding)}><Unplug size={13} />{t("common.revoke")}</Button> : null}
            <Button variant="ghost" size="sm" disabled={busyBinding === binding.id || binding.isDefault} onClick={() => void remove(binding)}><Trash2 size={13} />{t("credentials.delete")}</Button>
          </div>
        </div>)}</div>}
      </Card>)}</div>}
    <CredentialAuthorizationDialog authorization={authorization} provider={authorization ? providerByKind.get(authorization.kind) : undefined} open={flowOpen} onOpenChange={(open) => { setFlowOpen(open); if (!open && authorization && credentialAuthorizationTerminal(authorization.status)) setAuthorization(null) }} onCancel={cancel} />
  </>
}

function ConnectCredentialDialog({ provider, disabled, onBegin }: { provider: CredentialProvider; disabled: boolean; onBegin: (provider: CredentialProvider, input: { displayName: string; ownerScope: "workspace" | "user"; makeDefault: boolean }) => Promise<void> }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setBusy(true); setError("")
    try {
      const form = new FormData(event.currentTarget)
      await onBegin(provider, {
        displayName: String(form.get("displayName") ?? ""),
        ownerScope: String(form.get("ownerScope")) as "workspace" | "user",
        makeDefault: form.get("makeDefault") === "on",
      })
      setOpen(false)
    } catch (requestError) { setError(safeError(requestError)) } finally { setBusy(false) }
  }
  return <Dialog open={open} onOpenChange={setOpen}><DialogTrigger asChild><Button disabled={disabled}><Plus size={15} />{t("credentials.connect", { provider: provider.displayName })}</Button></DialogTrigger><DialogContent title={t("credentials.connectTitle", { provider: provider.displayName })} description={t("credentials.connectDescription")}><form onSubmit={(event) => void submit(event)}>
    <Label htmlFor={`credential-name-${provider.kind}`}>{t("common.name")}</Label><Input id={`credential-name-${provider.kind}`} name="displayName" defaultValue={`${provider.displayName} credential`} maxLength={256} required autoFocus />
    <Label htmlFor={`credential-owner-${provider.kind}`}>{t("credentials.ownerScope")}</Label><NativeSelect id={`credential-owner-${provider.kind}`} name="ownerScope" defaultValue="workspace"><option value="workspace">{t("credentials.workspaceOwned")}</option><option value="user">{t("credentials.userOwned")}</option></NativeSelect>
    <label className="checkbox-row credential-default-check"><input type="checkbox" name="makeDefault" defaultChecked />{t("credentials.makeDefault")}</label>
    <p className="form-help">{t("credentials.deviceFlowHelp")}</p>{error ? <div className="error-banner inline-error">{error}</div> : null}<div className="form-actions"><DialogClose asChild><Button type="button" variant="ghost">{t("common.cancel")}</Button></DialogClose><Button type="submit" disabled={busy}>{busy ? t("common.loading") : t("common.continue")}</Button></div>
  </form></DialogContent></Dialog>
}

function RenameCredentialDialog({ binding, disabled, onRename }: { binding: WorkspaceCredential; disabled: boolean; onRename: (name: string) => Promise<unknown> }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setBusy(true); setError("")
    try { await onRename(boundedText("credential name", String(new FormData(event.currentTarget).get("displayName") ?? ""), 256)); setOpen(false) } catch (requestError) { setError(safeError(requestError)) } finally { setBusy(false) }
  }
  return <Dialog open={open} onOpenChange={setOpen}><DialogTrigger asChild><Button variant="outline" size="sm" disabled={disabled}>{t("common.edit")}</Button></DialogTrigger><DialogContent title={t("credentials.renameTitle")} description={t("credentials.renameDescription")}><form onSubmit={(event) => void submit(event)}><Label htmlFor={`rename-${binding.id}`}>{t("common.name")}</Label><Input id={`rename-${binding.id}`} name="displayName" defaultValue={binding.displayName} maxLength={256} autoFocus required />{error ? <div className="error-banner inline-error">{error}</div> : null}<div className="form-actions"><DialogClose asChild><Button type="button" variant="ghost">{t("common.cancel")}</Button></DialogClose><Button type="submit" disabled={busy}>{busy ? t("common.loading") : t("common.save")}</Button></div></form></DialogContent></Dialog>
}

function CredentialAuthorizationDialog({ authorization, provider, open, onOpenChange, onCancel }: { authorization: CredentialAuthorization | null; provider: CredentialProvider | undefined; open: boolean; onOpenChange: (open: boolean) => void; onCancel: () => Promise<void> }) {
  const { t } = useTranslation()
  const { locale } = useLocale()
  const [copied, setCopied] = useState(false)
  if (!authorization) return null
  const pending = authorization.status === "pending"
  const succeeded = authorization.status === "succeeded"
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="credential-flow-dialog" title={pending ? t("credentials.authorizeTitle", { provider: provider?.displayName ?? authorization.kind }) : succeeded ? t("credentials.authorizedTitle") : t("credentials.authorizationEnded")} description={pending ? t("credentials.authorizeDescription") : credentialStatusLabel(t, authorization.status)}>
    <div className={`credential-flow-state ${succeeded ? "success" : pending ? "pending" : "failed"}`}>{succeeded ? <CheckCircle2 size={23} /> : <KeyRound size={23} />}<div><strong>{credentialStatusLabel(t, authorization.status)}</strong><span>{pending ? t("credentials.expires", { date: formatDate(authorization.expiresAt, locale) }) : authorization.lastErrorCode || t("credentials.authorizationComplete")}</span></div></div>
    {pending ? <><div className="credential-code"><span>{t("credentials.userCode")}</span><strong>{authorization.userCode || "—"}</strong><Button variant="outline" size="sm" disabled={!authorization.userCode} onClick={() => { void navigator.clipboard.writeText(authorization.userCode).then(() => { setCopied(true); window.setTimeout(() => setCopied(false), 1500) }) }}><Copy size={14} />{copied ? t("common.copied") : t("common.copy")}</Button></div>
      <Button asChild size="lg"><a href={authorization.verificationUriComplete || authorization.verificationUri} target="_blank" rel="noreferrer">{t("credentials.openAuthorization")}<ExternalLink size={15} /></a></Button><p className="form-help">{t("credentials.pollingHelp")}</p></> : null}
    {succeeded && authorization.binding ? <div className="credential-result"><span>{authorization.binding.displayName}</span><Badge tone="success">{t("credentials.ready")}</Badge></div> : null}
    <div className="form-actions">{pending ? <Button variant="ghost" onClick={() => void onCancel()}>{t("common.cancel")}</Button> : null}<DialogClose asChild><Button variant={pending ? "outline" : "default"}>{pending ? t("credentials.continueInBackground") : t("common.close")}</Button></DialogClose></div>
  </DialogContent></Dialog>
}

function credentialIdentity(binding: WorkspaceCredential): string {
  const metadata = binding.publicMetadata as Record<string, unknown>
  for (const key of ["userName", "username", "userOpenId", "appId", "site"]) {
    const value = metadata[key]
    if (typeof value === "string" && value.trim()) return value.slice(0, 256)
  }
  return ""
}

function credentialTone(binding: WorkspaceCredential): "success" | "warning" | "danger" | "neutral" {
  if (binding.status === "active") return "success"
  if (binding.status === "reauth_required") return "warning"
  if (binding.status === "revoked") return "danger"
  return "neutral"
}

function credentialStatusLabel(t: (key: string) => string, status: string): string {
  const known: Record<string, string> = {
    active: "credentials.statusActive", reauth_required: "credentials.statusReauth", revoked: "credentials.statusRevoked", disabled: "credentials.statusDisabled",
    pending: "credentials.statusPending", succeeded: "credentials.statusSucceeded", denied: "credentials.statusDenied", expired: "credentials.statusExpired", cancelled: "credentials.statusCancelled", failed: "credentials.statusFailed",
  }
  return t(known[status] ?? "common.status")
}

function CredentialSkeleton() { return <div className="credential-provider-list"><div className="skeleton skeleton-row" /><div className="skeleton skeleton-row" /></div> }
