import { useEffect, useState } from 'react'
import { Activity, AlertCircle, CheckCircle2, ChevronDown, ChevronRight, MessageSquare } from 'lucide-react'
import clsx from 'clsx'
import {
  listAuditCalls,
  listAuditSessions,
  type AuditCallSummary,
  type AuditSessionSummary,
  type ListAuditCallsFilters,
  type ListAuditSessionsFilters,
} from '../lib/api'
import ExecAuditCallDetail from './ExecAuditCallDetail'
import ExecAuditSessionDetail from './ExecAuditSessionDetail'

type Tab = 'calls' | 'sessions'

interface Props {
  workspaceId: string
}

export default function ExecAuditPanel({ workspaceId }: Props) {
  const [tab, setTab] = useState<Tab>('calls')
  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-2 border-b border-zinc-200 dark:border-zinc-800 px-4 py-3">
        <TabButton active={tab === 'calls'} onClick={() => setTab('calls')} icon={<MessageSquare size={16} />}>
          Calls
        </TabButton>
        <TabButton active={tab === 'sessions'} onClick={() => setTab('sessions')} icon={<Activity size={16} />}>
          Sessions
        </TabButton>
      </div>
      <div className="flex-1 overflow-auto p-4">
        {tab === 'calls' && <CallsTab workspaceId={workspaceId} />}
        {tab === 'sessions' && <SessionsTab workspaceId={workspaceId} />}
      </div>
    </div>
  )
}

function TabButton({
  active,
  onClick,
  icon,
  children,
}: {
  active: boolean
  onClick: () => void
  icon: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <button
      onClick={onClick}
      className={clsx(
        'flex items-center gap-1.5 px-3 py-1.5 rounded text-sm',
        active
          ? 'bg-zinc-200 dark:bg-zinc-800 text-zinc-900 dark:text-zinc-100'
          : 'text-zinc-600 dark:text-zinc-400 hover:bg-zinc-100 dark:hover:bg-zinc-900',
      )}
    >
      {icon}
      {children}
    </button>
  )
}

// ----- Calls tab -----

function CallsTab({ workspaceId }: { workspaceId: string }) {
  const [filters, setFilters] = useState<ListAuditCallsFilters>({ limit: 100 })
  const [calls, setCalls] = useState<AuditCallSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)

  const refresh = async () => {
    setLoading(true)
    setError(null)
    try {
      setCalls(await listAuditCalls(workspaceId, filters))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }

  // Re-fetch when workspace or filters change. JSON-stringify so the
  // filter object can be a fresh value each render without infinite
  // loops; React's structural compare doesn't apply to object refs.
  useEffect(() => {
    void refresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspaceId, JSON.stringify(filters)])

  return (
    <div>
      <CallsFilterBar filters={filters} onChange={setFilters} loading={loading} onRefresh={refresh} />
      {error && (
        <div className="mt-3 p-3 rounded bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-sm">
          {error}
        </div>
      )}
      <div className="mt-3 rounded border border-zinc-200 dark:border-zinc-800 overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-zinc-50 dark:bg-zinc-900 text-zinc-500 text-xs uppercase">
            <tr>
              <th className="text-left px-3 py-2 w-8"></th>
              <th className="text-left px-3 py-2">Time</th>
              <th className="text-left px-3 py-2">Source</th>
              <th className="text-left px-3 py-2">User</th>
              <th className="text-left px-3 py-2">Exe</th>
              <th className="text-left px-3 py-2">Method</th>
              <th className="text-left px-3 py-2">Status</th>
              <th className="text-right px-3 py-2">Dur</th>
            </tr>
          </thead>
          <tbody>
            {calls.length === 0 && !loading && (
              <tr><td colSpan={8} className="px-3 py-8 text-center text-zinc-500">No calls.</td></tr>
            )}
            {calls.map((c) => (
              <CallRow
                key={c.id}
                call={c}
                workspaceId={workspaceId}
                expanded={expanded === c.id}
                onToggle={() => setExpanded(expanded === c.id ? null : c.id)}
              />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function CallRow({
  call,
  workspaceId,
  expanded,
  onToggle,
}: {
  call: AuditCallSummary
  workspaceId: string
  expanded: boolean
  onToggle: () => void
}) {
  return (
    <>
      <tr
        onClick={onToggle}
        className="border-t border-zinc-200 dark:border-zinc-800 hover:bg-zinc-50 dark:hover:bg-zinc-900 cursor-pointer"
      >
        <td className="px-3 py-2 text-zinc-400">
          {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        </td>
        <td className="px-3 py-2 font-mono text-xs">{call.started_at?.slice(11, 19) ?? '—'}</td>
        <td className="px-3 py-2"><SourceBadge source={call.source} /></td>
        <td className="px-3 py-2 font-mono text-xs">{call.user_id ? short(call.user_id) : <span className="text-zinc-400">—</span>}</td>
        <td className="px-3 py-2 font-mono text-xs">{short(call.exe_id)}</td>
        <td className="px-3 py-2">{call.rpc_method ?? <span className="text-zinc-400">—</span>}</td>
        <td className="px-3 py-2">
          {call.is_error
            ? <span className="flex items-center gap-1 text-red-600"><AlertCircle size={14} />error</span>
            : <span className="flex items-center gap-1 text-emerald-600"><CheckCircle2 size={14} />ok</span>}
        </td>
        <td className="px-3 py-2 text-right font-mono text-xs">
          {call.duration_ms != null ? `${call.duration_ms}ms` : '—'}
        </td>
      </tr>
      {expanded && (
        <tr className="bg-zinc-50 dark:bg-zinc-900">
          <td colSpan={8} className="px-3 py-3">
            <ExecAuditCallDetail workspaceId={workspaceId} callId={call.id} />
          </td>
        </tr>
      )}
    </>
  )
}

function SourceBadge({ source }: { source: string }) {
  const color =
    source === 'envmcp'
      ? 'bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300'
      : source === 'rest'
        ? 'bg-purple-100 text-purple-700 dark:bg-purple-950 dark:text-purple-300'
        : 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300'
  return <span className={clsx('px-1.5 py-0.5 rounded text-xs font-medium', color)}>{source}</span>
}

function short(s: string): string {
  if (!s) return ''
  if (s.length <= 12) return s
  return s.slice(0, 6) + '…' + s.slice(-4)
}

function CallsFilterBar({
  filters,
  onChange,
  loading,
  onRefresh,
}: {
  filters: ListAuditCallsFilters
  onChange: (f: ListAuditCallsFilters) => void
  loading: boolean
  onRefresh: () => void
}) {
  return (
    <div className="flex flex-wrap items-end gap-2">
      <label className="flex flex-col text-xs">
        <span className="text-zinc-500 mb-0.5">Source</span>
        <select
          className="rounded border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2 py-1 text-sm"
          value={filters.source ?? ''}
          onChange={(e) =>
            onChange({ ...filters, source: (e.target.value || undefined) as ListAuditCallsFilters['source'] })
          }
        >
          <option value="">all</option>
          <option value="envmcp">envmcp</option>
          <option value="rest">rest</option>
          <option value="relay">relay</option>
        </select>
      </label>
      <label className="flex flex-col text-xs">
        <span className="text-zinc-500 mb-0.5">Method</span>
        <input
          className="rounded border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2 py-1 text-sm"
          value={filters.method ?? ''}
          onChange={(e) => onChange({ ...filters, method: e.target.value || undefined })}
          placeholder="e.g. shell"
        />
      </label>
      <label className="flex flex-col text-xs">
        <span className="text-zinc-500 mb-0.5">Exe ID</span>
        <input
          className="rounded border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2 py-1 text-sm"
          value={filters.exe_id ?? ''}
          onChange={(e) => onChange({ ...filters, exe_id: e.target.value || undefined })}
        />
      </label>
      <label className="flex flex-col text-xs">
        <span className="text-zinc-500 mb-0.5">Errors only</span>
        <input
          type="checkbox"
          checked={filters.is_error === true}
          onChange={(e) => onChange({ ...filters, is_error: e.target.checked ? true : undefined })}
          className="mt-1.5"
        />
      </label>
      <button
        onClick={onRefresh}
        disabled={loading}
        className="ml-auto rounded bg-zinc-900 dark:bg-zinc-100 text-white dark:text-zinc-900 px-3 py-1.5 text-sm disabled:opacity-50"
      >
        {loading ? 'Loading…' : 'Refresh'}
      </button>
    </div>
  )
}

// ----- Sessions tab -----

function SessionsTab({ workspaceId }: { workspaceId: string }) {
  const [filters, setFilters] = useState<ListAuditSessionsFilters>({ limit: 100 })
  const [sessions, setSessions] = useState<AuditSessionSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)

  const refresh = async () => {
    setLoading(true)
    setError(null)
    try {
      setSessions(await listAuditSessions(workspaceId, filters))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void refresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workspaceId, JSON.stringify(filters)])

  return (
    <div>
      <SessionsFilterBar filters={filters} onChange={setFilters} loading={loading} onRefresh={refresh} />
      {error && (
        <div className="mt-3 p-3 rounded bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 text-sm">
          {error}
        </div>
      )}
      <div className="mt-3 rounded border border-zinc-200 dark:border-zinc-800 overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-zinc-50 dark:bg-zinc-900 text-zinc-500 text-xs uppercase">
            <tr>
              <th className="text-left px-3 py-2 w-8"></th>
              <th className="text-left px-3 py-2">Opened</th>
              <th className="text-left px-3 py-2">Exe</th>
              <th className="text-left px-3 py-2">User</th>
              <th className="text-left px-3 py-2">Stream</th>
              <th className="text-right px-3 py-2">Frames ↔</th>
              <th className="text-right px-3 py-2">Bytes ↔</th>
            </tr>
          </thead>
          <tbody>
            {sessions.length === 0 && !loading && (
              <tr><td colSpan={7} className="px-3 py-8 text-center text-zinc-500">No sessions.</td></tr>
            )}
            {sessions.map((s) => (
              <SessionRow
                key={s.id}
                session={s}
                workspaceId={workspaceId}
                expanded={expanded === s.id}
                onToggle={() => setExpanded(expanded === s.id ? null : s.id)}
              />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function SessionRow({
  session,
  workspaceId,
  expanded,
  onToggle,
}: {
  session: AuditSessionSummary
  workspaceId: string
  expanded: boolean
  onToggle: () => void
}) {
  const fb = session.frames_to_backend ?? 0
  const fc = session.frames_to_client ?? 0
  const bb = session.bytes_to_backend ?? 0
  const bc = session.bytes_to_client ?? 0
  return (
    <>
      <tr
        onClick={onToggle}
        className="border-t border-zinc-200 dark:border-zinc-800 hover:bg-zinc-50 dark:hover:bg-zinc-900 cursor-pointer"
      >
        <td className="px-3 py-2 text-zinc-400">
          {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        </td>
        <td className="px-3 py-2 font-mono text-xs">{session.opened_at?.slice(11, 19) ?? '—'}</td>
        <td className="px-3 py-2 font-mono text-xs">{short(session.exe_id)}</td>
        <td className="px-3 py-2 font-mono text-xs">{session.user_id ? short(session.user_id) : <span className="text-zinc-400">—</span>}</td>
        <td className="px-3 py-2 font-mono text-xs">{short(session.stream_id)}</td>
        <td className="px-3 py-2 text-right font-mono text-xs">{fb}/{fc}</td>
        <td className="px-3 py-2 text-right font-mono text-xs">{fmtSizeShort(bb)}/{fmtSizeShort(bc)}</td>
      </tr>
      {expanded && (
        <tr className="bg-zinc-50 dark:bg-zinc-900">
          <td colSpan={7} className="px-3 py-3">
            <ExecAuditSessionDetail workspaceId={workspaceId} sessionId={session.id} />
          </td>
        </tr>
      )}
    </>
  )
}

function SessionsFilterBar({
  filters,
  onChange,
  loading,
  onRefresh,
}: {
  filters: ListAuditSessionsFilters
  onChange: (f: ListAuditSessionsFilters) => void
  loading: boolean
  onRefresh: () => void
}) {
  return (
    <div className="flex flex-wrap items-end gap-2">
      <label className="flex flex-col text-xs">
        <span className="text-zinc-500 mb-0.5">Exe ID</span>
        <input
          className="rounded border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2 py-1 text-sm"
          value={filters.exe_id ?? ''}
          onChange={(e) => onChange({ ...filters, exe_id: e.target.value || undefined })}
        />
      </label>
      <label className="flex flex-col text-xs">
        <span className="text-zinc-500 mb-0.5">User ID</span>
        <input
          className="rounded border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2 py-1 text-sm"
          value={filters.user_id ?? ''}
          onChange={(e) => onChange({ ...filters, user_id: e.target.value || undefined })}
        />
      </label>
      <label className="flex flex-col text-xs">
        <span className="text-zinc-500 mb-0.5">Turn ID</span>
        <input
          className="rounded border border-zinc-300 dark:border-zinc-700 bg-white dark:bg-zinc-900 px-2 py-1 text-sm"
          value={filters.turn_id ?? ''}
          onChange={(e) => onChange({ ...filters, turn_id: e.target.value || undefined })}
        />
      </label>
      <button
        onClick={onRefresh}
        disabled={loading}
        className="ml-auto rounded bg-zinc-900 dark:bg-zinc-100 text-white dark:text-zinc-900 px-3 py-1.5 text-sm disabled:opacity-50"
      >
        {loading ? 'Loading…' : 'Refresh'}
      </button>
    </div>
  )
}

// Compact size formatter for the dense table cell.
function fmtSizeShort(n: number): string {
  if (n < 1024) return `${n}B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)}K`
  return `${(n / 1024 / 1024).toFixed(1)}M`
}
