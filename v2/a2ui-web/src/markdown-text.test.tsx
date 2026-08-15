import { renderToStaticMarkup } from "react-dom/server"
import { describe, expect, it } from "vitest"
import { MarkdownText, safeMarkdownURL } from "./markdown-text"

function render(text: string, streaming = false): string {
  return renderToStaticMarkup(<MarkdownText text={text} streaming={streaming} copyLabel="Copy" copiedLabel="Copied" />)
}

describe("conversation Markdown", () => {
  it("renders GFM prose, links, tables, tasks, and fenced code", () => {
    const html = render([
      "## Result",
      "",
      "这是**重点**内容。 **bold** ~~old~~ [docs](https://example.com/docs)",
      "",
      "- [x] done",
      "",
      "| name | value |",
      "| --- | ---: |",
      "| answer | 42 |",
      "",
      "```sh",
      "echo ok",
      "```",
    ].join("\n"))
    expect(html).toContain("<h2>Result</h2>")
    expect(html).toContain("<strong>bold</strong>")
    expect(html).toContain("这是<strong>重点</strong>内容")
    expect(html).toContain("<del>old</del>")
    expect(html).toContain('href="https://example.com/docs"')
    expect(html).toContain("<table>")
    expect(html).toContain('type="checkbox"')
    expect(html).toContain("markdown-code-block")
    expect(html).toContain("echo ok")
  })

  it("keeps incomplete streaming Markdown renderable", () => {
    expect(() => render("A growing **response\n\n```ts\nconst value = 1", true)).not.toThrow()
    expect(render("A growing **response", true)).toContain("A growing **response")
  })

  it("does not execute raw HTML or unsafe and relative URLs", () => {
    const html = render('<script>alert(1)</script> [bad](javascript:alert(1)) [relative](./secret)')
    expect(html).not.toContain("<script")
    expect(html).not.toContain("javascript:")
    expect(html).not.toContain("./secret")
    expect(safeMarkdownURL("mailto:hello@example.com", "href", {} as never)).toBe("mailto:hello@example.com")
    expect(safeMarkdownURL("data:text/html,hello", "href", {} as never)).toBe("")
  })
})
