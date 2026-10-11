import { afterEach, describe, expect, it } from 'vitest'
import * as scoped from 'dsh-mnemon-strategy-scoped'
import * as lightContext from 'dsh-mnemon-strategy-light-context'
import * as autoCapture from 'dsh-mnemon-strategy-auto-capture'
import type { RuntimeMemorySnapshot } from 'dsh-mnemon-source-runtime/contracts'
import type { HostAgent, HostContextShape, ToolDefinition } from '../src/host/dsh.ts'
import { agentScope } from '../src/host/runtime.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { registerTools } from '../src/host/tools.ts'
import { compositionFixture } from './fixtures/composition.ts'
import { sessionLog } from './fixtures/session-log.ts'

const fixtures: Awaited<ReturnType<typeof compositionFixture>>[] = []
afterEach(async () => { for (const f of fixtures.splice(0)) await f.dispose() })

async function fixture(enhanced = false, child = false) {
  const f = await compositionFixture({}, enhanced ? { entryPrefix: 'custom' } : {})
  fixtures.push(f)
  if (enhanced) {
    await f.mount(scoped, { instanceId: 'scoped' })
    await f.mount(lightContext, { instanceId: 'light-context' })
    await f.mount(autoCapture, { instanceId: 'auto-capture' })
  }
  const root = { id: 'root', session: { header: { cwd: f.workspace }, ...sessionLog() } } as unknown as HostAgent
  const agent = child
    ? { id: 'child', session: { header: { cwd: f.workspace, origin: 'subagent', parentSession: root.id }, ...sessionLog() } } as unknown as HostAgent
    : root
  const tools = new Map<string, ToolDefinition>()
  const coordinator = new MnemonSubagentCoordinator({} as never, f.live)
  registerTools({ tools: { register: (tool: ToolDefinition) => tools.set(tool.name, tool) } } as unknown as HostContextShape, f.live, coordinator)
  const turn = await f.graph.composableTurns.beginTurn('root:1', agentScope(root, f.config))
  if (child) f.graph.composableTurns.pinTurn('child:1', agentScope(agent, f.config), turn.view.id)
  const execute = (input: object, name = 'mnemon_runtime_memory') => tools.get(name)!.execute(input as never, { agent, signal: new AbortController().signal })
  const runtime = f.graph.source('runtime')
  const snapshot = () => runtime.read<RuntimeMemorySnapshot>('snapshot')
  return { ...f, execute, runtime, snapshot, turn }
}

describe('Runtime USER tool empty-branch compatibility (issue 281)', () => {
  it.each([
    [false, false], [false, true], [true, false], [true, true],
  ])('adds, replaces and removes USER entries with an empty array (enhanced=%s, child=%s)', async (enhanced, child) => {
    const f = await fixture(enhanced, child)
    const unchanged = 'The synthetic project uses SQLite.'
    await f.runtime.mutate('mutate', { action: 'add', target: 'memory', content: unchanged })
    const base = await f.snapshot()
    await expect(f.execute({ action: 'add', target: 'user', content: 'I prefer concise replies.', branches: [] }))
      .resolves.toMatchObject({ added: 'I prefer concise replies.' })
    await expect(f.execute({ action: 'replace', target: 'user', old_text: 'I prefer concise replies.', content: 'I prefer detailed replies.', importance: 'critical', branches: [] }))
      .resolves.toMatchObject({ replaced: { from: 'I prefer concise replies.', to: 'I prefer detailed replies.' } })
    const changed = await f.snapshot()
    expect(changed.entries.filter(entry => entry.target === 'user')).toEqual([
      expect.objectContaining({ content: 'I prefer detailed replies.', importance: 'critical' }),
    ])
    expect(changed.entries.find(entry => entry.target === 'user')).not.toHaveProperty('branches')
    expect(changed.entries.filter(entry => entry.target === 'memory')).toEqual(base.entries)
    await expect(f.execute({ action: 'remove', target: 'user', old_text: 'I prefer detailed replies.', branches: [] }))
      .resolves.toMatchObject({ removed: 'I prefer detailed replies.' })
    expect((await f.snapshot()).entries).toEqual(base.entries)
  })

  it('keeps omitted USER branches and MEMORY branch preservation, replacement and clearing', async () => {
    const f = await fixture()
    await f.execute({ action: 'add', target: 'user', content: 'I prefer concise replies.' })
    await f.execute({ action: 'add', target: 'memory', content: 'Scoped decision v1.', branches: ['feature/281'] })
    await f.execute({ action: 'replace', target: 'memory', old_text: 'Scoped decision v1.', content: 'Scoped decision v2.' })
    expect((await f.snapshot()).entries.find(entry => entry.target === 'memory')).toMatchObject({ branches: ['feature/281'] })
    await f.execute({ action: 'replace', target: 'memory', old_text: 'Scoped decision v2.', content: 'Scoped decision v3.', branches: ['main'] })
    expect((await f.snapshot()).entries.find(entry => entry.target === 'memory')).toMatchObject({ branches: ['main'] })
    await f.execute({ action: 'replace', target: 'memory', old_text: 'Scoped decision v3.', content: 'Cross-branch decision.', branches: [] })
    expect((await f.snapshot()).entries.find(entry => entry.target === 'memory')).not.toHaveProperty('branches')
  })

  it.each([['feature/281'], [''], ['   '], [null], null, '', { length: 0 }].map(branches => ({ branches })))('rejects invalid USER branches $branches without changing storage', async ({ branches }) => {
    const f = await fixture()
    await f.runtime.mutate('mutate', { action: 'add', target: 'user', content: 'Original preference.' })
    const before = await f.snapshot()
    await expect(f.execute({ action: 'replace', target: 'user', old_text: 'Original preference.', content: 'Must not be saved.', branches }))
      .rejects.toThrow(/branches/)
    expect(await f.snapshot()).toMatchObject({ entries: before.entries, revision: before.revision })
  })

  it('keeps Source and generic View action contracts strict for USER branch fields', async () => {
    const f = await fixture(true)
    const input = { action: 'add', target: 'user', content: 'Must not be saved.', branches: [] }
    const before = await f.snapshot()
    await expect(f.runtime.mutate('mutate', input)).rejects.toThrow('branches applies to target=memory only')
    const offer = f.turn.view.actionOffers.find(offer => offer.sourceActionId === 'mutate')!
    await expect(f.execute({ offerId: offer.id, input }, 'mnemon_view_action')).rejects.toThrow('branches applies to target=memory only')
    expect(await f.snapshot()).toMatchObject({ entries: before.entries, revision: before.revision })
  })
})
