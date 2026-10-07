import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import assert from 'node:assert/strict'

// Exercise the exact shipped definitions and layout, without a browser or
// changing their public exports. Rendering-only imports are not exercised.
const file = new URL('../dist/plugins/@deepseek-ai/dsh-client-ui-trajectory/client.js', import.meta.url)
const source = readFileSync(file, 'utf8').replace('return module.exports;', `return {
  registerTrajectoryMessageDefinitions, registerTrajectoryAssistantDefinition,
  registerTrajectoryToolDefinition, TrajectorySnapshotBuilder, deriveTrajectoryLayout,
};`)
let factory
vm.runInNewContext(source, { window: { __ModuleLoader__: { load: module => { factory = module.factory } } }, console, setTimeout, clearTimeout }, { filename: file.pathname })
const imports = {
  react: { memo: value => value, forwardRef: value => value, createContext: () => ({}) },
  'react-dom': {},
  'react/jsx-runtime': { jsx: (type, props) => ({ type, props }), jsxs: (type, props) => ({ type, props }) },
  '@deepseek-ai/dsh-client-store': {},
  '@deepseek-ai/dsh-client-ui-primitives': { extractMarkdownPlainText: text => text },
}
const runtime = factory(name => {
  assert.ok(name in imports, `unhandled bundle import ${name}`)
  return imports[name]
})
const definitions = []
const registration = { uiConversation: { events: { register: value => definitions.push(value) } } }
runtime.registerTrajectoryMessageDefinitions(registration)
runtime.registerTrajectoryAssistantDefinition(registration)
runtime.registerTrajectoryToolDefinition(registration)

function layout(events) {
  const contexts = new Map()
  for (const event of events) {
    for (const definition of definitions) {
      const matched = definition.match(event)
      if (matched === null) continue
      const key = `${definition.kind}:${matched.id}`
      let context = contexts.get(key)
      const match = { event, location: { kind: 'unresolved' } }
      if (matched.role === 'start') {
        context = { key, kind: definition.kind, id: matched.id, start: match, matches: [match], definition }
        context.state = definition.start(context, match, { previous: () => undefined })
        contexts.set(key, context)
      } else {
        assert.ok(context, `update without start: ${key}`)
        context.matches.push(match)
        context.state = definition.update(context, match)
      }
    }
  }
  const nodes = [...contexts.values()].flatMap(context => {
    const node = context.definition.buildViewNode?.(context)
    return node == null ? [] : [node]
  })
  const snapshot = new runtime.TrajectorySnapshotBuilder().replace({ nodes })
  const turns = runtime.deriveTrajectoryLayout({ ...snapshot, nodes: snapshot.eventNodes }, (key, values) => key + JSON.stringify(values ?? {}))
  return turns.flatMap(turn => turn.groups.flatMap(group => group.cells.map(cell => ({ turn: turn.turn, ...cell }))))
}

const events = JSON.parse(readFileSync(0, 'utf8'))
const beforeResult = events.findIndex(event => event.type === 'tool/result')
const running = layout(events.slice(0, beforeResult)).filter(cell => cell.kind === 'tool')
assert.deepEqual(Array.from(running, cell => cell.callId), ['call-a', 'call-b'])
assert.ok(running.every(cell => cell.turn === 2), 'running tool has the wrong turn')
const cells = layout(events)
assert.equal(cells[0].kind, 'user')
assert.equal(cells[0].turn, 1)
const secondUser = cells.findIndex(cell => cell.kind === 'user' && cell.turn === 2)
assert.ok(secondUser > 0, 'second user message missing')
const tools = cells.filter(cell => cell.kind === 'tool')
assert.deepEqual(Array.from(tools, cell => cell.callId), ['call-a', 'call-b'])
assert.ok(tools.every(cell => cell.turn === 2), 'tool moved into the greeting turn')
assert.ok(cells.slice(0, secondUser).every(cell => cell.kind !== 'tool'), 'tool precedes its user request')
assert.ok(cells.some(cell => cell.thinkingDetail === 'inspect first'), 'reasoning overwritten by final answer')
assert.ok(cells.some(cell => cell.previewMarkdown === 'done'), 'final response missing')

// Sensitivity check: omit the Assistant tool-call ownership as the old facade
// did. The actual DSH layout prepends the orphan tools before the greeting.
const unowned = events.filter(event => !(event.type === 'assistant/message' && event.data.message.content.some(block => block.type === 'tool-call')))
const broken = layout(unowned)
assert.equal(broken[0].kind, 'tool', 'fixture no longer reproduces the reported ordering defect')
process.stdout.write(JSON.stringify({ turns: [...new Set(cells.map(cell => cell.turn))], toolCalls: tools.map(cell => cell.callId), ordering: 'user-before-owned-tools', legacy: 'orphan-tools-before-greeting' }) + '\n')
