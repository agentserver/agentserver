import { useState, useEffect, useCallback } from 'react'
import { Plus, Trash2, Copy, Check, X, Key } from 'lucide-react'
import {
  type CodexBrowser, type MintCodexTokenResponse,
  listCodexBrowsers, mintCodexToken, revokeCodexToken,
} from '../lib/api'
import { ConfirmModal } from './Modals'
import { DeviceListPanel, type DeviceRow } from './DeviceListPanel'

interface Props {
  workspaceId: string
}

// YYYY-MM-DD in local time, offset by `days` from today.
function dateOffsetStr(days: number): string {
  const d = new Date()
  d.setDate(d.getDate() + days)
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

export default function CodexTokensPanel({ workspaceId }: Props) {
  const [browsers, setBrowsers] = useState<CodexBrowser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showMint, setShowMint] = useState(false)
  const [newName, setNewName] = useState('')
  const [expiresDate, setExpiresDate] = useState<string>(() => dateOffsetStr(90))
  const [generated, setGenerated] = useState<MintCodexTokenResponse | null>(null)
  const [copied, setCopied] = useState(false)
  const [revokeTarget, setRevokeTarget] = useState<CodexBrowser | null>(null)

  const refresh = useCallback(async () => {
    try {
      const rows = await listCodexBrowsers(workspaceId)
      setBrowsers(rows)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }, [workspaceId])

  // Initial load + 10s poll so online state stays fresh while the user
  // watches a codex --remote session connect / disconnect.
  useEffect(() => {
    void refresh()
    const id = window.setInterval(() => { void refresh() }, 10_000)
    return () => window.clearInterval(id)
  }, [refresh])

  const onMint = async () => {
    if (!newName.trim() || !expiresDate) return
    try {
      // Use end-of-day local time so picking "today" doesn't immediately expire.
      const expiresAt = new Date(`${expiresDate}T23:59:59`).toISOString()
      const resp = await mintCodexToken({
        workspace_id: workspaceId,
        name: newName.trim(),
        expires_at: expiresAt,
      })
      setGenerated(resp)
      setShowMint(false)
      setNewName('')
      setExpiresDate(dateOffsetStr(90))
      void refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const onRevoke = async (id: string) => {
    try {
      await revokeCodexToken(id)
      setRevokeTarget(null)
      void refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const buildCommand = (token: string) =>
    `export AGENTSERVER_TOKEN='${token}'
codex --remote wss://codex-app.${typeof window !== 'undefined' ? window.location.host : '<host>'}:443 \\
      --remote-auth-token-env AGENTSERVER_TOKEN`

  const copyCommand = async () => {
    if (!generated) return
    await navigator.clipboard.writeText(buildCommand(generated.token))
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  const deviceRows: DeviceRow[] = browsers.map((b) => ({
    id: b.id,
    name: b.name,
    is_online: b.is_online,
    client_ip: b.client_ip,
    os: b.os,
    codex_version: b.codex_version,
    connected_at: b.connected_at,
    disconnected_at: b.disconnected_at,
    lastSeenFallback: b.last_used_at,
  }))

  const findBrowser = (id: string) => browsers.find((b) => b.id === id)

  return (
    <>
      <DeviceListPanel
        title="Browsers"
        icon={Key}
        iconClassName="text-blue-400"
        rows={deviceRows}
        loading={loading}
        error={error}
        emptyMessage="No browsers yet — generate a token to enable a remote codex CLI."
        description={
          <>
            Each browser is a <code className="rounded bg-[var(--background)] px-1 py-0.5 font-mono text-[11px] text-[var(--foreground)]">codex --remote wss://codex-app.&lt;host&gt;:443</code> client using this workspace's token. Online / OS / IP / codex version come from the live ws connection — they auto-update when a CLI connects or disconnects.
          </>
        }
        headerAction={
          <button
            onClick={() => setShowMint(true)}
            className="inline-flex items-center gap-1.5 rounded-md border border-[var(--border)] bg-[var(--card)] px-3 py-1 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--secondary)] transition-colors"
          >
            <Plus size={12} />
            Generate token
          </button>
        }
        actions={(row) => (
          <button
            onClick={() => {
              const b = findBrowser(row.id)
              if (b) setRevokeTarget(b)
            }}
            className="rounded p-1 text-[var(--muted-foreground)] hover:bg-[var(--secondary)] hover:text-[var(--destructive)]"
            aria-label="Revoke token"
            title="Revoke token"
          >
            <Trash2 size={14} />
          </button>
        )}
      />

      {showMint && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setShowMint(false)}>
          <div
            className="w-full max-w-sm rounded-lg border border-[var(--border)] bg-[var(--card)] p-6 shadow-xl"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="mb-4 flex items-center justify-between">
              <h2 className="text-lg font-semibold text-[var(--foreground)]">Generate codex token</h2>
              <button
                onClick={() => setShowMint(false)}
                className="rounded p-1 hover:bg-[var(--secondary)]"
              >
                <X size={16} />
              </button>
            </div>
            <form onSubmit={(e) => { e.preventDefault(); void onMint() }} className="flex flex-col gap-4">
              <div>
                <label className="mb-1 block text-sm font-medium text-[var(--foreground)]">Name</label>
                <input
                  autoFocus
                  type="text"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="my mac"
                  className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm text-[var(--foreground)] outline-none focus:border-[var(--primary)]"
                />
              </div>
              <div>
                <label className="mb-1 block text-sm font-medium text-[var(--foreground)]">Expires on</label>
                <input
                  type="date"
                  value={expiresDate}
                  min={dateOffsetStr(0)}
                  max={dateOffsetStr(365)}
                  onChange={(e) => setExpiresDate(e.target.value)}
                  className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm text-[var(--foreground)] outline-none focus:border-[var(--primary)]"
                />
              </div>
              <div className="flex justify-end gap-2">
                <button
                  type="button"
                  onClick={() => setShowMint(false)}
                  className="rounded-md border border-[var(--border)] px-4 py-2 text-sm font-medium text-[var(--foreground)] hover:bg-[var(--secondary)]"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={!newName.trim() || !expiresDate}
                  className="rounded-md bg-[var(--primary)] px-4 py-2 text-sm font-medium text-[var(--primary-foreground)] hover:opacity-90 disabled:opacity-50"
                >
                  Generate
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {generated && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-full max-w-xl rounded-lg border border-[var(--border)] bg-[var(--card)] p-6 shadow-xl">
            <div className="mb-3 flex items-center justify-between">
              <h2 className="text-lg font-semibold text-[var(--foreground)]">Token generated</h2>
              <button
                onClick={() => setGenerated(null)}
                className="rounded p-1 hover:bg-[var(--secondary)]"
              >
                <X size={16} />
              </button>
            </div>
            <p className="mb-3 text-sm text-[var(--muted-foreground)]">
              Copy it now — you won't see it again.
            </p>
            <div className="mb-4 flex items-start gap-2">
              <pre className="flex-1 overflow-x-auto rounded-md border border-[var(--border)] bg-[var(--background)] p-3 text-[11px] text-[var(--foreground)]">{buildCommand(generated.token)}</pre>
              <button
                onClick={copyCommand}
                className="rounded-md border border-[var(--border)] p-2 text-[var(--foreground)] hover:bg-[var(--secondary)]"
                aria-label="Copy command"
                title="Copy"
              >
                {copied ? <Check size={14} /> : <Copy size={14} />}
              </button>
            </div>
            <div className="flex justify-end">
              <button
                onClick={() => setGenerated(null)}
                className="rounded-md bg-[var(--primary)] px-4 py-2 text-sm font-medium text-[var(--primary-foreground)] hover:opacity-90"
              >
                I've saved it
              </button>
            </div>
          </div>
        </div>
      )}

      {revokeTarget && (
        <ConfirmModal
          title="Revoke codex token"
          message={`Revoke "${revokeTarget.name}"? Active codex --remote sessions using it will be cut at next reconnect.`}
          confirmLabel="Revoke"
          destructive
          onConfirm={() => onRevoke(revokeTarget.id)}
          onCancel={() => setRevokeTarget(null)}
        />
      )}
    </>
  )
}
