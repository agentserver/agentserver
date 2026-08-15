import { canonicalID } from "./utils"
import type { SessionTranscript } from "./api"

export const EVENT_CURSOR_NAME = "agentserver.event_cursor"
export const TOOL_PROGRESS_NAME = "agentserver.tool_progress"
export const RUN_STATUS_NAME = "agentserver.run_status"
export const APPROVAL_NAME = "agentserver.approval"
export const A2UI_OPERATIONS_NAME = "a2ui.operations"
export const MAXIMUM_EVENT_STREAM_BYTES = 16 * 1024 * 1024
const basicCatalog = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"

export type RunStatus = "idle" | "connecting" | "running" | "cancelling" | "completed" | "failed" | "cancelled" | "disconnected"
export interface ConversationMessage { id: string; role: string; text: string; complete: boolean }
export interface ReasoningMessage { id: string; text: string; complete: boolean }
export interface ToolView { id: string; name: string; arguments: string; result: string; status: string; progress: { value: number | null; total: number | null; message: string } | null }
export type ConversationItemKind = "message" | "reasoning" | "tool" | "approval" | "tool-result" | "surface"
export interface ConversationTimelineItem { kind: ConversationItemKind; id: string }
export interface ConversationSurfaceOwner { kind: "tool" | "approval"; id: string }
export interface ApprovalView {
  approvalId: string; executionId: string; runId: string; runAttemptId: string; runAttemptGeneration: number; nonce: string
  contextDigest: { domain: "approval-context"; canonicalizerVersion: "rfc8785-v1"; sha256: string }
  toolName: string; status: "pending" | "approved" | "denied" | "expired" | "cancelled" | "consumed"; decision: string
  approverId: string; expiresAt: string; version: number
}
export interface A2UIComponent { id: string; component: "Card" | "Column" | "Text"; child?: string; children?: string[]; text?: unknown }
export interface A2UISurface { id: string; components: A2UIComponent[]; dataModel: unknown; owner: ConversationSurfaceOwner | null }
export interface ConversationState {
  status: RunStatus; runId: string; cursor: string; cursorSequence: number; messages: ConversationMessage[]; reasoning: ReasoningMessage[]
  tools: ToolView[]; approvals: Record<string, ApprovalView>; approvalOrder: string[]; surfaces: Record<string, A2UISurface>; surfaceOrder: string[]
  timeline: ConversationTimelineItem[]; pendingSurfaceOwner: ConversationSurfaceOwner | null
  error: { code: string; message: string } | null; eventCount: number
}

export function createConversationState(): ConversationState {
  return { status: "idle", runId: "", cursor: "", cursorSequence: 0, messages: [], reasoning: [], tools: [], approvals: {}, approvalOrder: [], surfaces: {}, surfaceOrder: [], timeline: [], pendingSurfaceOwner: null, error: null, eventCount: 0 }
}

export function conversationFromTranscript(transcript: SessionTranscript): ConversationState {
  const state = createConversationState()
  state.messages = transcript.messages.map((message) => ({
    id: `${message.runId}:${message.messageId}`,
    role: message.role,
    text: message.content,
    complete: message.complete,
  }))
  state.timeline = state.messages.map((message) => ({ kind: "message", id: message.id }))
  return state
}

export function cloneConversationState(state: ConversationState): ConversationState { return structuredClone(state) }

export function appendUserMessage(state: ConversationState, id: string, text: string): ConversationState {
  const message = { id: requiredText("message ID", id), role: "user", text: requiredText("message", text), complete: true }
  return appendTimeline({ ...state, messages: [...state.messages, message], error: null }, "message", message.id)
}

export class SSEDecoder {
  #buffer = ""
  #data: string[] = []
  #seen = 0
  push(chunk: string): Record<string, unknown>[] {
    this.#seen += chunk.length
    if (this.#seen > MAXIMUM_EVENT_STREAM_BYTES) throw new Error("The AG-UI event stream exceeded 16 MiB.")
    this.#buffer += chunk
    const events: Record<string, unknown>[] = []
    while (true) {
      const newline = this.#buffer.indexOf("\n")
      if (newline < 0) break
      let line = this.#buffer.slice(0, newline)
      this.#buffer = this.#buffer.slice(newline + 1)
      if (line.endsWith("\r")) line = line.slice(0, -1)
      this.#line(line, events)
    }
    return events
  }
  finish(): Record<string, unknown>[] {
    const events: Record<string, unknown>[] = []
    if (this.#buffer) { this.#line(this.#buffer.replace(/\r$/u, ""), events); this.#buffer = "" }
    this.#dispatch(events)
    return events
  }
  #line(line: string, events: Record<string, unknown>[]) {
    if (!line) { this.#dispatch(events); return }
    if (line.startsWith(":")) return
    const separator = line.indexOf(":")
    const field = separator < 0 ? line : line.slice(0, separator)
    let value = separator < 0 ? "" : line.slice(separator + 1)
    if (value.startsWith(" ")) value = value.slice(1)
    if (field === "data") this.#data.push(value)
  }
  #dispatch(events: Record<string, unknown>[]) {
    if (!this.#data.length) return
    const raw = this.#data.join("\n"); this.#data = []
    const event = JSON.parse(raw) as unknown
    if (!isRecord(event) || typeof event.type !== "string" || !event.type) throw new Error("AG-UI returned an invalid event envelope.")
    events.push(event)
  }
}

export function reduceAGUIEvent(state: ConversationState, event: Record<string, unknown>): ConversationState {
  const surfaceContinuation = event.type === "CUSTOM" && event.name === A2UI_OPERATIONS_NAME
  let next: ConversationState = { ...state, eventCount: state.eventCount + 1, pendingSurfaceOwner: surfaceContinuation ? state.pendingSurfaceOwner : null }
  switch (event.type) {
    case "RUN_STARTED": next = { ...next, status: "running", runId: requiredText("run ID", event.runId), error: null }; break
    case "TEXT_MESSAGE_START": {
      const id = requiredText("message ID", event.messageId)
      if (next.messages.some((message) => message.id === id)) throw new Error("A message was started twice.")
      next = appendTimeline({ ...next, messages: [...next.messages, { id, role: typeof event.role === "string" ? event.role : "assistant", text: "", complete: false }] }, "message", id); break
    }
    case "TEXT_MESSAGE_CONTENT": next = { ...next, messages: update(next.messages, event.messageId, (message) => ({ ...message, text: message.text + requiredString("message delta", event.delta) })) }; break
    case "TEXT_MESSAGE_END": next = { ...next, messages: update(next.messages, event.messageId, (message) => ({ ...message, complete: true })) }; break
    case "REASONING_MESSAGE_START": {
      const id = requiredText("reasoning ID", event.messageId)
      if (next.reasoning.some((message) => message.id === id)) throw new Error("A reasoning message was started twice.")
      next = appendTimeline({ ...next, reasoning: [...next.reasoning, { id, text: "", complete: false }] }, "reasoning", id); break
    }
    case "REASONING_MESSAGE_CONTENT": next = { ...next, reasoning: update(next.reasoning, event.messageId, (message) => ({ ...message, text: message.text + requiredString("reasoning delta", event.delta) })) }; break
    case "REASONING_MESSAGE_END": next = { ...next, reasoning: update(next.reasoning, event.messageId, (message) => ({ ...message, complete: true })) }; break
    case "TOOL_CALL_START": {
      const id = requiredText("tool call ID", event.toolCallId)
      if (next.tools.some((tool) => tool.id === id)) throw new Error("A tool call was started twice.")
      next = appendTimeline({ ...next, tools: [...next.tools, { id, name: requiredText("tool name", event.toolCallName), arguments: "", result: "", status: "arguments", progress: null }] }, "tool", id); break
    }
    case "TOOL_CALL_ARGS": next = { ...next, tools: update(next.tools, event.toolCallId, (tool) => ({ ...tool, arguments: tool.arguments + requiredString("tool arguments", event.delta) })) }; break
    case "TOOL_CALL_END": next = { ...next, tools: update(next.tools, event.toolCallId, (tool) => ({ ...tool, status: "awaiting-result" })) }; break
    case "TOOL_CALL_RESULT": {
      const id = requiredText("event ID", event.toolCallId)
      next = appendTimeline({ ...next, tools: update(next.tools, id, (tool) => ({ ...tool, result: requiredString("tool result", event.content), status: "completed" })), pendingSurfaceOwner: { kind: "tool", id } }, "tool-result", id); break
    }
    case "CUSTOM": next = reduceCustom(next, event); break
    case "RUN_FINISHED": if (event.runId && next.runId && event.runId !== next.runId) throw new Error("A terminal event escaped the active run."); next = { ...next, status: "completed", error: null }; break
    case "RUN_ERROR": if (event.runId && next.runId && event.runId !== next.runId) throw new Error("A terminal event escaped the active run."); next = { ...next, status: event.code === "user_cancelled" || event.code === "run.cancelled" ? "cancelled" : "failed", error: { code: typeof event.code === "string" ? event.code : "run_error", message: requiredText("run error", event.message) } }; break
  }
  return next
}

function reduceCustom(state: ConversationState, event: Record<string, unknown>): ConversationState {
  const value = event.value
  if (event.name === EVENT_CURSOR_NAME) {
    if (!isRecord(value) || value.version !== 1 || typeof value.cursor !== "string" || !value.cursor || !Number.isSafeInteger(value.lastEventSequence) || Number(value.lastEventSequence) < 0 || (state.runId && value.runId !== state.runId)) throw new Error("The lifecycle cursor is invalid.")
    return { ...state, cursor: value.cursor, cursorSequence: Number(value.lastEventSequence) }
  }
  if (event.name === TOOL_PROGRESS_NAME) {
    if (!isRecord(value)) throw new Error("Tool progress is invalid.")
    return { ...state, tools: update(state.tools, value.toolCallId, (tool) => ({ ...tool, status: "running", progress: { value: typeof value.progress === "number" ? value.progress : null, total: typeof value.total === "number" ? value.total : null, message: typeof value.message === "string" ? value.message : "" } })) }
  }
  if (event.name === RUN_STATUS_NAME) {
    if (!isRecord(value) || value.status !== "cancelling" || (state.runId && value.runId !== state.runId)) throw new Error("The canonical run status is invalid.")
    return { ...state, status: "cancelling" }
  }
  if (event.name === APPROVAL_NAME) return reduceApproval(state, value)
  if (event.name === A2UI_OPERATIONS_NAME) return reduceA2UI(state, value)
  return state
}

function reduceApproval(state: ConversationState, raw: unknown): ConversationState {
  if (!isRecord(raw)) throw new Error("The approval payload is invalid.")
  const status = requiredText("approval status", raw.status) as ApprovalView["status"]
  if (!["pending", "approved", "denied", "expired", "cancelled", "consumed"].includes(status)) throw new Error("The approval status is unsupported.")
  const digest = raw.contextDigest
  if (!isRecord(digest) || digest.domain !== "approval-context" || digest.canonicalizerVersion !== "rfc8785-v1" || typeof digest.sha256 !== "string" || !/^[0-9a-f]{64}$/u.test(digest.sha256)) throw new Error("The approval digest is invalid.")
  const approval: ApprovalView = {
    approvalId: canonicalID("approval ID", requiredText("approval ID", raw.approvalId)), executionId: canonicalID("execution ID", requiredText("execution ID", raw.executionId)),
    runId: canonicalID("run ID", requiredText("run ID", raw.runId)), runAttemptId: canonicalID("run attempt ID", requiredText("run attempt ID", raw.runAttemptId)),
    runAttemptGeneration: positiveInteger(raw.runAttemptGeneration), nonce: canonicalID("approval nonce", requiredText("approval nonce", raw.nonce)),
    contextDigest: { domain: "approval-context", canonicalizerVersion: "rfc8785-v1", sha256: digest.sha256 }, toolName: requiredText("tool name", raw.toolName), status,
    decision: raw.decision === undefined ? "" : requiredString("decision", raw.decision), approverId: raw.approverId === undefined ? "" : requiredString("approver", raw.approverId),
    expiresAt: requiredText("approval expiry", raw.expiresAt), version: positiveInteger(raw.version),
  }
  if (state.runId && approval.runId !== state.runId) throw new Error("The approval escaped the active run.")
  if (!Number.isFinite(Date.parse(approval.expiresAt))) throw new Error("The approval expiry is invalid.")
  const existing = state.approvals[approval.approvalId]
  if (existing && approval.version < existing.version) throw new Error("The approval version moved backwards.")
  const next = {
    ...state,
    approvals: { ...state.approvals, [approval.approvalId]: approval },
    approvalOrder: existing ? state.approvalOrder : [...state.approvalOrder, approval.approvalId],
    pendingSurfaceOwner: { kind: "approval", id: approval.approvalId } as ConversationSurfaceOwner,
  }
  return existing ? next : appendTimeline(next, "approval", approval.approvalId)
}

function reduceA2UI(state: ConversationState, raw: unknown): ConversationState {
  if (!Array.isArray(raw) || !raw.length) throw new Error("A2UI operations are invalid.")
  const surfaces = structuredClone(state.surfaces)
  const order = [...state.surfaceOrder]
  let timeline = state.timeline
  for (const message of raw) {
    if (!isRecord(message) || message.version !== "v0.9") throw new Error("The A2UI version is unsupported.")
    const names = ["createSurface", "updateComponents", "updateDataModel", "deleteSurface"].filter((name) => message[name] !== undefined)
    if (names.length !== 1) throw new Error("An A2UI operation must contain exactly one command.")
    const name = names[0] ?? ""
    const operation = message[name]
    if (!isRecord(operation)) throw new Error("The A2UI operation is invalid.")
    const id = safeKey(requiredText("surface ID", operation.surfaceId))
    if (name === "createSurface") {
      if (surfaces[id] || operation.catalogId !== basicCatalog || operation.sendDataModel === true) throw new Error("The A2UI surface authority is invalid.")
      surfaces[id] = { id, components: [], dataModel: {}, owner: state.pendingSurfaceOwner }; order.push(id)
      if (!state.pendingSurfaceOwner) timeline = appendTimelineItems(timeline, "surface", id)
      continue
    }
    const surface = surfaces[id]
    if (!surface) throw new Error("An A2UI surface was updated before creation.")
    if (name === "deleteSurface") { delete surfaces[id]; order.splice(order.indexOf(id), 1); timeline = timeline.filter((item) => item.kind !== "surface" || item.id !== id); continue }
    if (name === "updateComponents") surfaces[id] = { ...surface, components: validateComponents(operation.components) }
    else surfaces[id] = { ...surface, dataModel: updatePointer(surface.dataModel, typeof operation.path === "string" ? operation.path : "", operation.value) }
  }
  return { ...state, surfaces, surfaceOrder: order, timeline, pendingSurfaceOwner: null }
}

function validateComponents(raw: unknown): A2UIComponent[] {
  if (!Array.isArray(raw) || !raw.length || raw.length > 512) throw new Error("A2UI components are outside protocol bounds.")
  const components = structuredClone(raw) as A2UIComponent[]
  const ids = new Set<string>()
  for (const component of components) {
    if (!isRecord(component) || !["Card", "Column", "Text"].includes(component.component)) throw new Error("An A2UI component is unsupported.")
    component.id = safeKey(requiredText("component ID", component.id)); if (ids.has(component.id)) throw new Error("An A2UI component is duplicated."); ids.add(component.id)
  }
  if (!ids.has("root")) throw new Error("A2UI requires a root component.")
  return components
}

function updatePointer(current: unknown, pointer: string, value: unknown): unknown {
  if (!pointer) return structuredClone(value)
  if (!pointer.startsWith("/")) throw new Error("A2UI data paths must be JSON Pointers.")
  const root = isRecord(current) || Array.isArray(current) ? structuredClone(current) : {}
  const segments = pointer.slice(1).split("/").map((segment) => safeKey(segment.replace(/~1/gu, "/").replace(/~0/gu, "~")))
  let target = root as Record<string, unknown>
  for (const segment of segments.slice(0, -1)) { if (!isRecord(target[segment])) target[segment] = {}; target = target[segment] as Record<string, unknown> }
  const last = segments.at(-1); if (last !== undefined) target[last] = structuredClone(value)
  return root
}

export function resolveJSONPointer(value: unknown, pointer: string): unknown {
  if (!pointer) return value
  if (!pointer.startsWith("/")) return undefined
  let current = value
  for (const segment of pointer.slice(1).split("/").map((part) => part.replace(/~1/gu, "/").replace(/~0/gu, "~"))) {
    if ((isRecord(current) || Array.isArray(current)) && Object.prototype.hasOwnProperty.call(current, segment)) current = (current as Record<string, unknown>)[segment]
    else return undefined
  }
  return current
}

function update<T extends { id: string }>(items: T[], rawId: unknown, callback: (item: T) => T): T[] {
  const id = requiredText("event ID", rawId); let found = false
  const result = items.map((item) => { if (item.id !== id) return item; found = true; return callback(item) })
  if (!found) throw new Error("An AG-UI event referenced state before its start event.")
  return result
}
function appendTimeline(state: ConversationState, kind: ConversationItemKind, id: string): ConversationState {
  const timeline = appendTimelineItems(state.timeline, kind, id)
  return timeline === state.timeline ? state : { ...state, timeline }
}
function appendTimelineItems(items: ConversationTimelineItem[], kind: ConversationItemKind, id: string): ConversationTimelineItem[] {
  if (items.some((item) => item.kind === kind && item.id === id)) return items
  return [...items, { kind, id }]
}
function isRecord(value: unknown): value is Record<string, any> { return Boolean(value) && typeof value === "object" && !Array.isArray(value) }
function requiredText(label: string, value: unknown): string { if (typeof value !== "string" || !value || /[\0\r\n]/u.test(value)) throw new Error(`${label} is invalid.`); return value }
function requiredString(label: string, value: unknown): string { if (typeof value !== "string") throw new Error(`${label} is invalid.`); return value }
function positiveInteger(value: unknown): number { if (!Number.isSafeInteger(value) || Number(value) < 1) throw new Error("A protocol version is invalid."); return Number(value) }
function safeKey(value: string): string { if (["__proto__", "prototype", "constructor"].includes(value)) throw new Error("An unsafe object key was rejected."); return value }
