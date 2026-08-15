import { Children, isValidElement, memo, useDeferredValue, useMemo, useState, type ReactNode } from "react"
import ReactMarkdown, { type Components, type UrlTransform } from "react-markdown"
import remarkGfm from "remark-gfm"
import { Check, Copy } from "lucide-react"

export interface MarkdownTextProps {
  text: string
  streaming?: boolean
  copyLabel: string
  copiedLabel: string
}

const markdownPlugins = [remarkGfm]

const safeMarkdownURL: UrlTransform = (value, key) => {
  if (key === "href" && value.startsWith("#")) return value
  try {
    const url = new URL(value)
    if (url.protocol === "https:" || url.protocol === "http:" || (key === "href" && url.protocol === "mailto:")) return value
  } catch {
    // Conversation Markdown has no trustworthy base for relative URLs.
  }
  return ""
}

function nodeText(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node)
  if (Array.isArray(node)) return node.map(nodeText).join("")
  if (isValidElement<{ children?: ReactNode }>(node)) return nodeText(node.props.children)
  return ""
}

function MarkdownCodeBlock({ children, copyLabel, copiedLabel }: { children: ReactNode; copyLabel: string; copiedLabel: string }) {
  const [copied, setCopied] = useState(false)
  const codeElement = Children.toArray(children).find((child) => isValidElement(child))
  const className = isValidElement<{ className?: string }>(codeElement) ? codeElement.props.className ?? "" : ""
  const language = /(?:^|\s)language-([\w-]+)/u.exec(className)?.[1] ?? ""
  const code = nodeText(codeElement ?? children).replace(/\n$/u, "")
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      setCopied(false)
    }
  }
  return <div className="markdown-code-block">
    <div className="markdown-code-toolbar"><span>{language || "code"}</span><button type="button" onClick={() => void copy()} aria-label={copied ? copiedLabel : copyLabel}>{copied ? <Check size={13} /> : <Copy size={13} />}<span>{copied ? copiedLabel : copyLabel}</span></button></div>
    <pre><code className={className || undefined}>{code}</code></pre>
  </div>
}

const MarkdownDocument = memo(function MarkdownDocument({ text, components }: { text: string; components: Components }) {
  return <ReactMarkdown remarkPlugins={markdownPlugins} components={components} skipHtml urlTransform={safeMarkdownURL}>{text}</ReactMarkdown>
})

export function MarkdownText({ text, streaming = false, copyLabel, copiedLabel }: MarkdownTextProps) {
  const deferredText = useDeferredValue(text)
  const visibleText = streaming ? deferredText : text
  const components = useMemo<Components>(() => ({
    a: ({ node: _node, href, children, ...props }) => href ? <a {...props} href={href} target="_blank" rel="noreferrer noopener">{children}</a> : <span className="markdown-disabled-link">{children}</span>,
    img: ({ node: _node, src, alt, ...props }) => src ? <img {...props} src={src} alt={alt ?? ""} loading="lazy" referrerPolicy="no-referrer" /> : <span className="markdown-image-alt">[{alt}]</span>,
    pre: ({ node: _node, children }) => <MarkdownCodeBlock copyLabel={copyLabel} copiedLabel={copiedLabel}>{children}</MarkdownCodeBlock>,
  }), [copiedLabel, copyLabel])
  return <div className="markdown-body" data-streaming={streaming || undefined}>
    <MarkdownDocument text={visibleText} components={components} />
  </div>
}

export { safeMarkdownURL }
