import { afterEach, describe, expect, it, vi } from "vitest"
import { ResourceAPI, type WorkspaceRepositorySetting } from "./api"

const workspaceId = "9271bfe5-68a4-484b-a2d3-e9f450a42d0c"
const actor = "3a18bfee-a152-422c-873f-f17dbd086f5a"
const source = { url: "https://code.byted.org/tce/rtm-aihub.git", ref: "main", workingDirectory: "." }
const saved: WorkspaceRepositorySetting = { workspaceId, source, version: 1, updatedBy: actor, updatedAt: "2026-10-09T02:00:00Z" }
afterEach(() => vi.unstubAllGlobals())

describe("workspace repository API", () => {
  it("accepts never-configured, saved and explicitly cleared settings", async () => {
    for (const setting of [{ workspaceId, source: null, version: 0 }, saved, { ...saved, source: null, version: 2 }]) {
      vi.stubGlobal("fetch", vi.fn(async () => Response.json({ setting })))
      expect((await new ResourceAPI("https://agent.example", "test").getRepository(workspaceId)).setting).toEqual(setting)
    }
  })
  it("sends a CAS update without carrying a Git secret", async () => {
    vi.stubGlobal("fetch", vi.fn(async (req: Request) => {
      expect(req.method).toBe("PATCH")
      expect(new URL(req.url).pathname).toBe(`/v2/workspaces/${workspaceId}/repository`)
      expect(await req.json()).toEqual({ source, expectedVersion: 0 })
      return Response.json({ setting: saved, changed: true })
    }))
    expect((await new ResourceAPI("https://agent.example", "test").updateRepository(workspaceId, { source, expectedVersion: 0 })).changed).toBe(true)
  })
  it.each([
    { ...saved, token: "secret" }, { ...saved, workspaceId: actor }, { ...saved, version: -1 },
    { ...saved, source: { ...source, token: "secret" } },
    { ...saved, source: { ...source, url: "https://user:secret@code.byted.org/tce/rtm-aihub.git" } },
    { ...saved, source: { ...source, workingDirectory: "../escape" } },
    { workspaceId, source, version: 0 },
    { workspaceId, source: null, version: 1 },
  ])("rejects corrupt/out-of-scope responses", async setting => {
    vi.stubGlobal("fetch", vi.fn(async () => Response.json({ setting })))
    await expect(new ResourceAPI("https://agent.example", "test").getRepository(workspaceId)).rejects.toThrow()
  })
})
