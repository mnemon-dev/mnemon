import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import type { Context } from '@deepseek-ai/cordis'
import { describe, expect, it, vi } from 'vitest'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import { HolographicProvider } from 'dsh-mnemon-provider-holographic'
import { installMemorySpaces } from '../src/index.ts'
import type { MemorySpaceCatalog } from '../src/contracts.ts'
import { providerEntries } from './providers.ts'
import { strategy } from './fixture.ts'

describe('Memory Spaces metadata IO through public Cordis plugins', () => {
  it('keeps live shared authority, durable receipts and cancellation without Provider data probes during composition', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'mnemon-spaces-io-'))
    const scope = { storage: 'custom' as const }
    const runners: MemoryCompositionRunner[] = []
    const mount = async () => {
      const runner = new MemoryCompositionRunner()
      runners.push(runner)
      await runner.mount(strategy, { instanceId: 'strategy' })
      await runner.mount({ inject: ['mnemonMemory'], async apply(ctx: Context) {
        await installMemorySpaces(ctx, providerEntries, { config: { dataDir: directory, cliPath: '/fake/mnemon' } })
      } }, { instanceId: 'spaces' })
      return { runner, client: await runner.managementClient('source:spaces', scope) }
    }
    const status = vi.spyOn(HolographicProvider.prototype, 'status')
    const search = vi.spyOn(HolographicProvider.prototype, 'search')
    const list = vi.spyOn(HolographicProvider.prototype, 'list')
    try {
      const first = await mount()
      const store = join(directory, 'facts.json')
      await first.client.mutate('provider-service-update', { providerId: 'holographic', settings: { dataPath: store }, enabled: true }, { confirmed: true })
      const catalog = (await first.client.read('body-directory')).value as unknown as MemorySpaceCatalog
      const body = catalog.items[0]!
      expect(body.provider.id).toBe('holographic')
      status.mockClear(); search.mockClear(); list.mockClear()
      const initialRevision = first.client.revision
      for (let index = 0; index < 4; index++) {
        const turn = await first.runner.beginTurn({ scope })
        expect(turn.view.readGrants[0]!.value).toMatchObject({ memoryBodyIds: [body.id] })
        turn.release()
        expect((await first.client.read('body-directory')).revision).toBe(initialRevision)
      }
      expect(status).not.toHaveBeenCalled()
      expect(search).not.toHaveBeenCalled()
      expect(list).not.toHaveBeenCalled()

      const turn = await first.runner.beginTurn({ scope })
      const offer = turn.view.actionOffers.find(offer => offer.sourceActionId === 'remember')!
      const receipt = await turn.executeAction(offer.id, { content: 'Durable provider checkpoint.', memoryBodyId: body.id, importance: 3 }, () => true)
      expect(receipt).toMatchObject({ status: 'succeeded', completion: 'committed', committedAt: expect.any(String) })
      expect(JSON.parse(readFileSync(store, 'utf8')).facts).toMatchObject([{ content: 'Durable provider checkpoint.' }])
      turn.release()
      const saved = await first.client.read('body-directory')
      expect(saved.revision).not.toBe(initialRevision)

      const second = await mount()
      const updated = await second.client.mutate('body-update', { memoryBodyId: body.id, name: 'Renamed elsewhere', active: false }, { confirmed: true })
      await expect(first.client.mutate('remember', { content: 'Stale write.', memoryBodyId: body.id }, { confirmed: true, expectedRevision: saved.revision })).rejects.toThrow('revision conflict')
      const changed = await first.client.read('body-directory')
      expect(changed.revision).toBe(updated.revision)
      expect(changed.value).toMatchObject({ activeCount: 0, items: [{ name: 'Renamed elsewhere', active: false }] })
      const inactive = await first.runner.beginTurn({ scope })
      expect(inactive.view.readGrants[0]!.value).toMatchObject({ memoryBodyIds: [], knownMemoryBodyIds: [body.id] })
      const grant = inactive.view.readGrants[0]!
      const writeScope = { viewId: inactive.view.id, grant: { ...grant } }
      expect((await first.client.read('body-directory', { writeScope })).value).toMatchObject({
        writeScope: { viewId: inactive.view.id, sourceInstanceKey: 'source:spaces', memoryBodyIds: [body.id] },
      })
      expect((await first.client.read('body-directory', { writeScope: { ...writeScope,
        grant: { ...grant, value: { memoryBodyIds: [body.id], knownMemoryBodyIds: [] } },
      } })).value).toMatchObject({ writeScope: { memoryBodyIds: [] } })
      expect((await first.client.read('body-directory', { writeScope: { ...writeScope,
        grant: { ...grant, value: { memoryBodyIds: [body.id] } },
      } })).value).toMatchObject({ writeScope: { memoryBodyIds: [body.id] } })
      for (const invalid of [{ ...grant, sourceInstanceKey: 'source:foreign' }, { ...grant, schema: 'foreign-schema' }]) {
        await expect(first.client.read('body-directory', { writeScope: { ...writeScope, grant: invalid } }))
          .rejects.toThrow('write scope grant does not belong to this Source')
      }
      await expect(first.client.read('body-directory', { writeScope: { ...writeScope,
        grant: { ...grant, value: { memoryBodyIds: [], knownMemoryBodyIds: [42] } },
      } })).rejects.toThrow('knownMemoryBodyIds')
      inactive.release()

      await second.client.mutate('body-update', { memoryBodyId: body.id, active: true }, { confirmed: true })
      const current = await first.client.read('body-directory')
      const diskBefore = readFileSync(store, 'utf8')
      const controller = new AbortController()
      controller.abort(new Error('cancelled before mutation'))
      await expect(first.runner.executeManagement({ sourceInstanceKey: 'source:spaces', scope, mode: 'mutate', operation: 'remember',
        input: { content: 'Cancelled write.', memoryBodyId: body.id }, confirmed: true, expectedRevision: current.revision, signal: controller.signal,
      })).rejects.toThrow('cancelled before mutation')
      expect(readFileSync(store, 'utf8')).toBe(diskBefore)

      await second.client.mutate('provider-service-update', { providerId: 'holographic', settings: {}, enabled: false }, { confirmed: true })
      expect((await first.client.read('body-directory')).value).toMatchObject({ total: 0, activeCount: 0 })
      expect(first.runner.context.get('mnemonProvider', false)).toBeUndefined()
    } finally {
      status.mockRestore(); search.mockRestore(); list.mockRestore()
      for (const runner of runners.reverse()) await runner.dispose()
      rmSync(directory, { recursive: true, force: true })
    }
  })
})
