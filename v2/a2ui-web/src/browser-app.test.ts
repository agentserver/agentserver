import { readFileSync } from "node:fs"
import { describe, expect, it } from "vitest"
import type { SessionTrajectoryRecord } from "@agentserver/v2-web-shared"
import { mergeTrajectoryTailRecords, prependTrajectoryRecords } from "./browser-app"
import { deriveTrajectoryTimeline, filterTrajectoryRecords, groupTrajectoryRecords } from "./trajectory-view"

describe("Browser product source", () => {
  it("uses generated SDKs instead of hand-written HTTP paths", () => {
    const source = readFileSync(new URL("./browser-app.tsx", import.meta.url), "utf8")
    expect(source).not.toMatch(/\bfetch\s*\(/u)
    expect(source).not.toMatch(/["'`]\/v2\//u)
    expect(source).toContain("new ResourceAPI")
    expect(source).toContain("new EdgeAPI")
  })

  it("binds each run to the session selected when the prompt is submitted", () => {
    const source = readFileSync(new URL("./browser-app.tsx", import.meta.url), "utf8")
    expect(source).toContain("interface ActiveRun {\n  sessionId: string")
    expect(source).toContain('const sessionId = run?.sessionId ?? ""')
    expect(source).toContain("activeRun.current = { sessionId,")
    expect(source).toContain("selectionRevisionRef.current !== selectionRevision")
    expect(source).not.toContain("selectedIdRef.current = selectedId }, [selectedId]")
    expect(source).toContain("activeRun.current?.controller?.abort()")
  })

  it("reloads the durable transcript when a session is restored or selected", () => {
    const source = readFileSync(new URL("./browser-app.tsx", import.meta.url), "utf8")
    expect(source).toContain("api.getSessionTranscript(workspaceId, sessionId)")
    expect(source).toContain("conversationFromTranscript(transcript)")
    expect(source).toContain("transcriptRevisionRef.current !== revision")
    expect(source).toContain("void loadTranscript(selectedId)")
  })

  it("renders one ordered conversation projection and moves pending approvals into the composer", () => {
    const source = readFileSync(new URL("./browser-app.tsx", import.meta.url), "utf8")
    expect(source).toContain("state.timeline.map((item)")
    expect(source).toContain('item.kind === "tool-result"')
    expect(source).toContain('data-conversation-kind="execution"')
    expect(source).toContain("ownedSurfaces(state, \"tool\", item.id)")
    expect(source).toContain("<ApprovalPanel approval={approval} onDecision={onDecision} />")
    expect(source).toContain("<MarkdownText text={message.text}")
    expect(source).not.toContain("state.messages.map((message) => <article")
    expect(source).not.toContain("state.approvalOrder.map((id) => state.approvals[id]).filter")
  })

  it("renders a tail-paged session Trajectory with live polling and an inspector", () => {
    const source = readFileSync(new URL("./browser-app.tsx", import.meta.url), "utf8")
    const trajectorySource = readFileSync(new URL("./trajectory-view.tsx", import.meta.url), "utf8")
    expect(source).toContain("api.getSessionTrajectory(workspaceId, sessionId, undefined, 100)")
    expect(source).toContain("api.getSessionTrajectory(workspaceId, sessionId, current.nextBefore, 100)")
    expect(source).toContain("mergeTrajectoryTailRecords(current.records, loaded.records)")
    expect(source).toContain("prependTrajectoryRecords(loaded.records, latest.records)")
    expect(source).toContain("await loadTrajectoryTail(selectedId, true)")
    expect(source).toContain("window.setTimeout(() => { void poll() }, 1200)")
    expect(source).toContain('role="tab" aria-selected={view === "trajectory"}')
    expect(source).toContain("<TrajectoryView")
    expect(source).toContain("permissionControl={<PermissionModeSelector")
    expect(trajectorySource).toContain("permissionControl?: ReactNode")
    expect(trajectorySource).toContain('data-trajectory-scroll')
    expect(trajectorySource).toContain("trajectory-lane-track")
    expect(trajectorySource).toContain("trajectory-run-groups")
    expect(trajectorySource).toContain('t("browser.inputLane")')
    expect(trajectorySource).not.toContain('t("browser.controlLane")')
    expect(trajectorySource).toContain("function TrajectoryInspectorSection")
    expect(trajectorySource).toContain("record.failure.message")
  })

  it("restores a prompt after a permission-mode CAS race", () => {
    const source = readFileSync(new URL("./browser-app.tsx", import.meta.url), "utf8")
    expect(source).toContain('error.status === 409 && error.code === "version_conflict" && run.permissionModeVersion > 0')
    expect(source).toContain("const promptToRestore = run.prompt")
    expect(source).toContain("activeRun.current = null")
    expect(source).toContain("setPrompt(promptToRestore)")
    expect(source).toContain("await loadSessions(sessionId)")
    expect(source).toContain("await loadTranscript(sessionId)")
  })

  it("keeps historical records while accepting authoritative tail order and updates", () => {
    const a = trajectoryRecord("event:a", "old a")
    const b = trajectoryRecord("event:b", "old b")
    const c = trajectoryRecord("event:c", "old c")
    const updatedB = trajectoryRecord("event:b", "updated b")
    const updatedC = trajectoryRecord("event:c", "updated c")
    const d = trajectoryRecord("event:d", "new d")

    const tail = mergeTrajectoryTailRecords([a, c, b], [updatedB, updatedC, d])
    expect(tail.map((record) => record.id)).toEqual(["event:a", "event:b", "event:c", "event:d"])
    expect(tail[1]?.summary).toBe("updated b")

    const history = prependTrajectoryRecords([a, b], [updatedB, updatedC, d])
    expect(history.map((record) => record.id)).toEqual(["event:a", "event:b", "event:c", "event:d"])
    expect(history[1]?.summary).toBe("updated b")
  })

  it("filters lifecycle noise while preserving the hierarchy needed to explain a match", () => {
    const run = trajectoryRecord("run:r", "Run completed", { kind: "run", status: "succeeded" })
    const attempt = trajectoryRecord("attempt:a", "Attempt succeeded", { kind: "attempt", status: "succeeded", parentId: run.id })
    const tool = trajectoryRecord("tool:t", "transport network failure", { kind: "tool", status: "failed", parentId: attempt.id })
    const event = trajectoryRecord("event:e", "run.finalizing transport", { parentId: attempt.id })
    const records = [run, attempt, event, tool]

    expect(filterTrajectoryRecords(records, { query: "transport", showLifecycle: false, problemsOnly: false }).map(({ id }) => id))
      .toEqual(["run:r", "tool:t"])
    expect(filterTrajectoryRecords(records, { query: "", showLifecycle: false, problemsOnly: true }).map(({ id }) => id))
      .toEqual(["run:r", "tool:t"])
    expect(filterTrajectoryRecords(records, { query: "finalizing", showLifecycle: true, problemsOnly: false }).map(({ id }) => id))
      .toEqual(["run:r", "attempt:a", "event:e"])
  })

  it("shows only input, model, and tool records in the default ledger", () => {
    const run = trajectoryRecord("run:r", "Run completed", { kind: "run", status: "succeeded" })
    const input = trajectoryRecord("input:r", "User input", { kind: "input", status: "succeeded", parentId: run.id })
    const attempt = trajectoryRecord("attempt:a", "Attempt succeeded", { kind: "attempt", status: "succeeded", parentId: run.id })
    const assistant = trajectoryRecord("assistant:m", "Assistant response", { kind: "assistant", status: "succeeded", parentId: attempt.id })
    const tool = trajectoryRecord("tool:t", "Tool completed", { kind: "tool", status: "succeeded", parentId: attempt.id })
    const execution = trajectoryRecord("execution:e", "Execution succeeded", { kind: "execution", status: "succeeded", parentId: tool.id })
    const sandbox = trajectoryRecord("sandbox:s", "Sandbox ready", { kind: "sandbox", status: "succeeded", parentId: attempt.id })
    const records = [run, input, attempt, sandbox, assistant, tool, execution]

    expect(filterTrajectoryRecords(records, { query: "", showLifecycle: false, problemsOnly: false }).map(({ id }) => id))
      .toEqual(["run:r", "input:r", "assistant:m", "tool:t"])
    expect(filterTrajectoryRecords(records, { query: "", showLifecycle: true, problemsOnly: false })).toEqual(records)
  })

  it("groups records by run and derives actual-time and sequence swimlanes", () => {
    const run = trajectoryRecord("run:r", "Run completed", {
      kind: "run", status: "succeeded", startedAt: "2026-08-15T01:00:00Z", completedAt: "2026-08-15T01:00:10Z",
    })
    const attempt = trajectoryRecord("attempt:a", "Attempt succeeded", {
      kind: "attempt", status: "succeeded", parentId: run.id, startedAt: "2026-08-15T01:00:02Z", completedAt: "2026-08-15T01:00:09Z",
    })
    const tool = trajectoryRecord("tool:t", "Tool completed", {
      kind: "tool", status: "succeeded", parentId: attempt.id, startedAt: "2026-08-15T01:00:03Z", completedAt: "2026-08-15T01:00:02.990Z",
    })
    const input = trajectoryRecord("input:r", "User input", {
      kind: "input", status: "succeeded", parentId: run.id, startedAt: "2026-08-15T01:00:00Z", completedAt: "2026-08-15T01:00:00Z",
    })
    const model = trajectoryRecord("assistant:m", "Assistant response", {
      kind: "assistant", status: "succeeded", parentId: attempt.id, startedAt: "2026-08-15T01:00:02Z", completedAt: "2026-08-15T01:00:08Z",
    })
    const lifecycle = trajectoryRecord("event:leased", "attempt.leased", {
      kind: "event", status: "info", parentId: attempt.id, startedAt: "2026-08-15T01:00:01Z", completedAt: "2026-08-15T01:00:01Z",
    })
    const records = [run, input, lifecycle, attempt, model, tool]

    const groups = groupTrajectoryRecords(records, "2026-08-15T01:00:20Z")
    expect(groups).toHaveLength(1)
    expect(groups[0]?.run?.id).toBe(run.id)
    expect(groups[0]?.startedAt).toBe(Date.parse("2026-08-15T01:00:00Z"))
    expect(groups[0]?.completedAt).toBe(Date.parse("2026-08-15T01:00:10Z"))

    const actual = deriveTrajectoryTimeline(records, "2026-08-15T01:00:20Z", "actual")
    expect(actual?.start).toBe(Date.parse("2026-08-15T01:00:00Z"))
    expect(actual?.end).toBe(Date.parse("2026-08-15T01:00:08Z"))
    expect(actual?.spans.find(({ record }) => record.id === tool.id)).toMatchObject({ lane: 2, start: Date.parse(tool.startedAt), end: Date.parse(tool.startedAt) })

    const sequence = deriveTrajectoryTimeline(records, "2026-08-15T01:00:20Z", "sequence")
    expect(actual?.spans.find(({ record }) => record.id === lifecycle.id)).toMatchObject({ lane: 2 })

    expect(sequence).toMatchObject({ start: 0, end: 4 })
    expect(sequence?.spans.map(({ lane, start, end }) => ({ lane, start, end }))).toEqual([
      { lane: 0, start: 0, end: 1 },
      { lane: 2, start: 1, end: 2 },
      { lane: 1, start: 2, end: 3 },
      { lane: 2, start: 3, end: 4 },
    ])
  })
})

function trajectoryRecord(id: string, summary: string, overrides: Partial<SessionTrajectoryRecord> = {}): SessionTrajectoryRecord {
  return {
    id, kind: "event", status: "info", title: id, summary,
    runId: "9271bfe5-68a4-484b-a2d3-e9f450a42d0c",
    startedAt: "2026-08-15T01:00:00Z", details: [],
    ...overrides,
  }
}
