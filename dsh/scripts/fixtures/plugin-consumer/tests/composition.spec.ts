import { mkdtempSync, readFileSync, readdirSync, rmSync } from 'node:fs'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import type { Context } from '@deepseek-ai/cordis'
import { describe, expect, it } from 'vitest'
import { MemoryCompositionRunner, type MemoryTestTurn } from 'dsh-mnemon/testing'
import * as spaces from 'dsh-mnemon-source-memory-spaces'
import * as runtime from 'dsh-mnemon-source-runtime'
import * as threeTier from 'dsh-mnemon-strategy-default-three-tier'
import * as scoped from 'dsh-mnemon-strategy-scoped'
import * as light from 'dsh-mnemon-strategy-light-context'
import * as capture from 'dsh-mnemon-strategy-auto-capture'
import * as general from 'dsh-mnemon-strategy-general'
import * as notes from '../lib/external-source.js'
import * as focus from '../lib/external-strategy.js'
import * as externalBudget from '../lib/external-strategy-extension.js'
import provider from '../lib/external-provider.js'

describe('external consumer of packed artifacts', () => {
  it('composes three optional plugins and an independently authored replacement slot using only packed SDKs', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'external-additive-strategies-'))
    const runner = new MemoryCompositionRunner({ strategyTypeId: 'default-three-tier' })
    const turns: MemoryTestTurn[] = []
    try {
      await runner.mount(threeTier, { instanceId: 'default' })
      // Extensions may be enabled before the Sources they will compose.
      await runner.mount(scoped, { instanceId: 'scoped' })
      const removeLight = await runner.mount(light, { instanceId: 'light', config: { maxProjectionCharacters: 200 } })
      await runner.mount(capture, { instanceId: 'capture' })
      for (const id of ['global', 'project']) {
        await runner.mount(runtime, { instanceId: id, config: { dataDir: join(directory, id) } })
      }
      expect(runner.inspect().evaluation).toMatchObject({ state: 'rejected', diagnostics: [{ message: expect.stringContaining('source.durable-evidence') }] })
      await runner.mount(spaces, { instanceId: 'spaces', config: { dataDir: join(directory, 'spaces'),
        providers: [{ use: 'dsh-mnemon-provider-holographic', instanceId: 'local-account' }] } })
      expect(runner.inspect().evaluation.state).toBe('ready')
      for (const id of ['global', 'project']) {
        await (await runner.managementClient('source:' + id)).mutate('mutate',
          { action: 'add', target: 'memory', content: (id + ' source sentinel. ').repeat(100) }, { confirmed: true })
      }
      const management = await runner.managementClient('source:spaces')
      await management.mutate('provider-service-update', { providerId: 'local-account', settings: {}, enabled: true }, { confirmed: true })
      const first = await runner.beginTurn()
      turns.push(first)
      expect(first.view.strategyTypeId).toBe('default-three-tier')
      expect(first.view.strategyExtensions?.map(item => item.slot).sort()).toEqual(['capture', 'projection', 'selection'])
      expect(first.view.projection.reduce((sum, item) => sum + item.text.length, 0)).toBeLessThanOrEqual(200)
      expect(first.view.projection.filter(item => item.mode === 'eager')).toHaveLength(2)
      expect(first.view.guidance?.system).toContain('MNEMON OPTIONAL AUTO CAPTURE')
      const offer = first.view.actionOffers.find(offer => offer.sourceActionId === 'remember')!
      await expect(first.executeAction(offer.id, { content: 'Packed additive sentinel.' }, () => false)).rejects.toThrow('not currently authorized')
      await expect(first.executeAction(offer.id, { content: 'Packed additive sentinel.' }, () => true)).resolves.toMatchObject({ completion: 'committed' })
      await expect(runner.mount(externalBudget, { instanceId: 'external-budget' })).rejects.toThrow('slot conflict')
      await removeLight()
      await runner.mount(externalBudget, { instanceId: 'external-budget' })
      const next = await runner.beginTurn()
      turns.push(next)
      expect(next.view.strategyExtensions?.map(item => item.typeId).sort()).toEqual(['auto-capture', 'external-budget', 'scoped'])
      const characters = next.view.projection.reduce((sum, item) => sum + item.text.length, 0)
      expect(characters).toBeGreaterThan(200)
      expect(characters).toBeLessThanOrEqual(1_200)
      const route = next.view.routes.find(route => route.sourceRouteId === 'recall')!
      expect((await next.executeRoute(route.id, { query: 'Packed additive sentinel' })).items[0]?.text).toContain('Packed additive sentinel')
      expect(first.view.strategyExtensions?.some(item => item.typeId === 'light-context')).toBe(true)
    } finally { turns.forEach(turn => turn.release()); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('applies a packed enhancement to the packed general main Strategy', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'external-general-strategy-'))
    const runner = new MemoryCompositionRunner({ strategyTypeId: 'general' })
    const turns: MemoryTestTurn[] = []
    try {
      await runner.mount(general, { instanceId: 'general' })
      await runner.mount(light, { instanceId: 'light', config: { maxProjectionCharacters: 200 } })
      await runner.mount(runtime, { instanceId: 'global', config: { dataDir: join(directory, 'global') } })
      await (await runner.managementClient('source:global')).mutate('mutate',
        { action: 'add', target: 'memory', content: 'General sentinel. '.repeat(100) }, { confirmed: true })
      const turn = await runner.beginTurn()
      turns.push(turn)
      expect(turn.view.strategyTypeId).toBe('general')
      expect(turn.view.strategyExtensions?.map(item => item.typeId)).toEqual(['light-context'])
      expect(turn.view.guidance?.system).toContain('MNEMON GENERAL MEMORY PROTOCOL')
      expect(turn.view.projection.filter(item => item.mode === 'eager').map(item => item.sourceInstanceKey)).toEqual(['source:global'])
      expect(turn.view.projection.reduce((sum, item) => sum + item.text.length, 0)).toBeLessThanOrEqual(200)
    } finally { turns.forEach(turn => turn.release()); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('imports every declared Node entry from installed packages, not repository sources', async () => {
    const names: string[] = JSON.parse(readFileSync(new URL('../artifacts.json', import.meta.url), 'utf8'))
    const require = createRequire(import.meta.url)
    expect(names).toHaveLength(18)
    for (const name of names) {
      const manifest = JSON.parse(readFileSync(require.resolve(name + '/package.json'), 'utf8'))
      for (const subpath of Object.keys(manifest.exports)) {
        if (subpath === './client' || subpath === './package.json') continue
        const specifier = name + (subpath === '.' ? '' : subpath.slice(1))
        if (subpath === './locale/*.json') {
          const directory = join(dirname(require.resolve(name + '/package.json')), 'locale')
          const files = readdirSync(directory).filter(file => file.endsWith('.json'))
          expect(files).toContain('en.json')
          for (const file of files) {
            const resolved = require.resolve(name + '/locale/' + file)
            expect(resolved).toBe(join(directory, file))
            expect(JSON.parse(readFileSync(resolved, 'utf8'))).toEqual(expect.any(Object))
          }
          continue
        }
        if (subpath.startsWith('./presentation/')) {
          const asset = readFileSync(require.resolve(specifier), 'utf8')
          if (subpath.endsWith('.json')) expect(Object.keys(JSON.parse(asset)).sort()).toEqual(['en', 'zh'])
          else expect(asset.trim()).not.toBe('')
          continue
        }
        expect(Object.keys(await import(specifier)).length, specifier).toBeGreaterThan(0)
      }
    }
  })

  it('ships Chinese plugin metadata and keeps manifest fallback for English', () => {
    const require = createRequire(import.meta.url)
    const english = JSON.parse(readFileSync(require.resolve('dsh-mnemon/locale/en.json'), 'utf8'))
    const chinese = JSON.parse(readFileSync(require.resolve('dsh-mnemon/locale/zh.json'), 'utf8'))
    const manifest = JSON.parse(readFileSync(require.resolve('dsh-mnemon/package.json'), 'utf8'))
    expect(english.meta).toEqual({})
    expect(manifest.name).toBe('dsh-mnemon')
    expect(manifest.description).toMatch(/Composable, view-based memory/)
    expect(chinese.meta.title).toBe('可组合记忆 (dsh-mnemon)')
    expect(chinese.meta.description).toMatch(/可组合视图记忆/)
  })

  it('supports a new Source and Strategy, authority checks, exact grants and explicit replacement', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'external-notes-'))
    const runner = new MemoryCompositionRunner()
    const turns: MemoryTestTurn[] = []
    try {
      await expect(runner.mount(notes, { instanceId: 'invalid', config: { path: 'relative.txt' } })).rejects.toThrow('absolute')
      expect(runner.inspect().evaluation.sourceInstanceKeys).toHaveLength(0)
      for (const id of ['work', 'personal']) await runner.mount(notes, { instanceId: id, config: { path: join(directory, id + '.txt') } })
      await expect(runner.beginTurn()).rejects.toThrow('no Serving')
      const removeStrategy = await runner.mount(focus, { instanceId: 'focus', config: { sourceKeys: ['source:work'], mode: 'eager' } })
      const management = await runner.managementClient('source:work')
      const original = management.revision
      await management.mutate('replace', { content: 'first snapshot' }, { confirmed: true })
      await expect(management.mutate('replace', { content: 'stale' }, { confirmed: true, expectedRevision: original })).rejects.toThrow('revision conflict')
      expect((await (await runner.managementClient('source:personal')).read('read')).value).toEqual({ content: '' })

      const pinned = await runner.beginTurn()
      turns.push(pinned)
      expect(pinned.view.projection.map(fragment => fragment.text)).toEqual(['first snapshot'])
      const route = pinned.view.routes[0]!
      const action = pinned.view.actionOffers[0]!
      await expect(pinned.executeAction(action.id, { content: 'denied' }, () => false)).rejects.toThrow('not currently authorized')
      await expect(pinned.executeAction(action.id, { content: 'second snapshot' }, () => true)).resolves.toMatchObject({ status: 'succeeded' })
      const exact = await pinned.executeRoute(route.id, {})
      expect(exact.items[0]?.text).toBe('first snapshot')
      expect((await management.read('read')).value).toEqual({ content: 'second snapshot' })

      // A competing candidate cannot silently replace the serving Strategy.
      const removeCandidate = await runner.mount(focus, { instanceId: 'candidate', config: { sourceKeys: ['source:personal'], mode: 'routed' } })
      expect(runner.inspect().evaluation.state).toBe('rejected')
      expect(runner.inspect().servingGenerationId).toBe(pinned.view.runtimeGeneration)
      await removeCandidate()
      await removeStrategy()
      await expect(runner.beginTurn()).rejects.toThrow('no Serving')
      await runner.mount(focus, { instanceId: 'focus', config: { sourceKeys: ['source:personal'], mode: 'routed' } })
      const current = await runner.beginTurn()
      turns.push(current)
      expect(current.view.runtimeGeneration).not.toBe(pinned.view.runtimeGeneration)
      expect(current.view.routes.map(route => route.sourceInstanceKey)).toEqual(['source:personal'])
      expect(current.view.projection[0]?.mode).toBe('routed')
      expect(runner.inspect().drainingGenerationIds).toContain(pinned.view.runtimeGeneration)
      expect((await pinned.executeRoute(route.id, {})).items[0]?.text).toBe('first snapshot')
      current.release()
      pinned.release()
      expect(runner.inspect().drainingGenerationIds).not.toContain(pinned.view.runtimeGeneration)
    } finally { turns.forEach(turn => turn.release()); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('loads a packed Provider through the Source loader and performs durable read/write', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'external-provider-loader-'))
    const runner = new MemoryCompositionRunner()
    const turns: MemoryTestTurn[] = []
    try {
      await runner.mount(focus, { instanceId: 'focus', config: { sourceKeys: ['source:spaces'], mode: 'routed' } })
      await runner.mount(spaces, { instanceId: 'spaces', config: { dataDir: directory,
        providers: [{ use: 'dsh-mnemon-provider-holographic', instanceId: 'local-account' }] } })
      const management = await runner.managementClient('source:spaces')
      await management.mutate('provider-service-update', { providerId: 'local-account', settings: {}, enabled: true }, { confirmed: true })
      const turn = await runner.beginTurn()
      turns.push(turn)
      await expect(turn.executeAction(turn.view.actionOffers.find(offer => offer.sourceActionId === 'remember')!.id,
        { content: 'artifact loader durable sentinel' }, () => true)).resolves.toMatchObject({ status: 'succeeded' })
      turn.release()
      const next = await runner.beginTurn()
      turns.push(next)
      expect((await next.executeRoute(next.view.routes.find(route => route.sourceRouteId === 'recall')!.id, { query: 'durable sentinel' })).items[0]?.text).toContain('artifact loader durable sentinel')
      next.release()
    } finally { turns.forEach(turn => turn.release()); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('supports an external Provider in two Source-private trees with identical child ids', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'external-provider-trees-'))
    const runner = new MemoryCompositionRunner()
    const turns: MemoryTestTurn[] = []
    try {
      await runner.mount(focus, { instanceId: 'focus', config: { sourceKeys: ['source:work', 'source:personal'], mode: 'routed' } })
      for (const id of ['work', 'personal']) await runner.mount({ inject: ['mnemonMemory'], async apply(ctx: Context) {
        await spaces.installMemorySpaces(ctx, [{ instanceId: 'same-child-id', module: provider, config: undefined }], { config: { dataDir: join(directory, id) } })
      } }, { instanceId: id })
      for (const id of ['work', 'personal']) {
        const management = await runner.managementClient('source:' + id)
        await management.mutate('provider-service-update', { providerId: 'same-child-id', settings: {}, enabled: true }, { confirmed: true })
      }
      const turn = await runner.beginTurn()
      turns.push(turn)
      const offer = turn.view.actionOffers.find(offer => offer.sourceInstanceKey === 'source:work' && offer.sourceActionId === 'remember')!
      await turn.executeAction(offer.id, { content: 'work only' }, () => true)
      turn.release()
      const next = await runner.beginTurn()
      turns.push(next)
      for (const route of next.view.routes.filter(route => route.sourceRouteId === 'recall')) {
        const result = await next.executeRoute(route.id, { query: 'work' })
        expect(result.items).toHaveLength(route.sourceInstanceKey === 'source:work' ? 1 : 0)
      }
      expect(runner.context.get('mnemonMemorySpace', false)).toBeUndefined()
      expect(runner.context.get('mnemonProvider', false)).toBeUndefined()
      next.release()
    } finally { turns.forEach(turn => turn.release()); await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })
})
