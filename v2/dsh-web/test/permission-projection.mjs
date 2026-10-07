import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import assert from 'node:assert/strict'

// Exercise the exact shipped store; no changes to upstream UI or merge rules.
const file = new URL('../dist/plugins/@deepseek-ai/dsh-api-session-controller/client.js', import.meta.url)
const source = readFileSync(file, 'utf8').replace('return module.exports;', 'return { ProjectionValueStore, assertSessionWireEvent };')
let factory
vm.runInNewContext(source, { window: { __ModuleLoader__: { load: module => { factory = module.factory } } }, console, queueMicrotask, setTimeout, clearTimeout }, { filename: file.pathname })
const imports = {
  '@deepseek-ai/cordis': {},
  '@deepseek-ai/dsh-api-gateway/client': { RemoteJournalStream: class {} },
  '@deepseek-ai/dsh-client-store': { notifySubscribers: listeners => { for (const f of listeners) f() } },
}
const { ProjectionValueStore, assertSessionWireEvent } = factory(name => { assert.ok(name in imports, name); return imports[name] })
const { baseline, frames, replay, events } = JSON.parse(readFileSync(0, 'utf8'))
for (const event of events) assertSessionWireEvent(event)
const store = new ProjectionValueStore()
store.seed(baseline)
assert.equal(store.get('permissions').currentValue, 'read-only')
for (const frame of frames) {
  store.apply(frame.key, frame.value, frame.seq)
  assert.equal(store.get('permissions').currentValue, frame.value.currentValue)
  // Delayed initial snapshots and duplicate frames must not undo a change.
  store.seed(baseline)
  assert.equal(store.get('permissions').currentValue, frame.value.currentValue)
}
store.clear()
store.seed(replay)
assert.equal(store.get('permissions').currentValue, 'full-access')
assert.equal(store.seqOf('permissions'), frames.at(-1).seq)
// Negative control reproduces the original equal-sequence bug.
store.clear()
store.seed(baseline)
store.apply('permissions', { currentValue: 'full-access' }, baseline.asOfSeq)
assert.equal(store.get('permissions').currentValue, 'read-only')
