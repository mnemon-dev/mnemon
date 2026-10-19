import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { MemoryCompositionRunner, type MemoryTestTurn } from 'dsh-mnemon/testing'
import type { MemorySpaceView, Insight } from '../src/contracts.ts'
import * as spaces from '../src/index.ts'
import { strategy } from './fixture.ts'

// Opt in with a verified release binary; never discover or mutate a personal root.
const cliPath = process.env.MNEMON_NATIVE_TEST_CLI
describe.skipIf(!cliPath)('real Native Provider through Source composition', () => {
  it('creates a space, writes via a View, recalls and forgets through the real CLI', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'mnemon-native-integration-'))
    const runner = new MemoryCompositionRunner()
    const turns: MemoryTestTurn[] = []
    try {
      await runner.mount(strategy, { instanceId: 'strategy' })
      await runner.mount(spaces, { instanceId: 'native', config: {
        dataDir: directory, cliPath, timeoutMs: 10_000,
        providers: [{ use: 'dsh-mnemon-provider-mnemon-native', instanceId: 'mnemon-native' }],
      } })
      const management = await runner.managementClient('source:native')
      const created = await management.mutate('body-create', {
        name: 'Isolated native test', description: 'Synthetic plugin integration fixture.', providerId: 'mnemon-native', active: true,
      }, { confirmed: true })
      const body = created.value as unknown as MemorySpaceView
      const turn = await runner.beginTurn()
      turns.push(turn)
      const offer = turn.view.actionOffers.find(item => item.sourceActionId === 'remember')!
      const text = 'composablenativesentinel verifies independent Source and Provider artifacts.'
      await expect(turn.executeAction(offer.id, {
        content: text, memoryBodyId: body.id, category: 'fact', source: 'user', importance: 5,
      }, () => true)).resolves.toMatchObject({ status: 'succeeded', completion: 'committed', committedAt: expect.any(String) })
      const listed = await management.read('list', { memoryBodyIds: [body.id], limit: 10 })
      const items = (listed.value as unknown as { items: Insight[] }).items
      const stored = items.find(item => item.content === text)
      expect(stored).toBeDefined()
      const route = turn.view.routes.find(item => item.sourceRouteId === 'recall')!
      const evidence = await turn.executeRoute(route.id, {
        query: 'composablenativesentinel', mode: 'keyword', memoryBodyIds: [body.id],
      })
      expect(evidence.items.some(item => item.text.includes('composablenativesentinel'))).toBe(true)
      await management.mutate('forget', { id: stored!.id, memoryBodyId: body.id }, { confirmed: true })
      const after = await management.read('list', { memoryBodyIds: [body.id], limit: 10 })
      expect((after.value as unknown as { items: Insight[] }).items).toHaveLength(0)
    } finally {
      turns.forEach(turn => turn.release())
      await runner.dispose()
      rmSync(directory, { recursive: true, force: true })
    }
  }, 30_000)
})
