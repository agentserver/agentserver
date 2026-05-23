# Exec-Gateway Audit — Frontend ExecAuditPanel Implementation Plan (Plan 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Prerequisite:** Plan 2a (`2026-05-23-exec-audit-agentserver.md`) merged. Plan 2b is not strictly required (the UI works against an empty DB, just shows no data), but it's much more useful to land after Plan 2b is live so there's actual data to look at.

**Goal:** Build a workspace-scoped ExecAuditPanel that replaces the OperationsPanel slot in `WorkspaceDetail.tsx`. Two tabs: Sessions (per envmcp WS bridge lifecycle) and Calls (per logical RPC / SDK call / relay transfer). Each row clickable for detail; payload download button per call (gracefully degrades when payload too large to store).

**Architecture:** Single `ExecAuditPanel.tsx` component with internal tab state. Per-tab table is server-side filtered via query params. Detail view is an inline expansion (drawer style) rather than route change — keeps the URL stable for sharing. API client added to `web/src/lib/api.ts` with one fetcher per endpoint. The auto-generated `schema.d.ts` (regenerated in Plan 2a Task 10) provides the response types — no hand-written DTOs in TS.

**Tech Stack:** React 19, TypeScript, Vite, Tailwind 4, lucide-react, react-router-dom. Existing `apiFetch` from `lib/api.ts` for auth + base URL.

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `web/src/components/ExecAuditPanel.tsx` | Create | Top-level component: tabs (Sessions / Calls), filter bar, paginated table, expandable detail. ~400 LOC. |
| `web/src/components/ExecAuditCallDetail.tsx` | Create | Inline detail row for a call (request preview, response preview, download buttons). ~150 LOC. |
| `web/src/components/ExecAuditSessionDetail.tsx` | Create | Inline detail row for a session (metadata + first 20 calls). ~100 LOC. |
| `web/src/lib/api.ts` | Modify | Add `listAuditSessions`, `getAuditSession`, `listAuditCalls`, `getAuditCall`, `downloadAuditCallPayload`. ~80 LOC added. |
| `web/src/components/WorkspaceDetail.tsx` | Modify | Re-add an `'exec-audit'` tab union member, nav mapping entry, sidebar item, render branch. Use `Activity` icon from lucide. ~10 LOC added. |
| `web/src/components/ManageWorkspaces.tsx` | Modify | Add `'exec-audit'` to validTabs literal. ~1 LOC. |

---

## Task 1: API client functions in api.ts

**Files:**
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Add type re-exports from the generated schema**

Edit `web/src/lib/api.ts`. Find the section where types are re-exported from `api-generated/schema` (near line 48, where the deleted `OperationRecord` re-exports lived). Add:

```typescript
export type AuditSessionSummary = components['schemas']['AuditSessionSummary']
export type AuditCallSummary = components['schemas']['AuditCallSummary']
export type AuditCallDetail = components['schemas']['AuditCallDetail']
export type AuditSessionDetail = components['schemas']['AuditSessionDetail']
export type ListAuditSessionsResponse = components['schemas']['ListAuditSessionsResponse']
export type ListAuditCallsResponse = components['schemas']['ListAuditCallsResponse']
```

(These types only exist if Plan 2a's swagger regen has landed. If `pnpm build` fails with `Cannot find ... AuditSessionSummary`, the prerequisite isn't met.)

- [ ] **Step 2: Add the fetcher functions**

Append at the end of `web/src/lib/api.ts` (or after the existing section headers in alphabetical-ish order — match the existing convention):

```typescript
// === Exec-Audit ===

export interface ListAuditSessionsFilters {
  exe_id?: string
  user_id?: string
  turn_id?: string
  since?: string  // RFC3339
  until?: string  // RFC3339
  limit?: number
}

export async function listAuditSessions(
  workspaceId: string,
  filters: ListAuditSessionsFilters = {},
): Promise<AuditSessionSummary[]> {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(filters)) {
    if (v !== undefined && v !== null && v !== '') qs.set(k, String(v))
  }
  const path = `/api/workspaces/${encodeURIComponent(workspaceId)}/exec-audit/sessions${qs.toString() ? `?${qs}` : ''}`
  const data = await apiFetch<ListAuditSessionsResponse>({ method: 'GET', path })
  return data.sessions ?? []
}

export async function getAuditSession(
  workspaceId: string,
  sessionId: string,
): Promise<AuditSessionDetail> {
  const path = `/api/workspaces/${encodeURIComponent(workspaceId)}/exec-audit/sessions/${encodeURIComponent(sessionId)}`
  return apiFetch<AuditSessionDetail>({ method: 'GET', path })
}

export interface ListAuditCallsFilters {
  exe_id?: string
  user_id?: string
  source?: 'envmcp' | 'rest' | 'relay'
  method?: string
  is_error?: boolean
  since?: string
  until?: string
  limit?: number
}

export async function listAuditCalls(
  workspaceId: string,
  filters: ListAuditCallsFilters = {},
): Promise<AuditCallSummary[]> {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(filters)) {
    if (v !== undefined && v !== null && v !== '') qs.set(k, String(v))
  }
  const path = `/api/workspaces/${encodeURIComponent(workspaceId)}/exec-audit/calls${qs.toString() ? `?${qs}` : ''}`
  const data = await apiFetch<ListAuditCallsResponse>({ method: 'GET', path })
  return data.calls ?? []
}

export async function getAuditCall(
  workspaceId: string,
  callId: string,
): Promise<AuditCallDetail> {
  const path = `/api/workspaces/${encodeURIComponent(workspaceId)}/exec-audit/calls/${encodeURIComponent(callId)}`
  return apiFetch<AuditCallDetail>({ method: 'GET', path })
}

// downloadAuditCallPayload returns a blob URL caller should use as an
// <a href> for download. side = 'request' | 'response'.
export async function downloadAuditCallPayload(
  workspaceId: string,
  callId: string,
  side: 'request' | 'response',
): Promise<{ blob: Blob; filename: string }> {
  const path = `/api/workspaces/${encodeURIComponent(workspaceId)}/exec-audit/calls/${encodeURIComponent(callId)}/payload?side=${side}`
  const resp = await fetch(path, { credentials: 'include' })
  if (resp.status === 404) {
    throw new Error('payload not stored (size exceeded cap)')
  }
  if (!resp.ok) {
    throw new Error(`download failed: HTTP ${resp.status}`)
  }
  const blob = await resp.blob()
  const filename = `${callId}.${side}.bin`
  return { blob, filename }
}
```

- [ ] **Step 3: Verify build**

```bash
cd web && pnpm build 2>&1 | tail -10
cd - >/dev/null
```

Expected: clean. If `components['schemas']['AuditSessionSummary']` is undefined, Plan 2a isn't merged yet — stop and escalate.

- [ ] **Step 4: Commit**

```bash
git add web/src/lib/api.ts
git commit -m "$(cat <<'EOF'
feat(web/api): exec-audit fetchers

listAuditSessions / getAuditSession / listAuditCalls / getAuditCall +
downloadAuditCallPayload. The download helper handles the 404 case
(payload was over the storage hard cap; only sha256 + size are kept)
with a typed error so the UI can show a clear message.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: ExecAuditPanel skeleton — tab toggle + empty states

**Files:**
- Create: `web/src/components/ExecAuditPanel.tsx`

- [ ] **Step 1: Write the component skeleton with both tabs as empty placeholders**

Create `web/src/components/ExecAuditPanel.tsx`:

```tsx
import { useState } from 'react'
import { Activity, MessageSquare } from 'lucide-react'
import clsx from 'clsx'

type Tab = 'sessions' | 'calls'

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

function TabButton({ active, onClick, icon, children }: {
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

// CallsTab and SessionsTab implemented in next tasks.
function CallsTab({ workspaceId }: { workspaceId: string }) {
  return <div className="text-zinc-500">Calls tab — workspace {workspaceId}</div>
}
function SessionsTab({ workspaceId }: { workspaceId: string }) {
  return <div className="text-zinc-500">Sessions tab — workspace {workspaceId}</div>
}
```

- [ ] **Step 2: Verify build**

```bash
cd web && pnpm build 2>&1 | tail -10
cd - >/dev/null
```

Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/ExecAuditPanel.tsx
git commit -m "$(cat <<'EOF'
feat(web): ExecAuditPanel skeleton with Calls/Sessions tab toggle

CallsTab and SessionsTab are placeholder components — wired with real
data in the next two commits. Default tab = Calls since that's the
more common debugging entry point.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Calls tab — fetch + table + filter bar

**Files:**
- Modify: `web/src/components/ExecAuditPanel.tsx`

- [ ] **Step 1: Replace the CallsTab placeholder with the full implementation**

Edit `web/src/components/ExecAuditPanel.tsx`. Replace the `CallsTab` function with:

```tsx
import { useEffect, useState } from 'react'
import { listAuditCalls, type AuditCallSummary, type ListAuditCallsFilters } from '../lib/api'
import { AlertCircle, ChevronDown, ChevronRight, CheckCircle2 } from 'lucide-react'

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

  useEffect(() => {
    void refresh()
    // re-fetch when filters change
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
              <CallRow key={c.id} call={c} workspaceId={workspaceId}
                       expanded={expanded === c.id} onToggle={() => setExpanded(expanded === c.id ? null : c.id)} />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function CallRow({ call, workspaceId, expanded, onToggle }: {
  call: AuditCallSummary
  workspaceId: string
  expanded: boolean
  onToggle: () => void
}) {
  return (
    <>
      <tr onClick={onToggle}
          className="border-t border-zinc-200 dark:border-zinc-800 hover:bg-zinc-50 dark:hover:bg-zinc-900 cursor-pointer">
        <td className="px-3 py-2 text-zinc-400">
          {expanded ? <ChevronDown size={14}/> : <ChevronRight size={14}/>}
        </td>
        <td className="px-3 py-2 font-mono text-xs">{call.started_at?.slice(11, 19) ?? '—'}</td>
        <td className="px-3 py-2"><SourceBadge source={call.source} /></td>
        <td className="px-3 py-2 font-mono text-xs">{call.user_id ? short(call.user_id) : <span className="text-zinc-400">—</span>}</td>
        <td className="px-3 py-2 font-mono text-xs">{short(call.exe_id)}</td>
        <td className="px-3 py-2">{call.rpc_method ?? <span className="text-zinc-400">—</span>}</td>
        <td className="px-3 py-2">
          {call.is_error
            ? <span className="flex items-center gap-1 text-red-600"><AlertCircle size={14}/>error</span>
            : <span className="flex items-center gap-1 text-emerald-600"><CheckCircle2 size={14}/>ok</span>
          }
        </td>
        <td className="px-3 py-2 text-right font-mono text-xs">{call.duration_ms != null ? `${call.duration_ms}ms` : '—'}</td>
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
  const color = source === 'envmcp' ? 'bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300'
              : source === 'rest'   ? 'bg-purple-100 text-purple-700 dark:bg-purple-950 dark:text-purple-300'
              :                       'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300'
  return <span className={clsx('px-1.5 py-0.5 rounded text-xs font-medium', color)}>{source}</span>
}

function short(s: string): string {
  if (s.length <= 12) return s
  return s.slice(0, 6) + '…' + s.slice(-4)
}

function CallsFilterBar({ filters, onChange, loading, onRefresh }: {
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
          onChange={(e) => onChange({ ...filters, source: (e.target.value || undefined) as ListAuditCallsFilters['source'] })}
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
        <input type="checkbox"
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
```

(Move the `import clsx from 'clsx'` to the top of the file if not already present.)

- [ ] **Step 2: Stub out ExecAuditCallDetail so the build still succeeds**

For now, add a minimal placeholder at the top of `ExecAuditPanel.tsx` so the build doesn't break:

```tsx
function ExecAuditCallDetail({ workspaceId, callId }: { workspaceId: string; callId: string }) {
  return <div className="text-zinc-500">Detail for {callId} in {workspaceId} (TODO: Task 4)</div>
}
```

This is the **one place** in this plan where a TODO is acceptable — the next task replaces it.

- [ ] **Step 3: Verify build**

```bash
cd web && pnpm build 2>&1 | tail -10
cd - >/dev/null
```

- [ ] **Step 4: Commit**

```bash
git add web/src/components/ExecAuditPanel.tsx
git commit -m "$(cat <<'EOF'
feat(web): ExecAuditPanel — Calls tab with filter bar + expandable rows

Filterable by source / method / exe_id / errors. Click a row to expand
the inline detail (placeholder for now — populated in next commit).
SourceBadge color-codes envmcp/rest/relay for quick visual triage.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Call detail component

**Files:**
- Create: `web/src/components/ExecAuditCallDetail.tsx`
- Modify: `web/src/components/ExecAuditPanel.tsx` — replace placeholder with real import

- [ ] **Step 1: Write the detail component**

Create `web/src/components/ExecAuditCallDetail.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Download, AlertCircle } from 'lucide-react'
import { getAuditCall, downloadAuditCallPayload, type AuditCallDetail } from '../lib/api'

interface Props {
  workspaceId: string
  callId: string
}

export default function ExecAuditCallDetail({ workspaceId, callId }: Props) {
  const [call, setCall] = useState<AuditCallDetail | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    getAuditCall(workspaceId, callId)
      .then((c) => { if (!cancelled) setCall(c) })
      .catch((e: unknown) => { if (!cancelled) setError(e instanceof Error ? e.message : String(e)) })
    return () => { cancelled = true }
  }, [workspaceId, callId])

  if (error) return <div className="text-red-600 text-sm">{error}</div>
  if (!call) return <div className="text-zinc-500 text-sm">Loading…</div>

  return (
    <div className="grid grid-cols-2 gap-4">
      <Section label="Metadata">
        <KV k="ID" v={call.id} mono />
        {call.session_id && <KV k="Session" v={call.session_id} mono />}
        {call.rpc_id && <KV k="RPC ID" v={call.rpc_id} mono />}
        {call.rpc_kind && <KV k="Kind" v={call.rpc_kind} />}
        <KV k="Started" v={call.started_at ?? '—'} mono />
        <KV k="Completed" v={call.completed_at ?? '—'} mono />
        {call.error_summary && (
          <div className="flex items-start gap-1 text-red-600 text-sm">
            <AlertCircle size={14} className="mt-0.5"/>
            <span>{call.error_summary}</span>
          </div>
        )}
      </Section>
      <Section label="Sizes">
        <KV k="Request" v={fmtSize(call.request_size)} />
        {call.request_sha256 && <KV k="Req SHA256" v={call.request_sha256.slice(0, 16) + '…'} mono />}
        <KV k="Response" v={fmtSize(call.response_size)} />
        {call.response_sha256 && <KV k="Resp SHA256" v={call.response_sha256.slice(0, 16) + '…'} mono />}
      </Section>
      <Section label="Request preview" cols={2}>
        <Preview body={call.request_preview} fullSize={call.request_size}
                 onDownload={() => download(workspaceId, callId, 'request', setError)} />
      </Section>
      <Section label="Response preview" cols={2}>
        <Preview body={call.response_preview} fullSize={call.response_size}
                 onDownload={() => download(workspaceId, callId, 'response', setError)} />
      </Section>
    </div>
  )
}

function Section({ label, cols, children }: { label: string; cols?: 1 | 2; children: React.ReactNode }) {
  return (
    <div className={cols === 2 ? 'col-span-2' : ''}>
      <div className="text-xs uppercase text-zinc-500 mb-1">{label}</div>
      <div className="flex flex-col gap-1">{children}</div>
    </div>
  )
}

function KV({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="flex gap-2 text-sm">
      <span className="text-zinc-500 min-w-[80px]">{k}</span>
      <span className={mono ? 'font-mono text-xs' : ''}>{v}</span>
    </div>
  )
}

function Preview({ body, fullSize, onDownload }: {
  body: string | undefined
  fullSize: number
  onDownload: () => void
}) {
  const truncated = (body?.length ?? 0) < fullSize
  return (
    <div>
      <pre className="bg-zinc-100 dark:bg-zinc-950 rounded p-2 max-h-64 overflow-auto text-xs">
        {body ?? '(no body)'}
      </pre>
      <div className="flex items-center gap-3 mt-1 text-xs text-zinc-500">
        {truncated && fullSize > 0 && <span>truncated ({fmtSize(body?.length ?? 0)} of {fmtSize(fullSize)})</span>}
        <button onClick={onDownload}
                disabled={fullSize === 0}
                className="ml-auto flex items-center gap-1 text-zinc-700 dark:text-zinc-300 hover:text-zinc-900 disabled:opacity-30">
          <Download size={12}/> Download full
        </button>
      </div>
    </div>
  )
}

async function download(workspaceId: string, callId: string, side: 'request' | 'response', setError: (s: string) => void) {
  try {
    const { blob, filename } = await downloadAuditCallPayload(workspaceId, callId, side)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  } catch (e: unknown) {
    setError(e instanceof Error ? e.message : String(e))
  }
}

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(2)} MiB`
}
```

- [ ] **Step 2: Replace the placeholder in ExecAuditPanel**

Edit `web/src/components/ExecAuditPanel.tsx`. Remove the temporary `function ExecAuditCallDetail(...)` placeholder, and at the top of the file add:

```tsx
import ExecAuditCallDetail from './ExecAuditCallDetail'
```

- [ ] **Step 3: Verify build**

```bash
cd web && pnpm build 2>&1 | tail -10
cd - >/dev/null
```

- [ ] **Step 4: Commit**

```bash
git add web/src/components/ExecAuditCallDetail.tsx web/src/components/ExecAuditPanel.tsx
git commit -m "$(cat <<'EOF'
feat(web): ExecAuditCallDetail — preview + download

Two-column metadata + sizes summary, full-width request/response preview
boxes (first 8 KiB returned by the server), and a Download Full button
per side. Download gracefully fails when the server returns 404 (payload
was over the 4 MiB hard cap and bytes were never stored).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Sessions tab + session detail

**Files:**
- Create: `web/src/components/ExecAuditSessionDetail.tsx`
- Modify: `web/src/components/ExecAuditPanel.tsx` — replace SessionsTab placeholder

- [ ] **Step 1: Build the SessionsTab implementation**

Edit `web/src/components/ExecAuditPanel.tsx`. Replace the `SessionsTab` function with a full implementation analogous to `CallsTab` but with the columns: time / exe / user / stream / duration / frames (to-backend / to-client) / bytes. Use a similar expandable-row pattern; the expansion shows the `ExecAuditSessionDetail` component.

The session-list filter bar should have: exe_id, user_id, turn_id, since/until.

- [ ] **Step 2: Build ExecAuditSessionDetail**

Create `web/src/components/ExecAuditSessionDetail.tsx` showing the session metadata + the `first_calls` list (compact table, no expansion — clicking jumps to Calls tab with `session_id` filter pre-applied; future enhancement, link out via `useNavigate` if a route exists).

Since `getAuditSession` returns `AuditSessionDetail = { session, first_calls }`, render both.

- [ ] **Step 3: Verify build**

```bash
cd web && pnpm build 2>&1 | tail
cd - >/dev/null
```

- [ ] **Step 4: Commit**

```bash
git add web/src/components/ExecAuditSessionDetail.tsx web/src/components/ExecAuditPanel.tsx
git commit -m "$(cat <<'EOF'
feat(web): ExecAuditPanel — Sessions tab + session detail

Same layout as Calls — filter bar, table, expandable rows. Detail shows
session metadata plus the session's first 20 calls inline.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Wire the panel into WorkspaceDetail

**Files:**
- Modify: `web/src/components/WorkspaceDetail.tsx`
- Modify: `web/src/components/ManageWorkspaces.tsx`

- [ ] **Step 1: Add the tab to WorkspaceDetail**

Edit `web/src/components/WorkspaceDetail.tsx`. Four edits, mirroring what was deleted in Plan 1's T7:

a) Add the import near the other component imports:
```tsx
import ExecAuditPanel from './ExecAuditPanel'
import { Activity } from 'lucide-react'  // re-add if it was fully removed in Plan 1
```

b) Add `'exec-audit'` to the `tab` union:
```tsx
| 'exec-audit'
```

c) Add to `TAB_TO_SLUG`:
```tsx
  'exec-audit': 'exec-audit',
```

d) Add to the sidebar items array (insert after `traces` or `credentials` — your call on ordering):
```tsx
  { key: 'exec-audit', label: 'Exec Audit', icon: <Activity size={16} /> },
```

e) Add the render branch in the tab body:
```tsx
  {tab === 'exec-audit' && (
    <ExecAuditPanel workspaceId={workspace.id} />
  )}
```

- [ ] **Step 2: Add to ManageWorkspaces validTabs**

Edit `web/src/components/ManageWorkspaces.tsx`. In the `validTabs` array (line ~18 after Plan 1), add `'exec-audit'`:

```typescript
    'llm', 'im', 'traces', 'exec-audit', 'credentials', 'members', 'api-keys', 'settings',
```

- [ ] **Step 3: Build + lint**

```bash
cd web && pnpm build 2>&1 | tail
cd web && pnpm lint 2>&1 | tail
cd - >/dev/null
```

Expected: clean (or only pre-existing warnings unrelated to our changes).

- [ ] **Step 4: Commit**

```bash
git add web/src/components/WorkspaceDetail.tsx web/src/components/ManageWorkspaces.tsx
git commit -m "$(cat <<'EOF'
feat(web): mount ExecAuditPanel as 'exec-audit' tab on WorkspaceDetail

Restores the Activity icon from lucide-react (removed in Plan 1 when
OperationsPanel went away). URL slug 'exec-audit' is bookmarkable;
ManageWorkspaces validTabs accepts it.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Verification + open PR

- [ ] **Step 1: Full sweep**

```bash
cd web && pnpm build && pnpm lint 2>&1 | tail
cd - >/dev/null
```

Both clean (or pre-existing).

- [ ] **Step 2: Visual smoke-test (recommended, may need helm dev cluster)**

```bash
make dev 2>&1 &
# Navigate to a workspace in browser, click the "Exec Audit" tab, verify:
#   - empty-state shows "No calls." when no data
#   - filter bar inputs render
#   - if Plan 2b is also live: a real call shows in the table after pressing Refresh
```

If the dev stack isn't readily available, skip this step and note in the report that visual verification must happen post-deploy.

- [ ] **Step 3: Push + open PR**

```bash
git push -u github HEAD
gh pr create --base main \
  --title "feat(web): ExecAuditPanel — Calls/Sessions tabs with detail + download" \
  --body "$(cat <<'EOF'
## Summary

Adds the ExecAuditPanel to WorkspaceDetail, replacing the slot OperationsPanel held before its removal in #198.

- New \`ExecAuditPanel.tsx\` with two tabs (Calls + Sessions)
- Per-tab filter bar (source / method / exe / user / errors / time)
- Expandable detail rows showing metadata, request/response preview, and full-payload download
- \`ExecAuditCallDetail.tsx\` and \`ExecAuditSessionDetail.tsx\` are extracted as siblings — keeps each file focused
- API client added to \`lib/api.ts\`
- WorkspaceDetail re-uses the \`Activity\` lucide icon for the tab

## Prerequisites

- Plan 2a (#TBD) merged — the OpenAPI-generated types referenced here.
- Plan 2b (#TBD) is recommended but not required — the panel renders empty states gracefully when no data is flowing.

## Test plan

- [x] \`pnpm build\` green
- [x] \`pnpm lint\` clean (relative to baseline)
- [ ] Post-deploy: navigate to a workspace, click "Exec Audit", verify empty-state renders
- [ ] Post-deploy with Plan 2b live: real data appears; expand row, request/response preview shows; Download Full produces a binary file

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 4: Report PR URL.**
