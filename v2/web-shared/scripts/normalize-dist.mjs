import { readFile, readdir, writeFile } from "node:fs/promises"
import { extname, join } from "node:path"

for (const root of ["platform-web/dist", "a2ui-web/dist"]) await normalizeTree(root)

async function normalizeTree(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const target = join(directory, entry.name)
    if (entry.isDirectory()) await normalizeTree(target)
    else if ([".html", ".css", ".js", ".json"].includes(extname(entry.name))) {
      const source = await readFile(target, "utf8")
      const normalized = `${source.replace(/[ \t]+$/gmu, "").replace(/\n*$/u, "")}\n`
      if (normalized !== source) await writeFile(target, normalized, "utf8")
    }
  }
}
