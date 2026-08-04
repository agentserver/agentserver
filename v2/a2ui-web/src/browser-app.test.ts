import { readFileSync } from "node:fs"
import { describe, expect, it } from "vitest"

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
})
