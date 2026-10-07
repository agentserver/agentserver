import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import assert from 'node:assert/strict'

const file = new URL('../dist/plugins/@deepseek-ai/dsh-api-session-controller/client.js', import.meta.url)
const source = readFileSync(file, 'utf8').replace('return module.exports;', 'return { ProjectionValueStore, assertSessionWireEvent };')
let factory
vm.runInNewContext(source, { window: { __ModuleLoader__: { load: module => { factory = module.factory } } }, console, queueMicrotask, setTimeout, clearTimeout }, { filename: file.pathname })
const imports = { '@deepseek-ai/cordis': {}, '@deepseek-ai/dsh-api-gateway/client': { RemoteJournalStream: class {} }, '@deepseek-ai/dsh-client-store': { notifySubscribers: listeners => { for (const f of listeners) f() } } }
const { ProjectionValueStore, assertSessionWireEvent } = factory(name => { assert.ok(name in imports, name); return imports[name] })
const { baseline, frames, events, replay, list } = JSON.parse(readFileSync(0, 'utf8'))
const store = new ProjectionValueStore()
store.seed(baseline)
assert.equal(store.get('title'), null)
for (const frame of frames) {
  store.apply(frame.key, frame.value, frame.seq)
  assert.equal(store.get('title'), frame.value)
  store.seed(baseline)
  assert.equal(store.get('title'), frame.value)
}
assert.equal(store.get('title'), '我的手动标题')
const fresh = new ProjectionValueStore()
fresh.applyCached(list.projections.values)
assert.equal(fresh.get('title'), '我的手动标题')
fresh.seed(replay)
assert.equal(fresh.get('title'), store.get('title'))
for (const event of events) assertSessionWireEvent(event)
const titles = events.filter(event => event.type === 'session/title')
assert.deepEqual(titles.map(event => event.data.source.kind), ['fallback', 'provider', 'user'])
const userSeq = events.find(event => event.type === 'user/message').seq
assert.deepEqual(titles[0].data.messageSeqs, [userSeq])
assert.deepEqual(titles[1].data.messageSeqs, [userSeq])
assert.deepEqual(titles[2].data.messageSeqs, [])
