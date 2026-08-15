import { readFileSync } from "node:fs"
import { describe, expect, it } from "vitest"
import type { SessionTrajectoryRecord } from "@agentserver/v2-web-shared"
import { mergeTrajectoryTailRecords, prependTrajectoryRecords } from "./browser-app"

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

  it("renders a tail-paged session Trajectory with live polling and an inspector", () => {
    const source = readFileSync(new URL("./browser-app.tsx", import.meta.url), "utf8")
    expect(source).toContain("api.getSessionTrajectory(workspaceId, sessionId, undefined, 100)")
    expect(source).toContain("api.getSessionTrajectory(workspaceId, sessionId, current.nextBefore, 100)")
    expect(source).toContain("mergeTrajectoryTailRecords(current.records, loaded.records)")
    expect(source).toContain("prependTrajectoryRecords(loaded.records, latest.records)")
    expect(source).toContain("await loadTrajectoryTail(selectedId, true)")
    expect(source).toContain("window.setTimeout(() => { void poll() }, 1200)")
    expect(source).toContain('role="tab" aria-selected={view === "trajectory"}')
    expect(source).toContain('data-trajectory-scroll')
    expect(source).toContain("function TrajectoryInspectorSection")
    expect(source).toContain("selected.failure.message")
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
})

function trajectoryRecord(id: string, summary: string): SessionTrajectoryRecord {
  return {
    id, kind: "event", status: "info", title: id, summary,
    runId: "9271bfe5-68a4-484b-a2d3-e9f450a42d0c",
    startedAt: "2026-08-15T01:00:00Z", details: [],
  }
}
