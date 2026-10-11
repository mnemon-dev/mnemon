import { mkdtempSync, mkdirSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import type { MemoryJsonValue } from 'dsh-mnemon/contracts'
import * as plugin from '../src/index.ts'
import { strategy } from './fixture.ts'

const releases: Array<() => Promise<void>> = []
afterEach(async () => { for (const release of releases.splice(0).reverse()) await release() })

async function fixture(limitBytes = 10 * 1024 * 1024) {
  const directory = mkdtempSync(join(tmpdir(), 'mnemon-documents-create-'))
  const workspace = join(directory, 'workspace')
  mkdirSync(workspace)
  const runner = new MemoryCompositionRunner()
  releases.push(async () => { try { await runner.dispose() } finally { rmSync(directory, { recursive: true, force: true }) } })
  await runner.mount(strategy, { instanceId: 'strategy' })
  await runner.mount(plugin, { instanceId: 'documents', config: { dataDir: join(directory, 'data'), limitBytes } })
  const base = { sourceInstanceKey: 'source:documents', scope: { storage: 'custom' as const, workspaceId: workspace }, confirmed: false }
  const initial = await runner.executeManagement({ ...base, mode: 'read', operation: 'snapshot', input: null })
  const original = await runner.executeManagement({ ...base, mode: 'mutate', operation: 'mutate', confirmed: true, expectedRevision: initial.revision,
    input: { action: 'create', title: 'Release implementation', content: '# Original implementation\n\n```ts\nconst retries = 3\n```\n\nKeep this exact implementation.', sessionIds: ['user-session'] } })
  const { document } = original.value as unknown as plugin.DocumentMutationResult
  const read = () => runner.executeManagement({ ...base, mode: 'read', operation: 'document', input: { id: document.id } })
  const snapshot = () => runner.executeManagement({ ...base, mode: 'read', operation: 'snapshot', input: null })
  const turn = await runner.beginTurn({ scope: base.scope })
  const create = turn.view.actionOffers.find(offer => offer.sourceActionId === 'create')
  return { runner, base, document, original, turn, create, read, snapshot }
}

describe('create-only Documents Action', () => {
  it('creates a separate document even with the same title and preserves the existing original', async () => {
    const f = await fixture()
    expect(f.create).toBeDefined()
    const result = await f.turn.executeAction(f.create!.id, { title: f.document.title, content: 'Additional rollout evidence.' }, () => true)
    expect(result).toMatchObject({ status: 'succeeded', completion: 'committed', details: { action: 'created', document: { revision: 1 } } })
    expect((result.details as unknown as plugin.DocumentMutationResult).document.id).not.toBe(f.document.id)
    expect((await f.read()).value).toMatchObject({ content: f.document.content, contentHash: f.document.contentHash, revision: f.document.revision })
    expect((await f.snapshot()).value).toMatchObject({ activeCount: 2 })
  })

  it.each([
    { action: 'update', id: 'original' },
    { action: 'archive', id: 'original' },
    { action: 'create' },
    { id: 'original' },
    { operation: 'manage' },
  ])('rejects mutation selectors %j without changing persisted data', async selector => {
    const f = await fixture()
    expect(f.create).toBeDefined()
    const input = { title: 'Condensed replacement', content: 'Summary without implementation.', ...selector,
      ...('id' in selector ? { id: f.document.id } : {}) } as MemoryJsonValue
    await expect(f.turn.executeAction(f.create!.id, input, () => true)).rejects.toThrow()
    expect((await f.read()).value).toMatchObject({ content: f.document.content, contentHash: f.document.contentHash, revision: f.document.revision })
    expect(await f.snapshot()).toMatchObject({ revision: f.original.revision, value: { activeCount: 1, archivedCount: 0 } })
  })

  it('fails at capacity without archiving or overwriting an existing document', async () => {
    const f = await fixture(2048)
    expect(f.create).toBeDefined()
    await expect(f.turn.executeAction(f.create!.id, { title: 'Too large', content: 'x'.repeat(2048) }, () => true)).rejects.toThrow(/capacity/i)
    expect((await f.read()).value).toMatchObject({ content: f.document.content, status: 'active', revision: f.document.revision })
    expect(await f.snapshot()).toMatchObject({ revision: f.original.revision, value: { activeCount: 1, archivedCount: 0 } })
  })

  it('keeps the existing manage Action available for an authorized update', async () => {
    const f = await fixture()
    const manage = f.turn.view.actionOffers.find(offer => offer.sourceActionId === 'manage')!
    await expect(f.turn.executeAction(manage.id, { action: 'update', id: f.document.id, content: 'Explicitly revised implementation.' }, () => true))
      .resolves.toMatchObject({ completion: 'committed', details: { action: 'updated' } })
    expect((await f.read()).value).toMatchObject({ content: 'Explicitly revised implementation.', revision: f.document.revision + 1 })
  })
})
