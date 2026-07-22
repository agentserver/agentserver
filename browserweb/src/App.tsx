import { useState } from 'react'
import { A2uiSurface } from '@a2ui/react/v0_9'
import { useAgent } from './useAgent'

function tokenFromUrl(): string {
  return new URLSearchParams(window.location.search).get('token') ?? ''
}

export function App() {
  const [token, setToken] = useState(tokenFromUrl)
  if (!token) return <TokenGate onSubmit={setToken} />
  return <Chat token={token} />
}

function TokenGate({ onSubmit }: { onSubmit: (t: string) => void }) {
  const [v, setV] = useState('')
  return (
    <div className="mx-auto max-w-md p-8">
      <h1 className="mb-2 text-lg font-semibold">browser-gateway</h1>
      <p className="mb-4 text-sm text-gray-600">Paste a workspace codex token to connect.</p>
      <input className="w-full rounded border p-2" value={v} onChange={(e) => setV(e.target.value)} placeholder="token" />
      <button className="mt-3 rounded bg-black px-4 py-2 text-white" onClick={() => v.trim() && onSubmit(v.trim())}>Connect</button>
    </div>
  )
}

function Chat({ token }: { token: string }) {
  const { chat, surfaces, send, running, error } = useAgent(token)
  const [input, setInput] = useState('')
  const submit = () => { send(input); setInput('') }
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3 p-4">
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}
      <div className="flex flex-col gap-2">
        {chat.messages.map((m) => (
          <div key={m.id} className={m.role === 'user' ? 'self-end rounded bg-blue-100 px-3 py-2' : 'self-start rounded bg-gray-100 px-3 py-2'}>
            <span className="whitespace-pre-wrap">{m.text}</span>
          </div>
        ))}
        {chat.tools.map((t) => (
          <div key={t.id} className="self-start rounded border border-gray-300 px-3 py-2 font-mono text-xs">
            <div className="font-semibold">{t.name}</div>
            <div className="whitespace-pre-wrap text-gray-700">{t.args}</div>
          </div>
        ))}
        {surfaces.map((s) => <A2uiSurface key={s.id} surface={s} />)}
      </div>
      <div className="flex gap-2">
        <input className="flex-1 rounded border p-2" value={input} onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && submit()} placeholder={running ? 'running…' : 'message'} disabled={running} />
        <button className="rounded bg-black px-4 py-2 text-white disabled:opacity-50" onClick={submit} disabled={running}>Send</button>
      </div>
    </div>
  )
}
