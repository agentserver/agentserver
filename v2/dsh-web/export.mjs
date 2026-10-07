// Export the official web profile, including its generated plugin graph.
// Launch tokens and cookies exist only in this process and are never exported.
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { cp, glob, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, dirname, join, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const [sourceArgument, outputArgument] = process.argv.slice(2)
if (!sourceArgument || !outputArgument) throw new Error('usage: node export.mjs SOURCE_CHECKOUT NEW_OUTPUT_DIRECTORY')
const sourceRoot = resolve(sourceArgument)
const outputRoot = resolve(outputArgument)
await mkdir(outputRoot, { recursive: false })
const profileDirectory = await mkdtemp(join(tmpdir(), 'agentserver-dsh-export-'))
const overlay = fileURLToPath(new URL('./export-overlay.yml', import.meta.url))
const environment = Object.fromEntries(['PATH', 'HOME', 'TMPDIR', 'LANG', 'LC_ALL', 'SHELL'].flatMap(name => process.env[name] === undefined ? [] : [[name, process.env[name]]]))
const child = spawn(process.execPath, ['--import', 'tsx/esm', 'apps/cli/src/bin.ts', 'web', '--patch', overlay, '--host', '127.0.0.1', '--port', '0', '--no-open'], {
  cwd: sourceRoot,
  env: { ...environment, DSH_HOME: profileDirectory, CI: 'true', LEFTHOOK: '0' },
  stdio: ['ignore', 'pipe', 'pipe'],
})
const exited = once(child, 'exit')
let startup = ''
let rejectStartup
const launched = new Promise((resolveStartup, reject) => {
  rejectStartup = reject
  const capture = chunk => {
    startup += chunk.toString()
    const match = /dsh web: (http:\/\/127\.0\.0\.1:\d+\/\?token=[^\s]+)/u.exec(startup)
    if (match) resolveStartup(new URL(match[1]))
  }
  child.stdout.on('data', capture)
  child.stderr.on('data', capture)
  child.once('error', reject)
  child.once('exit', code => reject(new Error(`DSH export process exited before readiness (${code})`)))
})
const timer = setTimeout(() => rejectStartup(new Error('DSH export startup timed out')), 60_000)
try {
  const launchURL = await launched
  clearTimeout(timer)
  const login = await fetch(launchURL, { redirect: 'manual' })
  const cookie = login.headers.get('set-cookie')?.split(';')[0]
  if (login.status !== 303 || !cookie) throw new Error('local DSH export authentication failed')
  const origin = launchURL.origin
  const get = async path => {
    const url = new URL(path, origin)
    if (url.origin !== origin) throw new Error('export resource escaped the local DSH origin')
    const response = await fetch(url, { headers: { cookie }, redirect: 'error' })
    if (!response.ok) throw new Error(`export resource ${url.pathname}: HTTP ${response.status}`)
    return new Uint8Array(await response.arrayBuffer())
  }
  const html = new TextDecoder().decode(await get('/'))
  const graphJSON = /globalThis\["__DSH_BOOT__"\]\s*=\s*(\{[^]*?\})\s*;?<\/script>/u.exec(html)?.[1]
  if (!graphJSON) throw new Error('official boot graph missing from exported page')
  const graph = JSON.parse(graphJSON)
  if (!html.includes('<title>agentserver</title>') || !html.includes('"previewNotice":false')) throw new Error('title or preview-notice deployment option missing')
  if (graph.entries.some(entry => entry.id === '@deepseek-ai/dsh-client-hmr' || entry.id === '@deepseek-ai/dsh-client-ui-directory-picker-native')) throw new Error('deployment-disabled plugin is active')
  if (html.includes(launchURL.searchParams.get('token')) || html.includes(cookie)) throw new Error('local export credential reached static HTML')
  await cp(join(sourceRoot, 'apps/web/dist'), outputRoot, {
    recursive: true,
    filter: path => !path.endsWith('.map') && basename(path) !== 'preview' && basename(path) !== 'preview.html',
  })
  // Upstream combo URLs share /plugins/ and distinguish resources in the
  // query. Preserve those wire URLs; the Go asset server uses this exact map.
  const resources = new Map(graph.entries.map(entry => [entry.url, `plugins/${entry.id}/client.js`]))
  for (const [index, batch] of graph.batches.entries()) {
    if (!resources.has(batch.url)) resources.set(batch.url, `plugins/batches/${batch.phase}-${index}.js`)
  }
  const entries = new Map(graph.entries.map(entry => [entry.id, entry]))
  for await (const manifest of glob('packages/*/*/package.json', { cwd: sourceRoot })) {
    const pkg = JSON.parse(await readFile(join(sourceRoot, manifest), 'utf8'))
    const entry = entries.get(pkg.name)
    if (!entry) continue
    for await (const chunk of glob('lib/client.*.js', { cwd: join(sourceRoot, dirname(manifest)) })) {
      const file = `plugins/${entry.id}/${basename(chunk)}`
      resources.set(`${file}?rev=${entry.rev}`, file)
    }
  }
  const resourceMap = {}
  for (const [resource, file] of resources) {
    const url = new URL(resource, origin)
    const destination = resolve(outputRoot, file)
    if (!destination.startsWith(outputRoot + sep)) throw new Error('invalid exported resource path')
    await mkdir(resolve(destination, '..'), { recursive: true })
    await writeFile(destination, await get(resource))
    resourceMap[url.pathname + url.search] = file
  }
  await writeFile(join(outputRoot, 'plugin-resources.json'), JSON.stringify(resourceMap, null, 2) + '\n')
  await writeFile(join(outputRoot, 'index.html'), html)
  const pkg = JSON.parse(await readFile(join(sourceRoot, 'package.json'), 'utf8'))
  process.stdout.write(JSON.stringify({ version: pkg.version, title: 'agentserver', plugins: graph.entries.length, resources: resources.size, output: outputRoot }) + '\n')
} finally {
  clearTimeout(timer)
  if (child.exitCode === null) child.kill('SIGTERM')
  const killTimer = setTimeout(() => child.kill('SIGKILL'), 5000)
  await exited.catch(() => {})
  clearTimeout(killTimer)
}
