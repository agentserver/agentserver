import { useState, type FormEvent } from "react"
import { useTranslation } from "react-i18next"
import { Button, Dialog, DialogClose, DialogContent, DialogTrigger, Input, Label, safeError, type ResourceAPI } from "@agentserver/v2-web-shared"
import { byteCloudCredentialInput } from "./manual-credential"

export function ByteCloudCredentialDialog({ api, workspaceId, onCreated }: { api: ResourceAPI; workspaceId: string; onCreated: () => Promise<void> }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [bindingId, setBindingId] = useState("")
  const changeOpen = (next: boolean) => {
    if (busy) return
    setOpen(next); setError("")
    if (next) setBindingId(crypto.randomUUID())
  }
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    let input: ReturnType<typeof byteCloudCredentialInput>
    try { input = byteCloudCredentialInput(new FormData(form), bindingId) }
    catch { setError(t("credentials.akskInvalid")); return }
    form.reset()
    setBusy(true); setError("")
    try {
      await api.createCredential(workspaceId, "bytecloud", input)
      setOpen(false)
      await onCreated()
    } catch (requestError) { setError(safeError(requestError)) }
    finally { setBusy(false) }
  }
  return <Dialog open={open} onOpenChange={changeOpen}>
    <DialogTrigger asChild><Button>{t("credentials.addAKSK")}</Button></DialogTrigger>
    <DialogContent title={t("credentials.addAKSK")} description={t("credentials.akskHelp")}>
      {open ? <form onSubmit={(event) => void submit(event)} autoComplete="off">
        <Label htmlFor="bytecloud-aksk-name">{t("common.name")}</Label><Input id="bytecloud-aksk-name" name="displayName" maxLength={256} required disabled={busy} />
        <Label htmlFor="bytecloud-aksk-access">{t("credentials.accessKey")}</Label><Input id="bytecloud-aksk-access" name="accessKeyId" type="password" autoComplete="new-password" maxLength={4096} required disabled={busy} />
        <Label htmlFor="bytecloud-aksk-secret">{t("credentials.secretKey")}</Label><Input id="bytecloud-aksk-secret" name="secretAccessKey" type="password" autoComplete="new-password" maxLength={4096} required disabled={busy} />
        <label className="checkbox-row credential-default-check"><input type="checkbox" name="makeDefault" defaultChecked disabled={busy} />{t("credentials.makeDefault")}</label>
        {error ? <div className="error-banner inline-error">{error}</div> : null}
        <div className="form-actions"><DialogClose asChild><Button type="button" variant="ghost" disabled={busy}>{t("common.cancel")}</Button></DialogClose><Button type="submit" disabled={busy}>{busy ? t("common.loading") : t("common.save")}</Button></div>
      </form> : null}
    </DialogContent>
  </Dialog>
}
