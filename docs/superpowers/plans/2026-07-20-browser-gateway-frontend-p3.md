# browser-gateway frontend + deploy (P3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a `browserweb` reference SPA (Vite + React 19) that connects to the browser-gateway `POST /agui` endpoint, renders streaming chat + tool calls, and renders the gateway's `a2ui.operations` CUSTOM events as A2UI cards — then embed it in the Go binary and wire deployment (Dockerfile frontend stage, CI, Helm) so a single `browser-gateway` container serves both the endpoint and the UI.

**Architecture:** A **pure client-side** SPA (no Node/Next.js runtime): `@ag-ui/client`'s `HttpAgent` talks HTTP+SSE directly to `/agui`; `@a2ui/react`'s `MessageProcessor` + `<A2uiSurface>` render the A2UI v0.9 cards from `onCustomEvent`. The built `dist` is embedded via `//go:embed` and served by the gateway's HTTP server. CopilotKit is intentionally NOT used (it requires a separate Node runtime, incompatible with single-binary deploy).

**Tech Stack:** Vite 7 + React 19 + Tailwind 4 (mirroring the repo's existing `web/`); `@ag-ui/client@0.0.57`, `@a2ui/react@0.10.2`, `@a2ui/web_core@0.10.5`, `zod@^3.25.76`; Go `embed`; GitHub Actions; Helm.

**Builds on:** P1 (`browser-gateway-p1`, PR #292) + P2 (`browser-gateway-p2`, PR #293). **Stacked branch off `browser-gateway-p2`** (`b277303`). Spec: `docs/superpowers/specs/2026-07-20-browser-gateway-design.md`.

## Global Constraints

- Repo module `github.com/agentserver/agentserver`, Go 1.26. Branch: stack on `browser-gateway-p2` (base `b277303`). Subagents run in `/root/agentserver` (NO worktree — subagent cwd pins to repo root). npm registry IS reachable in this environment (frontend can be installed/built).
- **Frontend stack (exact, verified on npm):** deps `@ag-ui/client@0.0.57`, `@a2ui/react@0.10.2`, `@a2ui/web_core@0.10.5`, `react@^19.2.0`, `react-dom@^19.2.0`, `zod@^3.25.76`; devDeps `vite@^7`, `@vitejs/plugin-react@^5`, `typescript@~5.9`, `@tailwindcss/vite@^4`, `tailwindcss@^4`, `@types/react@^19`, `@types/react-dom@^19`, `vitest@^3`. **Pin one `zod@^3.25.76`** (satisfies both @ag-ui and @a2ui; a stray zod 3.22 breaks A2UI). Use TS `moduleResolution: "bundler"` so the `/v0_9` subpath exports resolve.
- **AG-UI client API (verified):** `import { HttpAgent } from "@ag-ui/client"`. Construct `new HttpAgent({ url, headers: { Authorization: "Bearer "+token } })`. Subscribe via `agent.subscribe({ onTextMessageStartEvent, onTextMessageContentEvent, onTextMessageEndEvent, onToolCallStartEvent, onToolCallArgsEvent, onToolCallEndEvent, onCustomEvent, onRunFinishedEvent, onRunErrorEvent })` → returns `{ unsubscribe }`. Each callback gets `{ event, ... }`; `event` is a typed object. Send a turn: `agent.addMessage({ id, role: "user", content })` then `await agent.runAgent()`. The agent owns `messages`/`threadId`/`state`.
- **CUSTOM → A2UI carrier:** `onCustomEvent: ({ event }) => { if (event.name === "a2ui.operations") processor.processMessages(event.value) }`. `event.value` is the A2UI v0.9 message array.
- **A2UI renderer API (verified):** `import { MessageProcessor } from "@a2ui/web_core/v0_9"`; `import { A2uiSurface, basicCatalog } from "@a2ui/react/v0_9"`; import CSS `@a2ui/react/styles/structural.css`. `const processor = new MessageProcessor([basicCatalog]); processor.processMessages(msgs);` Surfaces live in `processor.model.surfacesMap` (a `Map<string, SurfaceModel>`); subscribe to changes via `processor.onSurfaceCreated(fn)` / `processor.onSurfaceDeleted(fn)` (each returns `{ unsubscribe }`). Render `<A2uiSurface surface={s} />` per surface; it self-updates on later `updateComponents`/`updateDataModel`.
- **Auth:** the SPA obtains a workspace codex token (minted by the console `POST /api/codex/tokens`, verified by CXG `RemoteVerifier`) and sends it as the Bearer. v1 UX: read `?token=` from the URL, else a paste field; store in memory only.
- **Serve:** built `dist` embedded via `browserweb/embed.go` (`//go:embed all:dist`) and served by the gateway server for `GET /` + non-API paths, with SPA fallback to `index.html`. `/agui`, `/healthz` keep precedence.
- **Deploy patterns (mirror existing):** Dockerfile — root `Dockerfile` builds `web/` in a `node:25-slim` pnpm stage then `COPY --from=frontend dist` into the Go build context (embed at Go-build time). CI — `.github/workflows/build.yml` has one job per image (login → `docker/metadata-action` `images: ghcr.io/agentserver/<name>` → `docker/build-push-action` `file: ./Dockerfile.<name>`); mirror `build-credentialproxy`. Helm — `deploy/helm/agentserver/templates/<name>.yaml` + a `values.yaml` block + httproute; browser-gateway's is a plain Deployment+Service (no S3/secrets), env `BRG_CODEX_APP_GATEWAY_WS_URL` pointing at the in-cluster codex-app-gateway service.
- TDD where there's real logic (the pure event→state reducer; the Go serve route). Frontend components verified by `pnpm build` (tsc + vite) succeeding. `gofmt -w` Go; commit per task.

---

### Task 1: Scaffold `browserweb` (build + deps resolve)

**Files:**
- Create: `browserweb/package.json`, `browserweb/vite.config.ts`, `browserweb/tsconfig.json`, `browserweb/tsconfig.app.json`, `browserweb/tsconfig.node.json`, `browserweb/index.html`, `browserweb/src/main.tsx`, `browserweb/src/App.tsx`, `browserweb/src/index.css`, `browserweb/.gitignore`

**Interfaces:**
- Produces: a buildable Vite app; `pnpm build` emits `browserweb/dist/`.

- [ ] **Step 1: package.json**

`browserweb/package.json`:
```json
{
  "name": "browserweb",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "test": "vitest run"
  },
  "dependencies": {
    "@a2ui/react": "0.10.2",
    "@a2ui/web_core": "0.10.5",
    "@ag-ui/client": "0.0.57",
    "react": "^19.2.0",
    "react-dom": "^19.2.0",
    "zod": "^3.25.76"
  },
  "devDependencies": {
    "@tailwindcss/vite": "^4.2.1",
    "@types/react": "^19.2.7",
    "@types/react-dom": "^19.2.3",
    "@vitejs/plugin-react": "^5.1.1",
    "tailwindcss": "^4.2.1",
    "typescript": "~5.9.3",
    "vite": "^7.3.1",
    "vitest": "^3.2.0"
  }
}
```

- [ ] **Step 2: vite + tsconfig + gitignore**

`browserweb/vite.config.ts`:
```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: { '/agui': 'http://localhost:8088' },
  },
})
```
`browserweb/tsconfig.json`:
```json
{ "files": [], "references": [{ "path": "./tsconfig.app.json" }, { "path": "./tsconfig.node.json" }] }
```
`browserweb/tsconfig.app.json`:
```json
{
  "compilerOptions": {
    "target": "ES2022", "useDefineForClassFields": true, "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext", "skipLibCheck": true, "moduleResolution": "bundler",
    "allowImportingTsExtensions": true, "verbatimModuleSyntax": true, "moduleDetection": "force",
    "noEmit": true, "jsx": "react-jsx", "strict": true, "noUnusedLocals": true,
    "noUnusedParameters": true, "noFallthroughCasesInSwitch": true, "types": ["vitest/globals"]
  },
  "include": ["src"]
}
```
`browserweb/tsconfig.node.json`:
```json
{
  "compilerOptions": {
    "target": "ES2023", "lib": ["ES2023"], "module": "ESNext", "skipLibCheck": true,
    "moduleResolution": "bundler", "allowImportingTsExtensions": true, "noEmit": true
  },
  "include": ["vite.config.ts"]
}
```
`browserweb/.gitignore`:
```
node_modules
dist
```

- [ ] **Step 3: index.html + minimal app + css**

`browserweb/index.html`:
```html
<!doctype html>
<html lang="en">
  <head><meta charset="UTF-8" /><meta name="viewport" content="width=device-width, initial-scale=1.0" /><title>browser-gateway</title></head>
  <body><div id="root"></div><script type="module" src="/src/main.tsx"></script></body>
</html>
```
`browserweb/src/index.css`:
```css
@import "tailwindcss";
@import "@a2ui/react/styles/structural.css";
```
`browserweb/src/main.tsx`:
```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { App } from './App'

createRoot(document.getElementById('root')!).render(<StrictMode><App /></StrictMode>)
```
`browserweb/src/App.tsx` (placeholder; replaced in Task 4):
```tsx
export function App() {
  return <div className="p-4">browser-gateway UI</div>
}
```

- [ ] **Step 4: install + build**

Run:
```bash
cd /root/agentserver/browserweb && pnpm install && pnpm build
```
Expected: install resolves all deps from npm (no peer/zod conflicts), `pnpm build` runs `tsc -b` clean and emits `browserweb/dist/index.html` + assets. If `pnpm` complains about a missing lockfile flag, plain `pnpm install` (which writes `pnpm-lock.yaml`) is correct here — commit the lockfile.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add browserweb/package.json browserweb/pnpm-lock.yaml browserweb/vite.config.ts browserweb/tsconfig*.json browserweb/index.html browserweb/.gitignore browserweb/src/
git commit -m "feat(browser-gateway): scaffold browserweb Vite+React SPA"
```

---

### Task 2: Pure AG-UI event → chat state reducer (tested)

**Files:**
- Create: `browserweb/src/agentState.ts`
- Test: `browserweb/src/agentState.test.ts`

**Interfaces:**
- Produces:
  - `type ChatMessage = { id: string; role: "user" | "assistant"; text: string }`
  - `type ToolCall = { id: string; name: string; args: string }`
  - `type ChatState = { messages: ChatMessage[]; tools: ToolCall[] }`
  - `const emptyChatState: ChatState`
  - `function reduceEvent(state: ChatState, ev: { type: string; [k: string]: any }): ChatState` — pure; applies one AG-UI event.

- [ ] **Step 1: Write the failing test**

`browserweb/src/agentState.test.ts`:
```ts
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentserver/browserweb && pnpm test`
Expected: FAIL (cannot import `reduceEvent`/`emptyChatState`).

- [ ] **Step 3: Implement the reducer**

`browserweb/src/agentState.ts`:
```ts
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/agentserver/browserweb && pnpm test`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add browserweb/src/agentState.ts browserweb/src/agentState.test.ts
git commit -m "feat(browser-gateway): pure AG-UI event→chat-state reducer + tests"
```

---

### Task 3: `useAgent` hook — HttpAgent + A2UI processor wiring

**Files:**
- Create: `browserweb/src/useAgent.ts`

**Interfaces:**
- Consumes: `HttpAgent` from `@ag-ui/client`; `MessageProcessor` + `basicCatalog` from `@a2ui/*/v0_9`; `reduceEvent`/`addUserMessage`/`ChatState`/`emptyChatState` (Task 2).
- Produces: `function useAgent(token: string): { chat: ChatState; surfaces: SurfaceModel<...>[]; send: (text: string) => void; running: boolean; error: string | null }`

- [ ] **Step 1: Implement the hook**

`browserweb/src/useAgent.ts`:
```ts
import { useEffect, useMemo, useState } from 'react'
import { HttpAgent } from '@ag-ui/client'
import { MessageProcessor } from '@a2ui/web_core/v0_9'
import { basicCatalog } from '@a2ui/react/v0_9'
import type { SurfaceModel, ReactComponentImplementation } from '@a2ui/react/v0_9'
import { reduceEvent, addUserMessage, emptyChatState, type ChatState } from './agentState'

type Surface = SurfaceModel<ReactComponentImplementation>

export function useAgent(token: string) {
  const agent = useMemo(() => new HttpAgent({ url: '/agui', headers: { Authorization: `Bearer ${token}` } }), [token])
  const processor = useMemo(() => new MessageProcessor([basicCatalog]), [])

  const [chat, setChat] = useState<ChatState>(emptyChatState)
  const [surfaces, setSurfaces] = useState<Surface[]>([])
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const syncSurfaces = () => setSurfaces(Array.from(processor.model.surfacesMap.values()) as Surface[])
    const created = processor.onSurfaceCreated(syncSurfaces)
    const deleted = processor.onSurfaceDeleted(syncSurfaces)
    const sub = agent.subscribe({
      onTextMessageStartEvent: ({ event }) => setChat((s) => reduceEvent(s, event as any)),
      onTextMessageContentEvent: ({ event }) => setChat((s) => reduceEvent(s, event as any)),
      onToolCallStartEvent: ({ event }) => setChat((s) => reduceEvent(s, event as any)),
      onToolCallArgsEvent: ({ event }) => setChat((s) => reduceEvent(s, event as any)),
      onCustomEvent: ({ event }) => {
        if ((event as any).name === 'a2ui.operations') processor.processMessages((event as any).value)
      },
      onRunErrorEvent: ({ event }) => setError((event as any).message ?? 'run error'),
    })
    return () => { sub.unsubscribe(); created.unsubscribe(); deleted.unsubscribe() }
  }, [agent, processor])

  const send = (text: string) => {
    if (!text.trim() || running) return
    setError(null)
    setChat((s) => addUserMessage(s, crypto.randomUUID(), text))
    agent.addMessage({ id: crypto.randomUUID(), role: 'user', content: text })
    setRunning(true)
    agent.runAgent().catch((e) => setError(String(e))).finally(() => setRunning(false))
  }

  return { chat, surfaces, send, running, error }
}
```
(Note: `event as any` casts are deliberate — the subscriber gives typed events, but we read a small common subset via the reducer; keep the reducer as the single typed surface. If TS flags a missing `SurfaceModel`/`ReactComponentImplementation` export path, they are exported from `@a2ui/react/v0_9` per the renderer — adjust the import to the type's actual export if the compiler disagrees.)

- [ ] **Step 2: Typecheck (build)**

Run: `cd /root/agentserver/browserweb && pnpm build`
Expected: `tsc -b` passes (the hook typechecks against the real @ag-ui / @a2ui types) and vite builds. If a subscriber callback name or an @a2ui type import differs from the above, fix it against the installed package's `.d.ts` (the API is verified but exact type-export paths must satisfy the compiler). Do genuine type-driven fixes; don't `// @ts-nocheck`.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver
git add browserweb/src/useAgent.ts
git commit -m "feat(browser-gateway): useAgent hook (HttpAgent + A2UI processor)"
```

---

### Task 4: Chat UI + A2UI surface rendering

**Files:**
- Modify: `browserweb/src/App.tsx`

**Interfaces:**
- Consumes: `useAgent` (Task 3); `A2uiSurface` from `@a2ui/react/v0_9`.

- [ ] **Step 1: Implement App**

`browserweb/src/App.tsx`:
```tsx
import { useState } from 'react'
import { A2uiSurface } from '@a2ui/react/v0_9'
import { useAgent } from './useAgent'

function tokenFromUrl(): string {
  return new URLSearchParams(window.location.search).get('token') ?? ''
}

export function App() {
  const [token, setToken] = useState(tokenFromUrl)
  if (!token) return <TokenGate onSubmit={setToken} />
  return <Chat token={token} />
}

function TokenGate({ onSubmit }: { onSubmit: (t: string) => void }) {
  const [v, setV] = useState('')
  return (
    <div className="mx-auto max-w-md p-8">
      <h1 className="mb-2 text-lg font-semibold">browser-gateway</h1>
      <p className="mb-4 text-sm text-gray-600">Paste a workspace codex token to connect.</p>
      <input className="w-full rounded border p-2" value={v} onChange={(e) => setV(e.target.value)} placeholder="token" />
      <button className="mt-3 rounded bg-black px-4 py-2 text-white" onClick={() => v.trim() && onSubmit(v.trim())}>Connect</button>
    </div>
  )
}

function Chat({ token }: { token: string }) {
  const { chat, surfaces, send, running, error } = useAgent(token)
  const [input, setInput] = useState('')
  const submit = () => { send(input); setInput('') }
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3 p-4">
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>}
      <div className="flex flex-col gap-2">
        {chat.messages.map((m) => (
          <div key={m.id} className={m.role === 'user' ? 'self-end rounded bg-blue-100 px-3 py-2' : 'self-start rounded bg-gray-100 px-3 py-2'}>
            <span className="whitespace-pre-wrap">{m.text}</span>
          </div>
        ))}
        {chat.tools.map((t) => (
          <div key={t.id} className="self-start rounded border border-gray-300 px-3 py-2 font-mono text-xs">
            <div className="font-semibold">{t.name}</div>
            <div className="whitespace-pre-wrap text-gray-700">{t.args}</div>
          </div>
        ))}
        {surfaces.map((s) => <A2uiSurface key={s.id} surface={s} />)}
      </div>
      <div className="flex gap-2">
        <input className="flex-1 rounded border p-2" value={input} onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && submit()} placeholder={running ? 'running…' : 'message'} disabled={running} />
        <button className="rounded bg-black px-4 py-2 text-white disabled:opacity-50" onClick={submit} disabled={running}>Send</button>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Build**

Run: `cd /root/agentserver/browserweb && pnpm build`
Expected: clean tsc + vite build; `browserweb/dist/` populated.

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver
git add browserweb/src/App.tsx
git commit -m "feat(browser-gateway): chat UI + A2UI surface rendering"
```

---

### Task 5: Embed the SPA + serve it from the gateway

**Files:**
- Create: `browserweb/embed.go`
- Modify: `internal/browsergateway/server.go`
- Test: `internal/browsergateway/server_test.go`

**Interfaces:**
- Consumes: P1 `Server.Handler()`.
- Produces: `browserweb.StaticFS embed.FS`; a static/SPA route on the gateway (`GET /` + non-API paths → embedded `dist`, SPA fallback to `index.html`).

- [ ] **Step 1: embed.go + a committed dist placeholder**

`browserweb/embed.go`:
```go
// Package browserweb embeds the built browser-gateway reference SPA.
package browserweb

import "embed"

//go:embed all:dist
var StaticFS embed.FS
```
`go:embed` fails to compile if `dist/` is absent. Since `dist` is gitignored (Task 1) and built in CI/Docker, commit a tiny placeholder so local `go build` works and tests have something to serve:
`browserweb/dist/index.html`:
```html
<!doctype html><title>browser-gateway</title><div id="root"></div>
```
Force-add it past .gitignore in Step 5.

- [ ] **Step 2: Write the failing serve test**

Add to `internal/browsergateway/server_test.go`:
```go
func TestServer_ServesSPA(t *testing.T) {
	s := NewServer(ServeConfig{CodexAppGatewayWSURL: "ws://unused", AllowedOrigins: []string{"*"}}, slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<div id=\"root\">") {
		t.Errorf("GET / did not serve the SPA index; body=%q", rec.Body.String())
	}
}

func TestServer_SPAFallback(t *testing.T) {
	s := NewServer(ServeConfig{CodexAppGatewayWSURL: "ws://unused", AllowedOrigins: []string{"*"}}, slog.Default())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/some/client/route", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("client route = %d, want 200 (SPA fallback)", rec.Code)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /root/agentserver && go test ./internal/browsergateway/ -run 'TestServer_ServesSPA|TestServer_SPAFallback' -v`
Expected: FAIL (no route for `/`; 404).

- [ ] **Step 4: Add static serving to server.go**

In `internal/browsergateway/server.go`: import `io/fs`, `net/http`, and `github.com/agentserver/agentserver/browserweb`. In `Handler()`, after registering `/healthz` and `POST /agui`, add a catch-all `GET /` that serves the embedded SPA with fallback:
```go
	mux.Handle("GET /", spaHandler())
```
and add:
```go
// spaHandler serves the embedded browserweb SPA, falling back to index.html
// for client-side routes (any path that isn't a real embedded file).
func spaHandler() http.Handler {
	sub, err := fs.Sub(browserweb.StaticFS, "dist")
	if err != nil {
		panic("browserweb dist embed: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err != nil && r.URL.Path != "/" {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
```
(Go 1.22 mux: the `GET /` pattern is the lowest-precedence catch-all, so `GET /healthz` and `POST /agui` still win for their exact paths.)

- [ ] **Step 5: Run tests + commit**

Run: `cd /root/agentserver && go test ./internal/browsergateway/... -count=1`
Expected: PASS (including the two new SPA tests and all prior tests).
```bash
cd /root/agentserver && gofmt -w internal/browsergateway/server.go
git add browserweb/embed.go internal/browsergateway/server.go internal/browsergateway/server_test.go
git add -f browserweb/dist/index.html
git commit -m "feat(browser-gateway): embed + serve the browserweb SPA (SPA fallback)"
```

---

### Task 6: Dockerfile — frontend build stage + embed

**Files:**
- Modify: `Dockerfile.browser-gateway`

**Interfaces:**
- Produces: an image whose Go build embeds the freshly-built `browserweb/dist`.

- [ ] **Step 1: Rewrite Dockerfile.browser-gateway with a frontend stage**

`Dockerfile.browser-gateway`:
```dockerfile
# syntax=docker/dockerfile:1
# Stage 1: build the browserweb SPA
FROM node:25-slim AS frontend
RUN npm install -g pnpm
WORKDIR /app/browserweb
COPY browserweb/package.json browserweb/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY browserweb/ ./
RUN pnpm build

# Stage 2: build the Go binary (embeds browserweb/dist)
FROM golang:1.26-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/browserweb/dist ./browserweb/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' \
    -o /out/browser-gateway ./cmd/browser-gateway

FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/browser-gateway /usr/local/bin/browser-gateway
ENTRYPOINT ["/usr/local/bin/browser-gateway"]
CMD ["serve"]
EXPOSE 8088
```

- [ ] **Step 2: Verify the image builds (if docker available)**

Run: `cd /root/agentserver && docker build -f Dockerfile.browser-gateway -t browser-gateway:dev .`
Expected: frontend stage installs + builds the SPA; Go stage embeds the real dist; image builds.
If docker is unavailable here, SKIP and note it; the load-bearing local checks are `cd browserweb && pnpm build` (Task 4) and `go build ./...` (already green).

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver
git add Dockerfile.browser-gateway
git commit -m "feat(browser-gateway): Dockerfile frontend build stage + embed"
```

---

### Task 7: CI — `build-browser-gateway` job

**Files:**
- Modify: `.github/workflows/build.yml`

**Interfaces:**
- Produces: a CI job that builds+pushes `ghcr.io/agentserver/browser-gateway` from `Dockerfile.browser-gateway`.

- [ ] **Step 1: Add the job**

In `.github/workflows/build.yml`, add a job mirroring `build-credentialproxy` (after the existing gateway jobs, e.g. after `build-cc-app-gateway`):
```yaml
  build-browser-gateway:
    runs-on: ubuntu-latest
    needs: [test]
    steps:
      - uses: actions/checkout@v6

      - uses: docker/setup-buildx-action@v4

      - uses: docker/login-action@v4
        with:
          registry: ${{ env.REGISTRY }}
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - uses: docker/metadata-action@v6
        id: meta
        with:
          images: ${{ env.REGISTRY }}/agentserver/browser-gateway
          tags: |
            type=sha,prefix=
            type=ref,event=branch
            type=semver,pattern={{version}}
            type=semver,pattern={{major}}.{{minor}}
            type=raw,value=latest,enable={{is_default_branch}}

      - uses: docker/build-push-action@v7
        with:
          context: .
          file: ./Dockerfile.browser-gateway
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
```

- [ ] **Step 2: Validate the workflow YAML**

Run: `cd /root/agentserver && python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/build.yml')); print('yaml ok')"`
Expected: `yaml ok` (no parse error). Confirm the new job's indentation matches the sibling jobs (2-space job key under `jobs:`).

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver
git add .github/workflows/build.yml
git commit -m "ci(browser-gateway): build+push image job"
```

---

### Task 8: Helm — deployment, service, values, route

**Files:**
- Create: `deploy/helm/agentserver/templates/browser-gateway.yaml`
- Modify: `deploy/helm/agentserver/values.yaml`
- Modify: `deploy/helm/agentserver/templates/httproute.yaml` (add the route if the chart uses HTTPRoute; else `ingress.yaml`)

**Interfaces:**
- Produces: an opt-in (`browserGateway.enabled`) Deployment+Service wired to the in-cluster codex-app-gateway, plus a public route.

- [ ] **Step 1: values.yaml block**

Add to `deploy/helm/agentserver/values.yaml` (near `codexAppGateway:`):
```yaml
browserGateway:
  enabled: false
  image:
    repository: ghcr.io/agentserver/browser-gateway
    tag: latest
    pullPolicy: Always
  replicaCount: 1
  port: 8088
  logLevel: info
  # Comma-separated CORS allowlist; "*" for dev.
  allowedOrigins: "*"
```

- [ ] **Step 2: template**

`deploy/helm/agentserver/templates/browser-gateway.yaml`:
```yaml
{{- if .Values.browserGateway.enabled }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}-browser-gateway
  labels:
    app: {{ .Release.Name }}-browser-gateway
spec:
  replicas: {{ .Values.browserGateway.replicaCount }}
  selector:
    matchLabels:
      app: {{ .Release.Name }}-browser-gateway
  template:
    metadata:
      labels:
        app: {{ .Release.Name }}-browser-gateway
    spec:
      serviceAccountName: {{ .Release.Name }}
      containers:
        - name: browser-gateway
          image: "{{ .Values.browserGateway.image.repository }}:{{ .Values.browserGateway.image.tag }}"
          imagePullPolicy: {{ .Values.browserGateway.image.pullPolicy }}
          args: ["serve"]
          ports:
            - containerPort: {{ .Values.browserGateway.port }}
          env:
            - name: BRG_LISTEN_ADDR
              value: ":{{ .Values.browserGateway.port }}"
            - name: BRG_CODEX_APP_GATEWAY_WS_URL
              value: "ws://{{ .Release.Name }}-codex-app-gateway.{{ .Release.Namespace }}.svc:{{ .Values.codexAppGateway.port }}"
            - name: BRG_ALLOWED_ORIGINS
              value: {{ .Values.browserGateway.allowedOrigins | quote }}
            - name: BRG_LOG_LEVEL
              value: {{ .Values.browserGateway.logLevel | quote }}
          readinessProbe:
            httpGet:
              path: /healthz
              port: {{ .Values.browserGateway.port }}
            initialDelaySeconds: 3
            periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: {{ .Release.Name }}-browser-gateway
  labels:
    app: {{ .Release.Name }}-browser-gateway
spec:
  selector:
    app: {{ .Release.Name }}-browser-gateway
  ports:
    - port: {{ .Values.browserGateway.port }}
      targetPort: {{ .Values.browserGateway.port }}
{{- end }}
```

- [ ] **Step 3: Lint the chart**

Run:
```bash
cd /root/agentserver && helm lint deploy/helm/agentserver --set browserGateway.enabled=true 2>&1 | tail -20 || echo "(helm not installed — skip; template is valid YAML by inspection)"
helm template t deploy/helm/agentserver --set browserGateway.enabled=true 2>&1 | grep -A2 "kind: Deployment" | grep browser-gateway && echo "template renders browser-gateway"
```
Expected: lint passes (or helm absent → skip); `helm template` renders the browser-gateway Deployment. If `helm` is unavailable, validate the template file is well-formed YAML with the same `python3 -c "import yaml..."` check applied to a `helm template`-free copy (the `{{ }}` makes raw YAML-parse fail — in that case rely on `helm template` if available, else inspection).

- [ ] **Step 4: (route) add the public host**

If `deploy/helm/agentserver/templates/httproute.yaml` defines per-service routes gated on `.enabled`, add a `browserGateway`-gated backendRef/rule pointing at the `{{ .Release.Name }}-browser-gateway` Service on `.Values.browserGateway.port`, mirroring how `codexAppGateway`/the main service are routed there. Read that file first and follow its existing structure exactly (hostnames, gateway ref). If routing is done via `ingress.yaml` instead, add the equivalent path/host there. Keep it gated on `browserGateway.enabled`.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add deploy/helm/agentserver/templates/browser-gateway.yaml deploy/helm/agentserver/values.yaml deploy/helm/agentserver/templates/httproute.yaml
git commit -m "feat(browser-gateway): Helm deployment, service, values, route"
```

---

## Self-Review

**1. Spec coverage (P3 scope):**
- CopilotKit reference frontend → **superseded by user decision**: pure `@ag-ui/client` + `@a2ui/react` SPA (Tasks 1–4). The design's "CopilotKit" is explicitly replaced (CopilotKit needs a Node runtime, incompatible with single-binary); recorded in the plan header + Global Constraints.
- Consumes `a2ui.operations` CUSTOM via `@a2ui/react` → Task 3 (`onCustomEvent` → `processor.processMessages`) + Task 4 (`<A2uiSurface>`). ✓
- Embed + serve in the single Go binary → Task 5. ✓
- Dockerfile frontend stage → Task 6; CI `build-browser-gateway` → Task 7; Helm → Task 8. ✓
- Auth handoff (workspace codex token) → Task 4 (`?token=` / paste), sent as Bearer by Task 3. ✓
- Out of scope (documented): HITL/approvals, A2UI interactive callbacks (the `MessageProcessor` `actionHandler` is noted in recon but not wired — a follow-up), live `commandExecution/outputDelta` streaming, Playwright E2E.

**2. Placeholder scan:** No TBD/TODO. Every code step has complete content. Two honest latitude points, both bounded: Task 3 Step 2 and Task 8 Step 4 tell the implementer to reconcile against the installed `.d.ts` / the existing route file — these are real "match the existing/installed artifact" instructions, not vague hand-waves, and each names the exact file and the exact fix criterion.

**3. Type/interface consistency:**
- `ChatState`/`ChatMessage`/`ToolCall`/`reduceEvent`/`addUserMessage`/`emptyChatState` — defined Task 2, consumed Task 3. ✓
- `useAgent(token) → { chat, surfaces, send, running, error }` — defined Task 3, consumed Task 4. ✓
- `browserweb.StaticFS` — defined Task 5 embed.go, consumed Task 5 server.go. ✓
- Package versions/import subpaths (`@a2ui/react/v0_9`, `@a2ui/web_core/v0_9`, `@ag-ui/client`) are consistent across Tasks 1/3 and match the verified npm versions. ✓
- Helm env `BRG_CODEX_APP_GATEWAY_WS_URL` matches the P1 config key; `BRG_ALLOWED_ORIGINS`/`BRG_LOG_LEVEL`/`BRG_LISTEN_ADDR` match P1's `LoadServeConfigFromEnv`. ✓

Fixed inline: none needed.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-20-browser-gateway-frontend-p3.md`. Two execution options:

**1. Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, on a branch stacked off `browser-gateway-p2` (no worktree).

**2. Inline Execution** — execute in this session with checkpoints.

This completes the browser-gateway feature (P1 endpoint → P2 tools+A2UI → P3 reference UI + deploy). After P3, remaining follow-ups (separate, smaller): HITL/approvals, A2UI interactive callbacks, live output-delta streaming.
