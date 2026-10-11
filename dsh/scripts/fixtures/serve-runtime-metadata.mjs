import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { actualReceipt, servePackedWebFixture } from './packed-web-fixture.mjs'

const entries = [
  { target: 'user', importance: 'critical', content: 'META280_USER: I prefer concise replies.', createdDays: 14, updatedDays: 2 },
  { target: 'memory', importance: 'normal', content: 'META280_PROJECT: The project uses SQLite.', createdDays: 7, updatedDays: 3 },
  { target: 'memory', importance: 'low', content: 'META280_LOW: The sample accent is blue.', createdDays: 4, updatedDays: 1 },
]
const added = 'META280_LIVE: I prefer metric units.'
const replaced = 'META280_UPDATED: I prefer metric units and Celsius.'
const commands = {
  METADATA280_ADD: { action: 'add', target: 'user', content: added, importance: 'low' },
  METADATA280_REPLACE: { action: 'replace', target: 'user', old_text: added, content: replaced, importance: 'critical' },
  METADATA280_REMOVE: { action: 'remove', target: 'user', old_text: replaced },
}
await servePackedWebFixture({
  issue: 280,
  async prepare({ data, privateFile }) {
    const now = Date.now(), ago = days => new Date(now - (days * 24 + 6) * 3600_000).toISOString()
    const seeded = entries.map(({ createdDays, updatedDays, ...entry }) => ({ ...entry, created_at: ago(createdDays), updated_at: ago(updatedDays) }))
    await mkdir(join(data, 'runtime'), { recursive: true, mode: 0o700 })
    await writeFile(join(data, 'runtime/memories.json'), JSON.stringify({ version: 1, entries: seeded }, null, 2) + '\n', { mode: 0o600 })
    await privateFile('seed.json', JSON.stringify({ seededAt: new Date(now).toISOString(), entries: seeded }, null, 2) + '\n')
    return { metadata: { commands: ['METADATA280_SHOW', ...Object.keys(commands)], seededEntries: entries.length } }
  },
  complete({ user, tools, receipts, runtimeProjections }) {
    const command = user.match(/\b(METADATA280_SHOW|METADATA280_ADD|METADATA280_REPLACE|METADATA280_REMOVE)\b/u)?.[1]
    if (command === 'METADATA280_SHOW') {
      const projection = runtimeProjections.at(-1) ?? ''
      const annotations = [...projection.matchAll(/\[importance=(critical|normal|low); created=([^;\]\n]+); updated=([^\]\n]+)\]/gu)].map(match => ({ importance: match[1], created: match[2], updated: match[3] }))
      const contents = [...projection.matchAll(/<runtime-memory-file name="(USER|MEMORY)\.md">([^]*?)<\/runtime-memory-file>/gu)].map(match => `${match[1]}.md:\n${match[2].trim()}`)
      const status = projection === '' ? 'No Runtime snapshot found in this model request.' : annotations.length === 0 ? 'The actual Runtime projection contains no importance/age annotations.' : `The actual Runtime projection contains ${annotations.length} importance/age annotations.`
      return { reply: `${status}\n\n${contents.join('\n\n')}`, observation: { command, annotations, snapshotFound: projection !== '' } }
    }
    if (command in commands) {
      if (receipts.length > 0) return { reply: `${command}: ${actualReceipt(receipts.at(-1))}`, observation: { command } }
      if (!tools.some(tool => tool.name === 'mnemon_runtime_memory')) return { reply: 'The actual request did not advertise mnemon_runtime_memory; select Coding with native tools.', observation: { command } }
      return { reply: { name: 'mnemon_runtime_memory', args: commands[command] }, observation: { command } }
    }
    return { reply: 'The fixture conversation is working. Send METADATA280_SHOW to inspect the actual injected Runtime metadata, or METADATA280_ADD, METADATA280_REPLACE and METADATA280_REMOVE to exercise real user-memory mutations.' }
  },
})
