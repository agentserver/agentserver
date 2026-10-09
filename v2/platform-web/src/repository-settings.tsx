import { useCallback, useEffect, useState, type FormEvent } from "react"
import { Link } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { APIError, Button, Card, Input, Label, NativeSelect, safeError, type ResourceAPI, type Workspace, type WorkspaceCredential, type WorkspaceRepositorySetting } from "@agentserver/v2-web-shared"

export function RepositorySettings({ workspace, api }: { workspace: Workspace; api: ResourceAPI }) {
  const { t } = useTranslation()
  const [setting, setSetting] = useState<WorkspaceRepositorySetting | null>(null)
  const [credentials, setCredentials] = useState<WorkspaceCredential[]>([])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const owner = workspace.currentUserRole === "owner" && workspace.status === "active"
  const load = useCallback(async () => {
    setLoading(true); setError("")
    try {
      const [result, bindings] = await Promise.all([
        api.getRepository(workspace.workspaceId),
        owner ? api.listCredentials(workspace.workspaceId, "git") : Promise.resolve([]),
      ])
      setSetting(result.setting)
      setCredentials(bindings.filter(binding => binding.ownerScope === "workspace" && binding.status === "active"))
    } catch (err) { setError(safeError(err)); setSetting(null) }
    finally { setLoading(false) }
  }, [api, workspace.workspaceId, owner])
  useEffect(() => { void load() }, [load])
  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!setting) return
    const form = new FormData(event.currentTarget)
    const url = String(form.get("url") ?? "").trim()
    const credentialBindingId = String(form.get("credentialBindingId") ?? "")
    if (!url && setting.source && !window.confirm(t("repository.clearConfirm"))) return
    setBusy(true); setError("")
    try {
      const result = await api.updateRepository(workspace.workspaceId, {
        expectedVersion: setting.version,
        source: url ? {
          url, ref: String(form.get("ref") ?? "").trim(),
          workingDirectory: String(form.get("workingDirectory") ?? "").trim() || ".",
          ...(credentialBindingId ? { credentialBindingId } : {}),
        } : null,
      })
      setSetting(result.setting)
    } catch (err) {
      if (err instanceof APIError && err.status === 409) {
        await load(); setError(t("repository.conflict"))
      } else setError(safeError(err))
    } finally { setBusy(false) }
  }
  return <Card className="settings-card">
    <div><h2>{t("repository.title")}</h2><p>{t("repository.help")}</p></div>
    {error ? <div className="error-banner inline-error">{error}</div> : null}
    {loading ? <div className="skeleton" /> : setting ? owner ? <form key={setting.version} onSubmit={event => void save(event)} autoComplete="off">
      <Label htmlFor="repository-url">{t("repository.url")}</Label><Input id="repository-url" name="url" type="url" maxLength={2048} defaultValue={setting.source?.url ?? ""} placeholder="https://code.byted.org/tce/rtm-aihub" disabled={busy} />
      <Label htmlFor="repository-ref">{t("repository.ref")}</Label><Input id="repository-ref" name="ref" maxLength={256} defaultValue={setting.source?.ref ?? ""} disabled={busy} />
      <Label htmlFor="repository-cwd">{t("repository.cwd")}</Label><Input id="repository-cwd" name="workingDirectory" maxLength={4096} defaultValue={setting.source?.workingDirectory ?? "."} disabled={busy} />
      <Label htmlFor="repository-credential">{t("repository.credential")}</Label><NativeSelect id="repository-credential" name="credentialBindingId" defaultValue={setting.source?.credentialBindingId ?? ""} disabled={busy}>
        <option value="">{t("repository.noCredential")}</option>
        {setting.source?.credentialBindingId && !credentials.some(binding => binding.id === setting.source?.credentialBindingId) ? <option value={setting.source.credentialBindingId} disabled>{t("repository.credentialUnavailable")}</option> : null}
        {credentials.map(binding => <option key={binding.id} value={binding.id}>{binding.displayName}</option>)}
      </NativeSelect>
      <p className="form-help"><Link to={`/workspaces/${workspace.workspaceId}/credentials`}>{t("repository.manageCredentials")}</Link></p>
      <p className="form-help">{t("repository.settingsOnlyNotice")}</p>
      <Button type="submit" disabled={busy}>{busy ? t("common.loading") : t("common.save")}</Button>
    </form> : <p>{setting.source ? `${setting.source.url} · ${setting.source.ref || "HEAD"} · ${setting.source.workingDirectory}` : t("repository.unconfigured")}</p> : <Button variant="outline" onClick={() => void load()}>{t("common.retry")}</Button>}
  </Card>
}
