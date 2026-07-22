import { useEffect, useMemo, useState } from 'react'
import { HttpAgent } from '@ag-ui/client'
import { MessageProcessor, SurfaceModel } from '@a2ui/web_core/v0_9'
import { basicCatalog } from '@a2ui/react/v0_9'
import type { ReactComponentImplementation } from '@a2ui/react/v0_9'
import { reduceEvent, addUserMessage, emptyChatState, type ChatState } from './agentState'

type Surface = SurfaceModel<ReactComponentImplementation>

export function useAgent(token: string) {
  const agent = useMemo(() => new HttpAgent({ url: '/agui', headers: { Authorization: `Bearer ${token}` } }), [token])
  const processor = useMemo(() => new MessageProcessor([basicCatalog]), [])

  const [chat, setChat] = useState<ChatState>(emptyChatState)
  const [surfaces, setSurfaces] = useState<Surface[]>([])
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const syncSurfaces = () => setSurfaces(Array.from(processor.model.surfacesMap.values()) as Surface[])
    const created = processor.onSurfaceCreated(syncSurfaces)
    const deleted = processor.onSurfaceDeleted(syncSurfaces)
    const sub = agent.subscribe({
      onTextMessageStartEvent: ({ event }) => setChat((s) => reduceEvent(s, event as any)),
      onTextMessageContentEvent: ({ event }) => setChat((s) => reduceEvent(s, event as any)),
      onToolCallStartEvent: ({ event }) => setChat((s) => reduceEvent(s, event as any)),
      onToolCallArgsEvent: ({ event }) => setChat((s) => reduceEvent(s, event as any)),
      onCustomEvent: ({ event }) => {
        if ((event as any).name === 'a2ui.operations') processor.processMessages((event as any).value)
      },
      onRunErrorEvent: ({ event }) => setError((event as any).message ?? 'run error'),
    })
    return () => { sub.unsubscribe(); created.unsubscribe(); deleted.unsubscribe() }
  }, [agent, processor])

  const send = (text: string) => {
    if (!text.trim() || running) return
    setError(null)
    setChat((s) => addUserMessage(s, crypto.randomUUID(), text))
    agent.addMessage({ id: crypto.randomUUID(), role: 'user', content: text })
    setRunning(true)
    agent.runAgent().catch((e) => setError(String(e))).finally(() => setRunning(false))
  }

  return { chat, surfaces, send, running, error }
}
