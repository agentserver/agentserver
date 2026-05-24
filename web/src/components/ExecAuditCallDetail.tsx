import { useEffect, useState } from 'react'
import { AlertCircle, Download } from 'lucide-react'
import { downloadAuditCallPayload, getAuditCall, type AuditCallDetail } from '../lib/api'

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
      .then((c) => {
        if (!cancelled) setCall(c)
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
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
            <AlertCircle size={14} className="mt-0.5" />
            <span>{call.error_summary}</span>
          </div>
        )}
      </Section>
      <Section label="Sizes">
        <KV k="Request" v={fmtSize(call.request_size ?? 0)} />
        {call.request_sha256 && <KV k="Req SHA256" v={call.request_sha256.slice(0, 16) + '…'} mono />}
        <KV k="Response" v={fmtSize(call.response_size ?? 0)} />
        {call.response_sha256 && <KV k="Resp SHA256" v={call.response_sha256.slice(0, 16) + '…'} mono />}
      </Section>
      <Section label="Request preview" cols={2}>
        <Preview
          body={call.request_preview}
          fullSize={call.request_size ?? 0}
          onDownload={() => downloadAndSave(workspaceId, callId, 'request', setError)}
        />
      </Section>
      <Section label="Response preview" cols={2}>
        <Preview
          body={call.response_preview}
          fullSize={call.response_size ?? 0}
          onDownload={() => downloadAndSave(workspaceId, callId, 'response', setError)}
        />
      </Section>
    </div>
  )
}

function Section({
  label,
  cols,
  children,
}: {
  label: string
  cols?: 1 | 2
  children: React.ReactNode
}) {
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
      <span className={mono ? 'font-mono text-xs break-all' : ''}>{v}</span>
    </div>
  )
}

function Preview({
  body,
  fullSize,
  onDownload,
}: {
  body: string | undefined
  fullSize: number
  onDownload: () => void
}) {
  const previewLen = body?.length ?? 0
  const truncated = previewLen < fullSize
  return (
    <div>
      <pre className="bg-zinc-100 dark:bg-zinc-950 rounded p-2 max-h-64 overflow-auto text-xs whitespace-pre-wrap break-all">
        {body ?? '(no body)'}
      </pre>
      <div className="flex items-center gap-3 mt-1 text-xs text-zinc-500">
        {truncated && fullSize > 0 && (
          <span>truncated ({fmtSize(previewLen)} of {fmtSize(fullSize)})</span>
        )}
        <button
          onClick={onDownload}
          disabled={fullSize === 0}
          className="ml-auto flex items-center gap-1 text-zinc-700 dark:text-zinc-300 hover:text-zinc-900 disabled:opacity-30"
        >
          <Download size={12} /> Download full
        </button>
      </div>
    </div>
  )
}

async function downloadAndSave(
  workspaceId: string,
  callId: string,
  side: 'request' | 'response',
  setError: (s: string) => void,
) {
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
