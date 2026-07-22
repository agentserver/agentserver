import { describe, it, expect } from 'vitest'
import { reduceEvent, emptyChatState } from './agentState'

describe('reduceEvent', () => {
  it('assembles a streamed assistant message from START/CONTENT/END', () => {
    let s = emptyChatState
    s = reduceEvent(s, { type: 'TEXT_MESSAGE_START', messageId: 'm1', role: 'assistant' })
    s = reduceEvent(s, { type: 'TEXT_MESSAGE_CONTENT', messageId: 'm1', delta: 'Hel' })
    s = reduceEvent(s, { type: 'TEXT_MESSAGE_CONTENT', messageId: 'm1', delta: 'lo' })
    s = reduceEvent(s, { type: 'TEXT_MESSAGE_END', messageId: 'm1' })
    expect(s.messages).toEqual([{ id: 'm1', role: 'assistant', text: 'Hello' }])
  })

  it('records a tool call from START/ARGS/END', () => {
    let s = emptyChatState
    s = reduceEvent(s, { type: 'TOOL_CALL_START', toolCallId: 't1', toolCallName: 'shell' })
    s = reduceEvent(s, { type: 'TOOL_CALL_ARGS', toolCallId: 't1', delta: 'ls -la' })
    s = reduceEvent(s, { type: 'TOOL_CALL_END', toolCallId: 't1' })
    expect(s.tools).toEqual([{ id: 't1', name: 'shell', args: 'ls -la' }])
  })

  it('ignores unrelated events without mutating input', () => {
    const s0 = emptyChatState
    const s1 = reduceEvent(s0, { type: 'CUSTOM', name: 'a2ui.operations', value: [] })
    expect(s1).toBe(s0)
  })
})
