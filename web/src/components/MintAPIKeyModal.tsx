import { useState, useEffect } from 'react'
import { X, Loader2, Copy, CheckCheck, Key } from 'lucide-react'
import {
  listWorkspaceAPIKeyScopes,
  mintWorkspaceAPIKey,
  type APIKeyScopeDescriptor,
  type WorkspaceAPIKeyMintResponse,
} from '../lib/api'

interface MintAPIKeyModalProps {
  workspaceId: string
  onClose: () => void
  onCreated: () => void
}

type Phase = 'loading' | 'form' | 'reveal'

// YYYY-MM-DD in local time, offset by `days` from today.
function dateOffsetStr(days: number): string {
  const d = new Date()
  d.setDate(d.getDate() + days)
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

export function MintAPIKeyModal({ workspaceId, onClose, onCreated }: MintAPIKeyModalProps) {
  const [phase, setPhase] = useState<Phase>('loading')
  const [scopes, setScopes] = useState<APIKeyScopeDescriptor[]>([])
  const [loadError, setLoadError] = useState<string | null>(null)

  // Form state
  const [name, setName] = useState('')
  const [checkedScopes, setCheckedScopes] = useState<Set<string>>(new Set())
  const [expiresDate, setExpiresDate] = useState<string>(() => dateOffsetStr(90))
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)

  // Reveal state
  const [minted, setMinted] = useState<WorkspaceAPIKeyMintResponse | null>(null)
  const [copied, setCopied] = useState(false)

  // Load scope catalog on mount
  useEffect(() => {
    listWorkspaceAPIKeyScopes(workspaceId)
      .then((catalog) => {
        setScopes(catalog)
        // Pre-check all available scopes (v1: only turns:submit)
        const defaults = new Set(catalog.filter((s) => s.available).map((s) => s.name))
        setCheckedScopes(defaults)
        setPhase('form')
      })
      .catch((err) => {
        setLoadError(String(err))
        setPhase('form') // show form without scopes; user will see error
      })
  }, [workspaceId])

  const toggleScope = (scopeName: string, available: boolean) => {
    if (!available) return
    setCheckedScopes((prev) => {
      const next = new Set(prev)
      if (next.has(scopeName)) {
        next.delete(scopeName)
      } else {
        next.add(scopeName)
      }
      return next
    })
  }

  const handleCreate = async () => {
    if (!name.trim() || checkedScopes.size === 0 || !expiresDate) return
    setSubmitting(true)
    setSubmitError(null)
    try {
      // Use end-of-day local time so picking "today" doesn't immediately expire.
      const expiresAt = new Date(`${expiresDate}T23:59:59`).toISOString()
      const result = await mintWorkspaceAPIKey(workspaceId, name.trim(), Array.from(checkedScopes), expiresAt)
      setMinted(result)
      setPhase('reveal')
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  const handleCopy = () => {
    if (!minted) return
    navigator.clipboard?.writeText(minted.secret).catch(() => {})
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  const canCreate = name.trim().length > 0 && checkedScopes.size > 0 && !!expiresDate && !submitting

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
      onClick={phase !== 'loading' ? undefined : onClose}
    >
      <div
        className="relative w-full max-w-md rounded-xl border border-[var(--border)] bg-[var(--card)] p-6 shadow-2xl"
        onClick={(e) => e.stopPropagation()}
      >
        <button
          onClick={phase === 'reveal' ? onCreated : onClose}
          className="absolute right-4 top-4 text-[var(--muted-foreground)] hover:text-[var(--foreground)] transition-colors"
        >
          <X size={16} />
        </button>

        <div className="flex items-center gap-2 mb-5">
          <Key size={18} className="text-[var(--muted-foreground)]" />
          <h2 className="text-base font-semibold text-[var(--foreground)]">
            {phase === 'reveal' ? 'API Key Created' : 'Create API Key'}
          </h2>
        </div>

        {/* Loading state */}
        {phase === 'loading' && (
          <div className="flex flex-col items-center gap-3 py-8">
            <Loader2 size={28} className="animate-spin text-[var(--muted-foreground)]" />
            <p className="text-sm text-[var(--muted-foreground)]">Loading scope catalog...</p>
          </div>
        )}

        {/* Form state */}
        {phase === 'form' && (
          <div className="flex flex-col gap-4">
            <div>
              <label className="block text-xs font-medium text-[var(--muted-foreground)] mb-1">
                Name <span className="text-red-400">*</span>
              </label>
              <input
                autoFocus
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. my-bot-integration"
                disabled={submitting}
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm text-[var(--foreground)] placeholder:text-[var(--muted-foreground)] focus:outline-none focus:ring-1 focus:ring-[var(--primary)] disabled:opacity-50"
                onKeyDown={(e) => { if (e.key === 'Enter' && canCreate) handleCreate() }}
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-[var(--muted-foreground)] mb-2">
                Scopes <span className="text-red-400">*</span>
              </label>
              {loadError ? (
                <p className="text-xs text-red-400">{loadError}</p>
              ) : scopes.length === 0 ? (
                <p className="text-xs text-[var(--muted-foreground)]">No scopes available.</p>
              ) : (
                <div className="flex flex-col gap-2">
                  {scopes.map((scope) => (
                    <label
                      key={scope.name}
                      className={`flex items-start gap-3 rounded-md border border-[var(--border)] px-3 py-2.5 ${
                        scope.available
                          ? 'cursor-pointer hover:bg-[var(--secondary)]/50 transition-colors'
                          : 'cursor-not-allowed opacity-50'
                      }`}
                      title={!scope.available ? 'Coming soon — no endpoint enforces this scope yet' : undefined}
                    >
                      <input
                        type="checkbox"
                        checked={checkedScopes.has(scope.name)}
                        disabled={!scope.available || submitting}
                        onChange={() => toggleScope(scope.name, scope.available)}
                        className="mt-0.5 shrink-0"
                      />
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <code className="text-[11px] font-mono text-[var(--foreground)]">{scope.name}</code>
                          {!scope.available && (
                            <span className="rounded bg-[var(--secondary)] px-1 py-0.5 text-[9px] text-[var(--muted-foreground)]">
                              coming soon
                            </span>
                          )}
                        </div>
                        <p className="mt-0.5 text-[11px] text-[var(--muted-foreground)]">{scope.description}</p>
                      </div>
                    </label>
                  ))}
                </div>
              )}
            </div>

            <div>
              <label className="block text-xs font-medium text-[var(--muted-foreground)] mb-1">
                Expires on
              </label>
              <input
                type="date"
                value={expiresDate}
                min={dateOffsetStr(0)}
                max={dateOffsetStr(365)}
                onChange={(e) => setExpiresDate(e.target.value)}
                disabled={submitting}
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm text-[var(--foreground)] focus:outline-none focus:ring-1 focus:ring-[var(--primary)] disabled:opacity-50"
              />
            </div>

            {submitError && (
              <p className="text-xs text-red-400">{submitError}</p>
            )}

            <div className="flex gap-2 justify-end pt-1">
              <button
                onClick={onClose}
                disabled={submitting}
                className="rounded-md border border-[var(--border)] bg-[var(--card)] px-4 py-1.5 text-xs font-medium text-[var(--foreground)] hover:bg-[var(--secondary)] transition-colors disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                onClick={handleCreate}
                disabled={!canCreate}
                className="rounded-md bg-[var(--primary)] px-4 py-1.5 text-xs font-medium text-[var(--primary-foreground)] hover:opacity-90 transition-opacity disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {submitting ? 'Creating...' : 'Create'}
              </button>
            </div>
          </div>
        )}

        {/* Reveal state */}
        {phase === 'reveal' && minted && (
          <div className="flex flex-col gap-4">
            <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-4 py-3">
              <p className="text-xs font-semibold text-amber-400">
                This is the only time you'll see this secret. Copy it now.
              </p>
            </div>

            <div>
              <label className="block text-xs font-medium text-[var(--muted-foreground)] mb-1">
                API Key Secret
              </label>
              <div className="flex items-center gap-2">
                <code className="flex-1 rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-[11px] font-mono text-[var(--foreground)] break-all select-all">
                  {minted.secret}
                </code>
                <button
                  onClick={handleCopy}
                  className="shrink-0 rounded-md border border-[var(--border)] bg-[var(--card)] p-2 text-[var(--muted-foreground)] hover:text-[var(--foreground)] hover:bg-[var(--secondary)] transition-colors"
                  title="Copy to clipboard"
                >
                  {copied ? <CheckCheck size={14} className="text-green-400" /> : <Copy size={14} />}
                </button>
              </div>
            </div>

            <div>
              <label className="block text-xs font-medium text-[var(--muted-foreground)] mb-1">
                Granted scopes
              </label>
              <div className="flex flex-wrap gap-1">
                {minted.scopes.map((s) => (
                  <span
                    key={s}
                    className="rounded bg-green-500/10 border border-green-500/20 px-2 py-0.5 text-[10px] font-mono text-green-400"
                  >
                    {s}
                  </span>
                ))}
              </div>
            </div>

            <div className="flex justify-end pt-1">
              <button
                onClick={onCreated}
                className="rounded-md bg-[var(--primary)] px-6 py-1.5 text-xs font-medium text-[var(--primary-foreground)] hover:opacity-90 transition-opacity"
              >
                Done
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
