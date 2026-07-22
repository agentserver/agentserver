export type ChatMessage = { id: string; role: 'user' | 'assistant'; text: string }
export type ToolCall = { id: string; name: string; args: string }
export type ChatState = { messages: ChatMessage[]; tools: ToolCall[] }

export const emptyChatState: ChatState = { messages: [], tools: [] }

// reduceEvent applies one AG-UI event to the chat state. Pure: returns the
// same reference when the event does not affect chat/tool state.
export function reduceEvent(state: ChatState, ev: { type: string; [k: string]: any }): ChatState {
  switch (ev.type) {
    case 'TEXT_MESSAGE_START':
      return { ...state, messages: [...state.messages, { id: ev.messageId, role: 'assistant', text: '' }] }
    case 'TEXT_MESSAGE_CONTENT':
      return {
        ...state,
        messages: state.messages.map((m) => (m.id === ev.messageId ? { ...m, text: m.text + (ev.delta ?? '') } : m)),
      }
    case 'TOOL_CALL_START':
      return { ...state, tools: [...state.tools, { id: ev.toolCallId, name: ev.toolCallName, args: '' }] }
    case 'TOOL_CALL_ARGS':
      return {
        ...state,
        tools: state.tools.map((t) => (t.id === ev.toolCallId ? { ...t, args: t.args + (ev.delta ?? '') } : t)),
      }
    default:
      return state // TEXT_MESSAGE_END, TOOL_CALL_END, CUSTOM, RUN_*, etc. — no chat-state change here
  }
}

// addUserMessage appends a user-authored message (used when the user submits input).
export function addUserMessage(state: ChatState, id: string, text: string): ChatState {
  return { ...state, messages: [...state.messages, { id, role: 'user', text }] }
}
