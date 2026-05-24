import { useEffect, useState } from 'react'
import { getAuditSession, type AuditSessionDetail } from '../lib/api'

interface Props {
  workspaceId: string
  sessionId: string
}

export default function ExecAuditSessionDetail({ workspaceId, sessionId }: Props) {
  const [detail, setDetail] = useState<AuditSessionDetail | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    getAuditSession(workspaceId, sessionId)
      .then((d) => {
        if (!cancelled) setDetail(d)
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [workspaceId, sessionId])

  if (error) return <div className="text-red-600 text-sm">{error}</div>
  if (!detail) return <div className="text-zinc-500 text-sm">Loading…</div>

  const s = detail.session
  const calls = detail.first_calls ?? []

  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-4">
        <Section label="Metadata">
          <KV k="ID" v={s.id} mono />
          {s.user_id && <KV k="User" v={s.user_id} mono />}
          <KV k="Exe" v={s.exe_id} mono />
          {s.turn_id && <KV k="Turn" v={s.turn_id} mono />}
          <KV k="Stream" v={s.stream_id} mono />
          {s.client_ip && <KV k="Client IP" v={s.client_ip} mono />}
          <KV k="Opened" v={s.opened_at ?? '—'} mono />
          <KV k="Closed" v={s.closed_at || '— (open)'} mono />
          {s.close_reason && <KV k="Close reason" v={s.close_reason} />}
        </Section>
        <Section label="Frames / bytes">
          <KV k="Frames →" v={String(s.frames_to_backend ?? 0)} />
          <KV k="Frames ←" v={String(s.frames_to_client ?? 0)} />
          <KV k="Bytes →" v={fmtSize(s.bytes_to_backend ?? 0)} />
          <KV k="Bytes ←" v={fmtSize(s.bytes_to_client ?? 0)} />
        </Section>
      </div>

      <div>
        <div className="text-xs uppercase text-zinc-500 mb-1">First {calls.length} calls</div>
        <div className="rounded border border-zinc-200 dark:border-zinc-800 overflow-hidden">
          <table className="w-full text-xs">
            <thead className="bg-zinc-50 dark:bg-zinc-900 text-zinc-500 uppercase">
              <tr>
                <th className="text-left px-2 py-1">Time</th>
                <th className="text-left px-2 py-1">Method</th>
                <th className="text-right px-2 py-1">Req size</th>
              </tr>
            </thead>
            <tbody>
              {calls.length === 0 && (
                <tr><td colSpan={3} className="px-2 py-3 text-center text-zinc-500">No calls recorded.</td></tr>
              )}
              {calls.map((c) => (
                <tr key={c.id} className="border-t border-zinc-200 dark:border-zinc-800">
                  <td className="px-2 py-1 font-mono">{c.started_at?.slice(11, 19) ?? '—'}</td>
                  <td className="px-2 py-1">{c.rpc_method ?? <span className="text-zinc-400">—</span>}</td>
                  <td className="px-2 py-1 text-right font-mono">{c.request_size ?? 0}B</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="text-xs uppercase text-zinc-500 mb-1">{label}</div>
      <div className="flex flex-col gap-1">{children}</div>
    </div>
  )
}

function KV({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="flex gap-2 text-sm">
      <span className="text-zinc-500 min-w-[100px]">{k}</span>
      <span className={mono ? 'font-mono text-xs break-all' : ''}>{v}</span>
    </div>
  )
}

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / 1024 / 1024).toFixed(2)} MiB`
}
