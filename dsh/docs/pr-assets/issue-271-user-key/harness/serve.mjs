import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { mkdir, writeFile } from 'node:fs/promises'
import { spawn } from 'node:child_process'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { openVikingUserKeyFixture } from '../../../../scripts/fixtures/openviking-user-key.mjs'

const args = new Map()
for (let i = 2; i < process.argv.length; i += 2) args.set(process.argv[i], process.argv[i + 1])
for (const name of ['--artifacts', '--run-root', '--framework-root', '--native-cli']) assert(args.has(name), `Missing ${name}`)
const runRoot = resolve(args.get('--run-root'))
const nativeCli = resolve(args.get('--native-cli'))
await mkdir(runRoot, { recursive: true, mode: 0o700 })
const apiKey = randomUUID()
const backend = openVikingUserKeyFixture(new Map([[apiKey, { account: 'fixture', user: 'alice' }]]))
const uri = 'viking://user/alice/memories/entities/issue-271-canary.md'
backend.files.set(`fixture:${uri}`, { account: 'fixture', uri, content: 'Issue 271 synthetic memory: user-key discovery can browse this exact text.\n这是隔离夹具数据，不是真实火山云账号。' })
await new Promise(done => backend.server.listen(0, '127.0.0.1', done))
const settings = { endpoint: `http://127.0.0.1:${backend.server.address().port}/openviking`, account: 'fixture', discoveryUser: 'alice', apiKey }
await writeFile(join(runRoot, 'provider-private.json'), JSON.stringify(settings, null, 2) + '\n', { mode: 0o600 })
const saveEvidence = () => writeFile(join(runRoot, 'provider-evidence.json'), JSON.stringify({
  fixture: 'deterministic-openviking-user-key', cloudAccountVerified: false,
  requests: backend.requests, files: [...backend.files.values()],
}, null, 2) + '\n')
await saveEvidence()
const evidenceTimer = setInterval(() => { void saveEvidence() }, 1000)
const helper = resolve(dirname(fileURLToPath(import.meta.url)), '../../issue-267-settings-migration/harness/packed-e2e.mjs')
// Both source revisions support this public DSH cohort. "fixed" selects the
// helper's normal-peer install path; the supplied artifacts select the code.
const child = spawn(process.execPath, [helper, '--mode', 'fixed', '--framework-version', '0.1.7-alpha.1',
  '--framework-root', resolve(args.get('--framework-root')), '--artifacts', resolve(args.get('--artifacts')),
  '--run-root', join(runRoot, 'web'), '--native-cli', nativeCli, '--serve', 'true'], { stdio: 'inherit' })
console.log(`Issue 271 fixture settings: ${join(runRoot, 'provider-private.json')} (private)`)
console.log(`WebUI bootstrap state will be written to ${join(runRoot, 'web/server.json')} (private)`)
let stopping = false
async function stop() {
  if (stopping) return
  stopping = true
  clearInterval(evidenceTimer)
  if (child.exitCode === null && child.signalCode === null) { child.kill('SIGTERM'); await new Promise(done => child.once('exit', done)) }
  backend.server.closeAllConnections()
  await new Promise(done => backend.server.close(done))
  await saveEvidence()
}
child.once('exit', code => { process.exitCode = code ?? 0; void stop() })
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => { void stop() })
