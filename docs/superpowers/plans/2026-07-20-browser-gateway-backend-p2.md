# browser-gateway backend (P2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend browser-gateway (P1 text-streaming MVP) to translate codex tool activity — `commandExecution` and `fileChange` items — into AG-UI tool-call events **plus gateway-synthesized A2UI cards** (carried in AG-UI `CUSTOM` events), fix the P1 `reasoning` mapping against the real codex schema, and pin the deployed codex to the version this maps against.

**Architecture:** All work is in the existing `internal/browsergateway` Go packages plus one new `internal/browsergateway/a2ui` package. The mapper gains cases for the tool item types; a pure `a2ui` package hand-builds A2UI v0.9 message arrays for a command-result card and a file-diff card; the run loop is unchanged (it already streams whatever events the mapper returns). No changes to the AG-UI or codex wire clients beyond a codex-version bump + a request-shape compat check.

**Tech Stack:** Go 1.26; AG-UI Go SDK (`events.NewCustomEvent`, `events.WithValue`, `NewToolCall*Event`); codex app-server v2 (target tag **rust-v0.144.6**); A2UI **v0.9** JSON (hand-rolled, no Go producer SDK exists).

**Builds on:** `docs/superpowers/plans/2026-07-20-browser-gateway-backend-p1.md` (branch `browser-gateway-p1`, PR #292). This plan is a **stacked branch off `browser-gateway-p1`**. Spec: `docs/superpowers/specs/2026-07-20-browser-gateway-design.md`.

## Global Constraints

- Repo module `github.com/agentserver/agentserver`, Go 1.26. Branch: stack on `browser-gateway-p1` (base commit `f86eb34`). Subagents run in `/root/agentserver` (do NOT use a git worktree — subagent cwd pins to the repo root).
- **codex app-server v2 wire facts (tag rust-v0.144.6, authoritative — read from `/root/codex` at that tag).** All structs are `#[serde(rename_all="camelCase")]`; item enum is internally tagged `{"type": "...", ...}`. Absent Option fields serialize as explicit `null` (keys always present).
  - **`agentMessage`** item: `{type:"agentMessage", id, text, phase:"commentary"|"final_answer"|null, memoryCitation?}` — text is the single joined string field `text`. (P1 already maps this correctly.)
  - **`reasoning`** item: `{type:"reasoning", id, summary:string[], content:string[]}` — **NO `text` field** (P1 bug: it read `text`). Both arrays always present (may be empty).
  - **`commandExecution`** item: `{type:"commandExecution", id, command:string, cwd:string, processId:string|null, source:string, status:"inProgress"|"completed"|"failed"|"declined", commandActions:[...], aggregatedOutput:string|null, exitCode:number|null, durationMs:number|null}`.
  - **`fileChange`** item: `{type:"fileChange", id, changes:[{path:string, kind:{type:"add"|"delete"|"update", movePath?:string|null}, diff:string}], status:"inProgress"|"completed"|"failed"|"declined"}`.
  - Notification methods (slash form; no `item/updated` exists): `item/started {item, threadId, turnId, startedAtMs}`, `item/completed {item, threadId, turnId, completedAtMs}`, `item/agentMessage/delta {threadId, turnId, itemId, delta}`, `item/commandExecution/outputDelta {threadId, turnId, itemId, delta}`, `turn/completed {threadId, turn}`, top-level `error {error:TurnError, willRetry, threadId, turnId}`.
  - `turn.status` ∈ `completed|interrupted|failed|inProgress`; `turn.error = {message, codexErrorInfo(string OR single-key object)|null, additionalDetails|null}`.
  - `thread/start` result: id is at `result.thread.id` (extra fields ignored). `turn/start` params: `{threadId, input:[<UserInput items>]}` — Task 1 confirms the exact `input` item shape at 0.144.6.
- **A2UI v0.9 wire facts** (target v0.9 to match shipping renderers/CopilotKit). A payload is an **ordered array of message objects**, each `{version:"v0.9", <oneKey>:{...}}` with exactly one of `createSurface|updateComponents|updateDataModel|deleteSurface`. Component model is a flat adjacency list; exactly one `id:"root"`; single-child containers (`Card`) use `child` (a component-id string), multi-child (`Column`,`List`) use `children` (array of ids). Bindable props are `literal | {"path":"/ptr"} | {"call":..,"args":..}`. Basic catalog `catalogId` = `https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json`; component types used here: `Card`, `Column`, `Text`.
- **A2UI carrier over AG-UI** (framework-neutral, per spec §7.2): an AG-UI `CUSTOM` event `{name:"a2ui.operations", value:<array of A2UI messages>}`. Emit it AFTER the item's tool-call events.
- **codex version bump**: `Dockerfile.codex-app-gateway` currently pins `CODEX_VERSION=0.137.0`; P2 bumps it to `0.144.6` so the deployed codex emits the shapes this maps against. This is the one cross-component change and is intentional.
- TDD: failing test → run/fail → implement → run/pass → commit. `gofmt -w` touched files; `go vet` before committing. Run focused tests while iterating; full package + `-race` once before committing.
- Reuse P1 conventions: mapper is pure (`Map(codexclient.Frame) Result`); "unknown item type/frame → log warning + skip"; empty text → no event (SDK rejects empty deltas).

---

### Task 1: Pin codex 0.144.6 + confirm request-shape compatibility

**Files:**
- Modify: `Dockerfile.codex-app-gateway` (the `ARG CODEX_VERSION=` line)
- Modify (only if the shape changed): `internal/browsergateway/codexclient/protocol.go`, `client.go`
- Reference (read-only): `/root/codex` at tag `rust-v0.144.6`, file `codex-rs/app-server-protocol/src/protocol/v2/turn.rs` and `v2/thread.rs`

**Interfaces:**
- Produces: a documented confirmation that P1's `turn/start` params (`{threadId, input:[{type:"text", text}]}`), `thread/start` (`{}` → `result.thread.id`), and `thread/resume` (`{threadId}`) are accepted by codex 0.144.6; codexclient adjusted iff not.

- [ ] **Step 1: Read the 0.144.6 turn/start request shape**

Run:
```bash
cd /root/codex && git rev-parse --abbrev-ref HEAD; git describe --tags
sed -n '1,120p' codex-rs/app-server-protocol/src/protocol/v2/turn.rs | grep -nA30 "TurnStartParams"
grep -rn "pub struct TurnStartParams" codex-rs/app-server-protocol/src/protocol/v2/turn.rs
grep -rn "enum UserInput\|struct.*Input\b" codex-rs/app-server-protocol/src/protocol/v2/*.rs codex-rs/app-server-protocol/src/protocol/common.rs | head
```
Expected: `TurnStartParams` has a `threadId` field and an `input` field (a `Vec<UserInput>` or similar). Read the `UserInput` (or input item) enum: confirm a text variant serializes as `{"type":"text","text":"..."}`. Record the exact shape in the report.

- [ ] **Step 2: Decide — compatible or adjust**

If the text input variant is `{"type":"text","text":...}` (unchanged from P1): no codexclient change needed. If the shape differs (e.g. a wrapper key, a renamed field, a required field), update `internal/browsergateway/codexclient/protocol.go` `turnStartParams`/`turnInputItem` to match, and adjust `client_test.go`'s fake if needed. Document exactly what you found and did.

- [ ] **Step 3: Bump the codex version**

In `Dockerfile.codex-app-gateway`, change the codex version arg to `0.144.6`:
```dockerfile
ARG CODEX_VERSION=0.144.6
```
(Leave everything else in that Dockerfile unchanged.)

- [ ] **Step 4: Verify P1 still builds + tests pass**

Run: `cd /root/agentserver && go build ./... && go test ./internal/browsergateway/... ./cmd/browser-gateway/... -count=1`
Expected: build exit 0; all packages `ok`. (These tests use a fake ws, so they validate our client's own request encoding, which is what Step 2 may have changed.)

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add Dockerfile.codex-app-gateway internal/browsergateway/codexclient/ 2>/dev/null; git add Dockerfile.codex-app-gateway
git commit -m "chore(browser-gateway): pin codex 0.144.6 + confirm turn/start request compat"
```
(If codexclient was unchanged, only `Dockerfile.codex-app-gateway` is staged.)

---

### Task 2: Fix `reasoning` mapping against the real schema

**Files:**
- Modify: `internal/browsergateway/mapper/mapper.go`
- Modify: `internal/browsergateway/mapper/testdata/reasoning.json`
- Modify: `internal/browsergateway/mapper/mapper_test.go`

**Interfaces:**
- Consumes: `codexItem` struct in mapper.go (P1). Produces: corrected reasoning handling reading `summary[]`/`content[]`.

- [ ] **Step 1: Replace the fixture with the real reasoning shape**

Overwrite `internal/browsergateway/mapper/testdata/reasoning.json`:
```json
{"method":"item/completed","params":{"item":{"type":"reasoning","id":"rsn-1","summary":["Considering options"],"content":["The user asked for X, so I will Y."]},"threadId":"thr-1","turnId":"trn-1"}}
```

- [ ] **Step 2: Update the failing test to assert real behavior**

In `internal/browsergateway/mapper/mapper_test.go`, ensure `TestMap_Reasoning` still expects a reasoning START/CONTENT/END sequence and additionally assert the content text is the joined summary+content. Replace the existing `TestMap_Reasoning` with:
```go
func TestMap_Reasoning(t *testing.T) {
	r := Map(loadFrame(t, "reasoning.json"))
	got := typesOf(r.Events)
	if len(got) != 3 || got[0] != events.EventTypeReasoningMessageStart || got[1] != events.EventTypeReasoningMessageContent || got[2] != events.EventTypeReasoningMessageEnd {
		t.Fatalf("event types = %v, want reasoning start/content/end", got)
	}
	// The content event must carry joined summary+content, not empty.
	ce, ok := r.Events[1].(*events.ReasoningMessageContentEvent)
	if !ok {
		t.Fatalf("event[1] is %T, want *ReasoningMessageContentEvent", r.Events[1])
	}
	if ce.Delta == "" {
		t.Fatal("reasoning content delta is empty — summary/content not read")
	}
}
```
(If the P1 test helper is named `types` not `typesOf`, keep the existing name — match the file. The struct field for the content delta is `Delta`; confirm via the SDK `reasoning_events.go` if the compiler complains.)

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /root/agentserver && go test ./internal/browsergateway/mapper/ -run TestMap_Reasoning -v`
Expected: FAIL — the P1 code reads `it.Text` (now empty) so it returns no events / empty delta.

- [ ] **Step 4: Fix the reasoning branch**

In `internal/browsergateway/mapper/mapper.go`, add `Summary []string` and `Content []string` to the `codexItem` struct, and rewrite the `reasoning` case of `mapItem` to join them:
```go
// in codexItem struct, add:
	Summary []string `json:"summary"`
	Content []string `json:"content"`
```
```go
	case "reasoning":
		text := strings.TrimSpace(strings.Join(append(append([]string{}, it.Summary...), it.Content...), "\n"))
		if text == "" {
			return Result{}
		}
		return Result{Events: []events.Event{
			events.NewReasoningMessageStartEvent(it.ID, "assistant"),
			events.NewReasoningMessageContentEvent(it.ID, text),
			events.NewReasoningMessageEndEvent(it.ID),
		}}
```
Add `"strings"` to the imports if not already present.

- [ ] **Step 5: Run test to verify it passes + full mapper suite**

Run: `cd /root/agentserver && go test ./internal/browsergateway/mapper/ -v`
Expected: PASS (all mapper tests, including the unchanged `TestMap_AgentMessage`/`TestMap_TurnCompleted`/`TestMap_TurnFailed`).

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver && gofmt -w internal/browsergateway/mapper/
git add internal/browsergateway/mapper/
git commit -m "fix(browser-gateway): map codex reasoning summary/content (real 0.144.6 shape)"
```

---

### Task 3: `a2ui` package — v0.9 structs + card builders

**Files:**
- Create: `internal/browsergateway/a2ui/a2ui.go`
- Create: `internal/browsergateway/a2ui/cards.go`
- Test: `internal/browsergateway/a2ui/cards_test.go`

**Interfaces:**
- Produces:
  - `type Message struct` and component types serializing to A2UI v0.9 JSON.
  - `func CommandCard(id, command, output, statusLine string) []Message`
  - `func FileDiffCard(id string, files []FileChange) []Message` where `type FileChange struct { Path, Kind, Diff string }`
  - `const CatalogID = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"`

- [ ] **Step 1: Write the failing golden test**

`internal/browsergateway/a2ui/cards_test.go`:
```go
package a2ui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommandCard_Shape(t *testing.T) {
	msgs := CommandCard("cmd-1", "ls -la", "total 0", "exit 0")
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages (createSurface, updateComponents, updateDataModel), got %d", len(msgs))
	}
	if msgs[0].CreateSurface == nil || msgs[0].CreateSurface.SurfaceID != "cmd-cmd-1" {
		t.Fatalf("msg[0] not a createSurface for surface cmd-cmd-1: %+v", msgs[0])
	}
	if msgs[0].CreateSurface.CatalogID != CatalogID {
		t.Errorf("catalogId = %q", msgs[0].CreateSurface.CatalogID)
	}
	if msgs[1].UpdateComponents == nil {
		t.Fatal("msg[1] not updateComponents")
	}
	// exactly one root component
	roots := 0
	for _, c := range msgs[1].UpdateComponents.Components {
		if c.ID == "root" {
			roots++
			if c.Component != "Card" || c.Child == "" {
				t.Errorf("root should be a Card with a child, got %+v", c)
			}
		}
	}
	if roots != 1 {
		t.Fatalf("want exactly 1 root component, got %d", roots)
	}
	if msgs[2].UpdateDataModel == nil {
		t.Fatal("msg[2] not updateDataModel")
	}
	// every message serializes with version v0.9 and exactly one payload key
	for i, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("msg[%d] marshal: %v", i, err)
		}
		s := string(b)
		if !strings.Contains(s, `"version":"v0.9"`) {
			t.Errorf("msg[%d] missing version v0.9: %s", i, s)
		}
	}
}

func TestFileDiffCard_Shape(t *testing.T) {
	msgs := FileDiffCard("fc-1", []FileChange{{Path: "a.go", Kind: "update", Diff: "@@ -1 +1 @@"}})
	if len(msgs) != 3 || msgs[0].CreateSurface == nil || msgs[0].CreateSurface.SurfaceID != "file-fc-1" {
		t.Fatalf("file diff card surface wrong: %+v", msgs[0])
	}
	if msgs[1].UpdateComponents == nil {
		t.Fatal("msg[1] not updateComponents")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/agentserver && go test ./internal/browsergateway/a2ui/ -v`
Expected: FAIL (compile error: `Message`/`CommandCard`/`FileDiffCard`/`CatalogID` undefined).

- [ ] **Step 3: Write the A2UI v0.9 types**

`internal/browsergateway/a2ui/a2ui.go`:
```go
// Package a2ui hand-builds A2UI v0.9 generative-UI payloads
// (https://github.com/a2ui-project/a2ui) for gateway-synthesized cards.
// There is no Go producer SDK; these structs mirror the v0.9 JSON Schema.
// Payloads are delivered over AG-UI as a CUSTOM event {name:"a2ui.operations",
// value:[]Message}. Component model: flat adjacency list, one id:"root";
// single-child containers use "child" (a component id), multi-child use
// "children" (ids).
package a2ui

const (
	// Version is the A2UI wire version this package emits.
	Version = "v0.9"
	// CatalogID is the basic component catalog for v0.9.
	CatalogID = "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"
)

// Message is one A2UI server->client message: exactly one payload key set.
type Message struct {
	Version          string            `json:"version"`
	CreateSurface    *CreateSurface    `json:"createSurface,omitempty"`
	UpdateComponents *UpdateComponents `json:"updateComponents,omitempty"`
	UpdateDataModel  *UpdateDataModel  `json:"updateDataModel,omitempty"`
}

type CreateSurface struct {
	SurfaceID     string `json:"surfaceId"`
	CatalogID     string `json:"catalogId"`
	SendDataModel bool   `json:"sendDataModel,omitempty"`
}

type UpdateComponents struct {
	SurfaceID  string      `json:"surfaceId"`
	Components []Component `json:"components"`
}

type UpdateDataModel struct {
	SurfaceID string `json:"surfaceId"`
	Value     any    `json:"value,omitempty"`
}

// Component is one node in the flat adjacency list. Only the fields used by
// this package's cards are modeled; A2UI ignores unknown props on render.
type Component struct {
	ID        string   `json:"id"`
	Component string   `json:"component"`      // "Card" | "Column" | "Text"
	Child     string   `json:"child,omitempty"`    // single-child containers (Card)
	Children  []string `json:"children,omitempty"` // multi-child containers (Column)
	Text      any      `json:"text,omitempty"`     // literal string OR Binding
}

// Binding is a data-model reference: {"path":"/ptr"} (RFC 6901 JSON Pointer).
type Binding struct {
	Path string `json:"path"`
}

// bind is a small helper for a data-model binding.
func bind(path string) Binding { return Binding{Path: path} }
```

- [ ] **Step 4: Write the card builders**

`internal/browsergateway/a2ui/cards.go`:
```go
package a2ui

import "fmt"

// CommandCard builds an A2UI card showing a shell command, its output, and a
// status line, bound to a per-item surface. id is the codex item id.
func CommandCard(id, command, output, statusLine string) []Message {
	surface := "cmd-" + id
	return []Message{
		{Version: Version, CreateSurface: &CreateSurface{SurfaceID: surface, CatalogID: CatalogID, SendDataModel: true}},
		{Version: Version, UpdateComponents: &UpdateComponents{SurfaceID: surface, Components: []Component{
			{ID: "root", Component: "Card", Child: "col"},
			{ID: "col", Component: "Column", Children: []string{"cmd", "out", "status"}},
			{ID: "cmd", Component: "Text", Text: bind("/command")},
			{ID: "out", Component: "Text", Text: bind("/output")},
			{ID: "status", Component: "Text", Text: bind("/status")},
		}}},
		{Version: Version, UpdateDataModel: &UpdateDataModel{SurfaceID: surface, Value: map[string]string{
			"command": command,
			"output":  output,
			"status":  statusLine,
		}}},
	}
}

// FileChange is one changed file for FileDiffCard.
type FileChange struct {
	Path string
	Kind string // "add" | "delete" | "update"
	Diff string
}

// FileDiffCard builds an A2UI card summarizing file changes: a header plus one
// Text node per file carrying "<kind> <path>" and the diff. id is the item id.
func FileDiffCard(id string, files []FileChange) []Message {
	surface := "file-" + id
	comps := []Component{
		{ID: "root", Component: "Card", Child: "col"},
	}
	children := []string{"header"}
	comps = append(comps, Component{ID: "header", Component: "Text", Text: bind("/header")})
	data := map[string]string{"header": fmt.Sprintf("%d file(s) changed", len(files))}
	for i, f := range files {
		pathID := fmt.Sprintf("path%d", i)
		diffID := fmt.Sprintf("diff%d", i)
		children = append(children, pathID, diffID)
		comps = append(comps,
			Component{ID: pathID, Component: "Text", Text: bind("/" + pathID)},
			Component{ID: diffID, Component: "Text", Text: bind("/" + diffID)},
		)
		data[pathID] = fmt.Sprintf("%s %s", f.Kind, f.Path)
		data[diffID] = f.Diff
	}
	comps = append([]Component{comps[0]}, append([]Component{{ID: "col", Component: "Column", Children: children}}, comps[1:]...)...)
	return []Message{
		{Version: Version, CreateSurface: &CreateSurface{SurfaceID: surface, CatalogID: CatalogID, SendDataModel: true}},
		{Version: Version, UpdateComponents: &UpdateComponents{SurfaceID: surface, Components: comps}},
		{Version: Version, UpdateDataModel: &UpdateDataModel{SurfaceID: surface, Value: data}},
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /root/agentserver && go test ./internal/browsergateway/a2ui/ -v`
Expected: PASS (both tests). If `TestFileDiffCard_Shape` fails on component ordering, confirm the `root` Card is components[0] and a `col` Column exists with the file children — adjust the slice assembly so `root`(Card,child=col) and `col`(Column) are both present.

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver && gofmt -w internal/browsergateway/a2ui/
git add internal/browsergateway/a2ui/
git commit -m "feat(browser-gateway): a2ui v0.9 package (command + file-diff cards)"
```

---

### Task 4: Map `commandExecution` → tool events + A2UI card

**Files:**
- Modify: `internal/browsergateway/mapper/mapper.go`
- Create: `internal/browsergateway/mapper/testdata/command_execution.json`
- Modify: `internal/browsergateway/mapper/mapper_test.go`

**Interfaces:**
- Consumes: `a2ui.CommandCard`, AG-UI `NewToolCallStartEvent/ArgsEvent/EndEvent/ResultEvent`, `NewCustomEvent`+`WithValue`.
- Produces: `commandExecution` case in `mapItem` emitting `TOOL_CALL_START → TOOL_CALL_ARGS → TOOL_CALL_END → TOOL_CALL_RESULT → CUSTOM(a2ui.operations)`.

- [ ] **Step 1: Add the fixture**

`internal/browsergateway/mapper/testdata/command_execution.json`:
```json
{"method":"item/completed","params":{"item":{"type":"commandExecution","id":"cmd-1","command":"ls -la","cwd":"/w","processId":null,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":"total 0","exitCode":0,"durationMs":12},"threadId":"thr-1","turnId":"trn-1"}}
```

- [ ] **Step 2: Write the failing test**

Add to `internal/browsergateway/mapper/mapper_test.go`:
```go
func TestMap_CommandExecution(t *testing.T) {
	r := Map(loadFrame(t, "command_execution.json"))
	got := typesOf(r.Events)
	want := []events.EventType{
		events.EventTypeToolCallStart,
		events.EventTypeToolCallArgs,
		events.EventTypeToolCallEnd,
		events.EventTypeToolCallResult,
		events.EventTypeCustom,
	}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %v, want %v (full: %v)", i, got[i], want[i], got)
		}
	}
	last, ok := r.Events[len(r.Events)-1].(*events.CustomEvent)
	if !ok || last.Name != "a2ui.operations" {
		t.Fatalf("last event not CUSTOM a2ui.operations: %+v", r.Events[len(r.Events)-1])
	}
}
```
(Match the P1 helper name — if P1 named it `types`, use `types`; this plan assumes `typesOf`. If needed, add `func typesOf(evs []events.Event) []events.EventType` mirroring P1's helper.)

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /root/agentserver && go test ./internal/browsergateway/mapper/ -run TestMap_CommandExecution -v`
Expected: FAIL — `commandExecution` falls through to the default (unmapped) branch, producing no events.

- [ ] **Step 4: Implement the case**

In `internal/browsergateway/mapper/mapper.go`: extend `codexItem` with the command fields, add the `a2ui` import, and add the case.
```go
// add to codexItem:
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregatedOutput"`
	ExitCode         *int   `json:"exitCode"`
	Status           string `json:"status"`
```
```go
	case "commandExecution":
		statusLine := it.Status
		if it.ExitCode != nil {
			statusLine = fmt.Sprintf("%s (exit %d)", it.Status, *it.ExitCode)
		}
		card := a2ui.CommandCard(it.ID, it.Command, it.AggregatedOutput, statusLine)
		return Result{Events: []events.Event{
			events.NewToolCallStartEvent(it.ID, "shell"),
			events.NewToolCallArgsEvent(it.ID, it.Command),
			events.NewToolCallEndEvent(it.ID),
			events.NewToolCallResultEvent(it.ID, it.ID, it.AggregatedOutput),
			events.NewCustomEvent("a2ui.operations", events.WithValue(card)),
		}}
```
Add imports `"fmt"` and `"github.com/agentserver/agentserver/internal/browsergateway/a2ui"`.
(Note: `NewToolCallResultEvent(messageID, toolCallID, content)` — reuse `it.ID` for both; the SDK sets role="tool" internally.)

- [ ] **Step 5: Run test to verify it passes + full mapper suite**

Run: `cd /root/agentserver && go test ./internal/browsergateway/mapper/ -v`
Expected: PASS (all mapper tests).

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver && gofmt -w internal/browsergateway/mapper/
git add internal/browsergateway/mapper/
git commit -m "feat(browser-gateway): map commandExecution → tool events + A2UI card"
```

---

### Task 5: Map `fileChange` → tool events + A2UI card

**Files:**
- Modify: `internal/browsergateway/mapper/mapper.go`
- Create: `internal/browsergateway/mapper/testdata/file_change.json`
- Modify: `internal/browsergateway/mapper/mapper_test.go`

**Interfaces:**
- Consumes: `a2ui.FileDiffCard`, `a2ui.FileChange`. Produces: `fileChange` case emitting `TOOL_CALL_START → TOOL_CALL_ARGS → TOOL_CALL_END → TOOL_CALL_RESULT → CUSTOM(a2ui.operations)`.

- [ ] **Step 1: Add the fixture**

`internal/browsergateway/mapper/testdata/file_change.json`:
```json
{"method":"item/completed","params":{"item":{"type":"fileChange","id":"fc-1","status":"completed","changes":[{"path":"main.go","kind":{"type":"update","movePath":null},"diff":"@@ -1 +1 @@\n-old\n+new"}]},"threadId":"thr-1","turnId":"trn-1"}}
```

- [ ] **Step 2: Write the failing test**

Add to `internal/browsergateway/mapper/mapper_test.go`:
```go
func TestMap_FileChange(t *testing.T) {
	r := Map(loadFrame(t, "file_change.json"))
	got := typesOf(r.Events)
	want := []events.EventType{
		events.EventTypeToolCallStart,
		events.EventTypeToolCallArgs,
		events.EventTypeToolCallEnd,
		events.EventTypeToolCallResult,
		events.EventTypeCustom,
	}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	start, ok := r.Events[0].(*events.ToolCallStartEvent)
	if !ok || start.ToolCallName != "apply_patch" {
		t.Fatalf("tool name = %v, want apply_patch", r.Events[0])
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /root/agentserver && go test ./internal/browsergateway/mapper/ -run TestMap_FileChange -v`
Expected: FAIL — `fileChange` unmapped.

- [ ] **Step 4: Implement the case**

In `mapper.go`, add a `Changes` field to `codexItem` and a nested type, then the case:
```go
// add to codexItem:
	Changes []codexFileChange `json:"changes"`
```
```go
type codexFileChange struct {
	Path string `json:"path"`
	Kind struct {
		Type string `json:"type"`
	} `json:"kind"`
	Diff string `json:"diff"`
}
```
```go
	case "fileChange":
		files := make([]a2ui.FileChange, 0, len(it.Changes))
		var argsB strings.Builder
		for _, c := range it.Changes {
			files = append(files, a2ui.FileChange{Path: c.Path, Kind: c.Kind.Type, Diff: c.Diff})
			fmt.Fprintf(&argsB, "%s %s\n", c.Kind.Type, c.Path)
		}
		card := a2ui.FileDiffCard(it.ID, files)
		return Result{Events: []events.Event{
			events.NewToolCallStartEvent(it.ID, "apply_patch"),
			events.NewToolCallArgsEvent(it.ID, strings.TrimSpace(argsB.String())),
			events.NewToolCallEndEvent(it.ID),
			events.NewToolCallResultEvent(it.ID, it.ID, fmt.Sprintf("%d file(s) changed", len(it.Changes))),
			events.NewCustomEvent("a2ui.operations", events.WithValue(card)),
		}}
```
(`strings` and `fmt` are already imported from Tasks 2/4.)

- [ ] **Step 5: Run test to verify it passes + full mapper suite + race**

Run: `cd /root/agentserver && go test ./internal/browsergateway/mapper/ -v && go test ./internal/browsergateway/... -race -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver && gofmt -w internal/browsergateway/mapper/
git add internal/browsergateway/mapper/
git commit -m "feat(browser-gateway): map fileChange → tool events + A2UI diff card"
```

---

### Task 6: Stream agent-message deltas (started → START, delta → CONTENT, completed → END)

**Files:**
- Modify: `internal/browsergateway/mapper/mapper.go`
- Modify: `internal/browsergateway/mapper/mapper_test.go`
- Create: `internal/browsergateway/mapper/testdata/agent_message_started.json`, `internal/browsergateway/mapper/testdata/agent_message_delta.json`
- Modify: `internal/browsergateway/mapper/testdata/agent_message.json` (now the *completed* frame → END only)
- Modify: `internal/browsergateway/integration_test.go` (fake CXG emits the streaming sequence)

**Interfaces:**
- Produces: `item/started`(agentMessage) → `TEXT_MESSAGE_START`; `item/agentMessage/delta` → `TEXT_MESSAGE_CONTENT`; `item/completed`(agentMessage) → `TEXT_MESSAGE_END`. (Replaces P1's "full text on completed".)

- [ ] **Step 1: Add fixtures for the streaming sequence**

`internal/browsergateway/mapper/testdata/agent_message_started.json`:
```json
{"method":"item/started","params":{"item":{"type":"agentMessage","id":"msg-1","text":"","phase":null},"threadId":"thr-1","turnId":"trn-1","startedAtMs":1}}
```
`internal/browsergateway/mapper/testdata/agent_message_delta.json`:
```json
{"method":"item/agentMessage/delta","params":{"threadId":"thr-1","turnId":"trn-1","itemId":"msg-1","delta":"Hello"}}
```
Overwrite `internal/browsergateway/mapper/testdata/agent_message.json` (this is now the *completed* frame):
```json
{"method":"item/completed","params":{"item":{"type":"agentMessage","id":"msg-1","text":"Hello from codex","phase":"final_answer"},"threadId":"thr-1","turnId":"trn-1","completedAtMs":2}}
```

- [ ] **Step 2: Rewrite the agent-message tests**

Replace `TestMap_AgentMessage` in `mapper_test.go` with three focused tests:
```go
func TestMap_AgentMessageStart(t *testing.T) {
	r := Map(loadFrame(t, "agent_message_started.json"))
	got := typesOf(r.Events)
	if len(got) != 1 || got[0] != events.EventTypeTextMessageStart {
		t.Fatalf("start frame → %v, want [TEXT_MESSAGE_START]", got)
	}
}
func TestMap_AgentMessageDelta(t *testing.T) {
	r := Map(loadFrame(t, "agent_message_delta.json"))
	got := typesOf(r.Events)
	if len(got) != 1 || got[0] != events.EventTypeTextMessageContent {
		t.Fatalf("delta frame → %v, want [TEXT_MESSAGE_CONTENT]", got)
	}
	ce := r.Events[0].(*events.TextMessageContentEvent)
	if ce.Delta != "Hello" {
		t.Errorf("delta = %q, want Hello", ce.Delta)
	}
}
func TestMap_AgentMessageCompleted(t *testing.T) {
	r := Map(loadFrame(t, "agent_message.json"))
	got := typesOf(r.Events)
	if len(got) != 1 || got[0] != events.EventTypeTextMessageEnd {
		t.Fatalf("completed frame → %v, want [TEXT_MESSAGE_END]", got)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `cd /root/agentserver && go test ./internal/browsergateway/mapper/ -run TestMap_AgentMessage -v`
Expected: FAIL — P1 emits START+CONTENT+END on completed and ignores started/delta.

- [ ] **Step 4: Implement streaming lifecycle**

In `mapper.go`:
- Add an `item/started` case to `Map` that calls a new `mapItemStarted(it)`.
- Add an `item/agentMessage/delta` case to `Map` that decodes `{itemId, delta}` and returns a `TEXT_MESSAGE_CONTENT` (guard empty delta).
- Change the `agentMessage` case of `mapItem` (the item/completed path) to emit only `TEXT_MESSAGE_END`.

```go
// in Map's switch, before "item/completed":
	case "item/started":
		var p itemParams
		if err := json.Unmarshal(f.Params, &p); err != nil {
			slog.Warn("browser-gateway/mapper: bad item/started params", "err", err)
			return Result{}
		}
		return mapItemStarted(p.Item)
	case "item/agentMessage/delta":
		var d struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if err := json.Unmarshal(f.Params, &d); err != nil || d.Delta == "" {
			return Result{}
		}
		return Result{Events: []events.Event{events.NewTextMessageContentEvent(d.ItemID, d.Delta)}}
```
```go
func mapItemStarted(it codexItem) Result {
	switch it.Type {
	case "agentMessage":
		return Result{Events: []events.Event{events.NewTextMessageStartEvent(it.ID, events.WithRole("assistant"))}}
	default:
		return Result{}
	}
}
```
Change the completed `agentMessage` case:
```go
	case "agentMessage":
		return Result{Events: []events.Event{events.NewTextMessageEndEvent(it.ID)}}
```

- [ ] **Step 5: Update the integration test's fake CXG to stream**

In `internal/browsergateway/integration_test.go`, in the fake CXG's `turn/start` handler, replace the single `item/completed` agentMessage notification with the streaming sequence:
```go
			case "turn/start":
				reply(*m.ID, `{"turn":{"id":"trn-1"}}`)
				notify("item/started", `{"item":{"type":"agentMessage","id":"msg-1","text":"","phase":null},"threadId":"thr-1","turnId":"trn-1","startedAtMs":1}`)
				notify("item/agentMessage/delta", `{"threadId":"thr-1","turnId":"trn-1","itemId":"msg-1","delta":"Hello!"}`)
				notify("item/completed", `{"item":{"type":"agentMessage","id":"msg-1","text":"Hello!","phase":"final_answer"},"threadId":"thr-1","turnId":"trn-1","completedAtMs":2}`)
				notify("turn/completed", `{"threadId":"thr-1","turn":{"id":"trn-1","status":"completed","items":[],"error":null}}`)
```
The existing assertions (`RUN_STARTED`, `TEXT_MESSAGE_START/CONTENT/END`, `RUN_FINISHED`, delta "Hello!") still hold because the sequence still produces exactly those event types.

- [ ] **Step 6: Run mapper + integration + race**

Run: `cd /root/agentserver && go test ./internal/browsergateway/... -race -count=1`
Expected: PASS (mapper streaming tests + the updated integration test).

- [ ] **Step 7: Commit**

```bash
cd /root/agentserver && gofmt -w internal/browsergateway/
git add internal/browsergateway/mapper/ internal/browsergateway/integration_test.go
git commit -m "feat(browser-gateway): stream agentMessage deltas (started/delta/completed lifecycle)"
```

---

### Task 7: End-to-end integration test for a tool run

**Files:**
- Modify: `internal/browsergateway/integration_test.go`

**Interfaces:**
- Consumes: the full stack. Adds a second scenario emitting a `commandExecution` completed item and asserting the SSE stream contains `TOOL_CALL_START/ARGS/END/RESULT` and a `CUSTOM` (a2ui.operations) event.

- [ ] **Step 1: Add the tool-run integration test**

Append to `internal/browsergateway/integration_test.go` a `TestIntegration_ToolRun` that reuses the fake-CXG pattern but, on `turn/start`, emits a `commandExecution` `item/completed` then `turn/completed`, and asserts the SSE body contains the tool-call event types and a `CUSTOM` event whose `name` is `a2ui.operations`:
```go
func TestIntegration_ToolRun(t *testing.T) {
	cxg := fakeCXGWith(t, func(ctx context.Context, c *websocket.Conn, notify func(string, string)) {
		notify("item/completed", `{"item":{"type":"commandExecution","id":"cmd-1","command":"ls","cwd":"/w","processId":null,"source":"agent","status":"completed","commandActions":[],"aggregatedOutput":"total 0","exitCode":0,"durationMs":5},"threadId":"thr-1","turnId":"trn-1"}`)
		notify("turn/completed", `{"threadId":"thr-1","turn":{"id":"trn-1","status":"completed","items":[],"error":null}}`)
	})
	defer cxg.Close()
	// ... same server setup + POST /agui as TestIntegration_TextRun ...
	// assert eventTypes contains TOOL_CALL_START, TOOL_CALL_ARGS, TOOL_CALL_END, TOOL_CALL_RESULT, CUSTOM
	// assert at least one data line has "name":"a2ui.operations"
}
```
Refactor the P1 `fakeCXG` into a `fakeCXGWith(t, onTurnStart func(...))` helper so both `TestIntegration_TextRun` and `TestIntegration_ToolRun` share the handshake/auth and differ only in the per-turn notifications. Keep `TestIntegration_TextRun` green by passing it the streaming agent-message sequence from Task 6.

Concretely, the assertion block mirrors `TestIntegration_TextRun`'s scanner loop; collect `ev.Type` and also capture the raw `data:` line to check for `"name":"a2ui.operations"`. Break on `RUN_FINISHED`.

- [ ] **Step 2: Run the integration suite + race**

Run: `cd /root/agentserver && go test ./internal/browsergateway/ -run TestIntegration -race -count=1 -v`
Expected: PASS (both `TestIntegration_TextRun` and `TestIntegration_ToolRun`).

- [ ] **Step 3: Full suite + vet + module build**

Run: `cd /root/agentserver && go test ./internal/browsergateway/... ./cmd/browser-gateway/... -race -count=1 && go vet ./internal/browsergateway/... && go build ./...`
Expected: all `ok`; vet silent; build exit 0.

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver && gofmt -w internal/browsergateway/integration_test.go
git add internal/browsergateway/integration_test.go
git commit -m "test(browser-gateway): end-to-end tool-run integration (tool events + A2UI)"
```

---

## Self-Review

**1. Spec coverage (P2 scope from the design spec §2/§7):**
- Tool events for commandExecution/fileChange → Tasks 4, 5. ✓
- Gateway-side A2UI synthesis, delivered as AG-UI `CUSTOM{name:"a2ui.operations"}` → Tasks 3 (builders), 4, 5 (emit). ✓
- codex version alignment (deploy the version we map against) → Task 1. ✓
- reasoning correctness against real schema (bug found reading source) → Task 2. ✓
- agentMessage delta streaming (P1-deferred, now schema-confirmed) → Task 6. ✓
- End-to-end verification → Task 7. ✓
- Explicitly out of P2 (P3 follow-up): CopilotKit frontend, CI/Helm wiring, HITL/approvals, A2UI interactive callbacks, `item/commandExecution/outputDelta` live streaming (cards use final `aggregatedOutput`). Noted, not built.

**2. Placeholder scan:** No TBD/TODO. Every code step shows complete code; Task 1 is an investigate-then-modify task whose "code" is conditional and fully described with the exact commands + decision rule. Task 7 Step 1 gives the test skeleton + explicit refactor instruction and the concrete notifications; the assertion loop is described as "mirror TestIntegration_TextRun's scanner" with the exact strings to check — acceptable because that loop already exists verbatim in the file being modified.

**3. Type consistency:**
- `a2ui.Message{Version, CreateSurface, UpdateComponents, UpdateDataModel}`, `a2ui.Component{ID, Component, Child, Children, Text}`, `a2ui.CommandCard(id,command,output,statusLine) []Message`, `a2ui.FileDiffCard(id, []FileChange) []Message`, `a2ui.FileChange{Path,Kind,Diff}`, `a2ui.CatalogID` — defined Task 3, consumed Tasks 4/5. ✓
- mapper `codexItem` fields added incrementally (Summary/Content Task 2; Command/AggregatedOutput/ExitCode/Status Task 4; Changes Task 5) — all on the one struct, no collision. ✓
- AG-UI SDK calls: `NewToolCallStartEvent(id, name)`, `NewToolCallArgsEvent(id, delta)`, `NewToolCallEndEvent(id)`, `NewToolCallResultEvent(msgID, toolCallID, content)`, `NewCustomEvent(name, WithValue(v))`, `NewTextMessageStartEvent(id, WithRole(...))`, `NewTextMessageContentEvent(id, delta)`, `NewTextMessageEndEvent(id)`, `NewReasoningMessage*Event` — all match the signatures used/verified in P1. ✓
- EventType constants referenced in tests (`EventTypeToolCallStart/Args/End/Result`, `EventTypeCustom`, `EventTypeTextMessage*`, `EventTypeReasoningMessage*`) — confirm exact spelling in the SDK `events.go` at Task 3/4 first use; P1 already used the TextMessage/Reasoning ones successfully. ✓
- Test helper name (`typesOf` vs P1's `types`): each task notes to match the existing P1 helper name; if P1 used `types`, use `types` throughout. Flagged in Tasks 4 and 6.

Fixed inline: none needed beyond the helper-name note.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-20-browser-gateway-backend-p2.md`. Two execution options:

**1. Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, on a branch stacked off `browser-gateway-p1` (no worktree — subagents pin to the repo root).

**2. Inline Execution** — execute in this session with checkpoints.

Follow-up plan after P2: **P3** — CopilotKit reference frontend (consuming `a2ui.operations` CUSTOM events via `@a2ui/react`) + CI (`build-browser-gateway`) + Helm (`templates/browser-gateway.yaml`, `values.yaml`, httproute).
