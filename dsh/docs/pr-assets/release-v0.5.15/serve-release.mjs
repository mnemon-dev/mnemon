import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { actualReceipt, servePackedWebFixture } from '../../../scripts/fixtures/packed-web-fixture.mjs'

const seed = 'RELEASE515_USER: I prefer concise replies.'
const updated = 'RELEASE515_USER: I prefer concise replies with examples.'
await servePackedWebFixture({
  issue: 'release-v0.5.15',
  async prepare({ data }) {
    const now = Date.now()
    await mkdir(join(data, 'runtime'), { recursive: true, mode: 0o700 })
    await writeFile(join(data, 'runtime/memories.json'), JSON.stringify({ version: 1, entries: [{
      target: 'user', content: seed, importance: 'critical',
      created_at: new Date(now - 14.25 * 86400000).toISOString(),
      updated_at: new Date(now - 2.25 * 86400000).toISOString(),
    }] }, null, 2) + '\n', { mode: 0o600 })
    return { metadata: { purpose: 'v0.5.15 integrated release smoke', commands: ['RELEASE515_REPLACE', 'RELEASE515_SHOW'] } }
  },
  complete({ user, tools, receipts, runtimeProjections }) {
    if (user.includes('RELEASE515_REPLACE')) {
      if (receipts.length) return { reply: actualReceipt(receipts.at(-1)), observation: { command: 'RELEASE515_REPLACE' } }
      if (!tools.some(tool => tool.name === 'mnemon_runtime_memory')) return { reply: 'Native Runtime tool is unavailable; select the standard Coding preset.' }
      return { reply: { name: 'mnemon_runtime_memory', args: { action: 'replace', target: 'user', old_text: seed, content: updated, importance: 'critical', branches: [] } }, observation: { command: 'RELEASE515_REPLACE' } }
    }
    if (user.includes('RELEASE515_SHOW')) {
      const projection = runtimeProjections.at(-1) ?? ''
      const annotations = [...projection.matchAll(/\[importance=(critical|normal|low); created=([^;\]\n]+); updated=([^\]\n]+)\]/gu)].map(match => ({ importance: match[1], created: match[2], updated: match[3] }))
      const contents = [...projection.matchAll(/<runtime-memory-file name="(USER|MEMORY)\.md">([^]*?)<\/runtime-memory-file>/gu)].map(match => `${match[1]}.md:\n${match[2].trim()}`)
      return { reply: `Actual injected Runtime metadata: ${annotations.length} annotation(s).\n\n${contents.join('\n\n')}`, observation: { command: 'RELEASE515_SHOW', annotations } }
    }
    return { reply: 'Release fixture ready. Send RELEASE515_REPLACE, then RELEASE515_SHOW. Only model choices are scripted; DSH tools and storage are real.' }
  },
})
