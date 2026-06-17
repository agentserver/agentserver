import { useEffect, useState } from 'react'
import {
  getOAuthConsentInfo,
  listMyWorkspaces,
  submitOAuthConsent,
  type Workspace,
} from '../lib/api'

interface OAuthConsentProps {
  challenge: string
}

// scopeLabels maps known scope strings to human-readable consent rows.
// Unknown scopes (openid, profile, agent:register, etc.) render with
// the raw scope name as the label — a safe fallback that keeps the
// non-MCP consent flows (codex device login) showing something rather
// than nothing. The MCP-flow scopes get explicit copy because mcp:exec
// is the shell-access opt-in and the user needs to see clearly what
// they're granting.
type ScopeRow = { label: string; description?: string; warning?: boolean }
const scopeLabels: Record<string, ScopeRow> = {
  'mcp:read': {
    label: 'Read files and list environments',
    description:
      'List your registered executors and read file contents through this workspace.',
  },
  'mcp:exec': {
    label: 'Run shell commands',
    description:
      'Run arbitrary shell commands, write files, and control processes on your registered executors. Only grant this to clients you trust.',
    warning: true,
  },
  'agent:register': {
    label: 'Register as a local agent',
    description: 'Receive and execute tasks from this workspace.',
  },
  openid: { label: 'Sign in to verify your identity' },
  profile: { label: 'View your basic profile' },
}

function renderScope(scope: string): ScopeRow {
  return scopeLabels[scope] ?? { label: scope }
}

export function OAuthConsent({ challenge }: OAuthConsentProps) {
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [selected, setSelected] = useState<string>('')
  const [scopes, setScopes] = useState<string[]>([])
  const [clientId, setClientId] = useState<string>('')
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    // Fetch workspaces + scopes in parallel — both are needed before
    // the page can render, and they're independent.
    Promise.all([listMyWorkspaces(), getOAuthConsentInfo(challenge)])
      .then(([ws, info]) => {
        setWorkspaces(ws)
        if (ws.length === 1) setSelected(ws[0].id)
        setScopes(info.requested_scope ?? [])
        setClientId(info.client_id ?? '')
        setLoading(false)
      })
      .catch(() => {
        setError('Failed to load consent screen')
        setLoading(false)
      })
  }, [challenge])

  const handleSubmit = async (action: 'accept' | 'deny') => {
    if (action === 'accept' && !selected) {
      setError('Please select a workspace')
      return
    }
    setSubmitting(true)
    setError('')
    try {
      const { redirect_to } = await submitOAuthConsent(challenge, selected, action)
      window.location.href = redirect_to
    } catch {
      setError('Failed to submit. Please try again.')
      setSubmitting(false)
    }
  }

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="text-[var(--muted-foreground)]">Loading...</div>
      </div>
    )
  }

  // Render the requested scopes in a fixed order: read before exec so
  // the user reads the safe one first, then the warning. Unknown
  // scopes go last in whatever order Hydra returned them.
  const orderedScopes = [...scopes].sort((a, b) => {
    const order = ['openid', 'profile', 'mcp:read', 'mcp:exec', 'agent:register']
    const ai = order.indexOf(a)
    const bi = order.indexOf(b)
    if (ai === -1 && bi === -1) return a.localeCompare(b)
    if (ai === -1) return 1
    if (bi === -1) return -1
    return ai - bi
  })

  return (
    <div className="min-h-screen flex items-center justify-center">
      <div className="w-full max-w-md border border-[var(--border)] rounded-lg p-6 space-y-6">
        <div className="text-center">
          <h2 className="text-lg font-semibold">Authorize access</h2>
          <p className="text-sm text-[var(--muted-foreground)] mt-1">
            {clientId
              ? <><span className="font-mono">{clientId}</span> requests access to a workspace</>
              : 'An app requests access to a workspace'}
          </p>
        </div>

        {workspaces.length === 0 ? (
          <div className="text-center text-sm text-[var(--muted-foreground)]">
            No workspaces available. Contact your administrator.
          </div>
        ) : (
          <div className="space-y-2">
            <p className="text-sm font-medium">Workspace</p>
            {workspaces.map((ws) => (
              <label
                key={ws.id}
                className={`flex items-center gap-3 p-3 rounded-md border cursor-pointer transition-colors ${
                  selected === ws.id
                    ? 'border-[var(--primary)] bg-[var(--primary)]/5'
                    : 'border-[var(--border)] hover:border-[var(--muted-foreground)]'
                }`}
              >
                <input
                  type="radio"
                  name="workspace"
                  value={ws.id}
                  checked={selected === ws.id}
                  onChange={() => setSelected(ws.id)}
                  className="accent-[var(--primary)]"
                />
                <span className="text-sm font-medium">{ws.name}</span>
              </label>
            ))}
          </div>
        )}

        {orderedScopes.length > 0 && (
          <div className="space-y-2">
            <p className="text-sm font-medium">Permissions requested</p>
            <ul className="space-y-2">
              {orderedScopes.map((scope) => {
                const row = renderScope(scope)
                return (
                  <li
                    key={scope}
                    className={`text-sm p-2 rounded-md ${
                      row.warning
                        ? 'border border-orange-400/40 bg-orange-400/5'
                        : ''
                    }`}
                  >
                    <div className={row.warning ? 'font-medium text-orange-400' : 'font-medium'}>
                      {row.warning && '⚠️ '}
                      {row.label}
                    </div>
                    {row.description && (
                      <div className="text-xs text-[var(--muted-foreground)] mt-0.5">
                        {row.description}
                      </div>
                    )}
                  </li>
                )
              })}
            </ul>
          </div>
        )}

        {error && (
          <div className="text-sm text-red-500 text-center">{error}</div>
        )}

        <div className="flex gap-3 justify-end">
          <button
            onClick={() => handleSubmit('deny')}
            disabled={submitting}
            className="px-4 py-2 text-sm border border-[var(--border)] rounded-md hover:bg-[var(--muted)]"
          >
            Deny
          </button>
          <button
            onClick={() => handleSubmit('accept')}
            disabled={submitting || !selected}
            className="px-4 py-2 text-sm bg-[var(--primary)] text-[var(--primary-foreground)] rounded-md hover:opacity-90 disabled:opacity-50"
          >
            {submitting ? 'Authorizing...' : 'Allow'}
          </button>
        </div>
      </div>
    </div>
  )
}
