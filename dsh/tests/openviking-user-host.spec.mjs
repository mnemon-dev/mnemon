import { randomUUID } from 'node:crypto'
import { afterEach, describe, expect, it } from 'vitest'
import openviking from 'dsh-mnemon-provider-openviking'
import { compositionFixture } from './fixtures/composition.ts'
import { openVikingUserKeyFixture } from '../scripts/fixtures/openviking-user-key.mjs'

const releases = []
afterEach(async () => { for (const release of releases.splice(0).reverse()) await release() })

describe('OpenViking user-key configuration in a composed Host', () => {
  it('saves two isolated instances and preserves previous settings and projections after a rejected replacement', async () => {
    const workKey = randomUUID()
    const personalKey = randomUUID()
    const identities = new Map([
      [workKey, { account: 'work', user: 'alice' }],
      [personalKey, { account: 'personal', user: 'alice' }],
    ])
    const backend = openVikingUserKeyFixture(identities)
    await new Promise(resolve => backend.server.listen(0, '127.0.0.1', resolve))
    releases.push(() => new Promise(resolve => backend.server.close(resolve)))
    const f = await compositionFixture({}, { providers: [
      { instanceId: 'work-cloud', module: openviking, config: undefined },
      { instanceId: 'personal-cloud', module: openviking, config: undefined },
    ] })
    releases.push(f.dispose)
    const spaces = f.graph.source('memory-spaces')
    const endpoint = `http://127.0.0.1:${backend.server.address().port}/openviking`
    const settings = { endpoint, account: 'work', discoveryUser: 'alice', apiKey: workKey }
    // The original setup and account-only workaround both still report the
    // backend denial rather than silently opting into a different scope.
    for (const account of ['', 'work']) await expect(spaces.mutate('provider-service-update', {
      providerId: 'work-cloud', settings: { endpoint, account, apiKey: settings.apiKey }, enabled: true,
    })).rejects.toThrow('access restrictions')
    const saved = await spaces.mutate('provider-service-update', { providerId: 'work-cloud', settings, enabled: true })
    expect(saved).toMatchObject({ configured: true, enabled: true, settings: { discoveryUser: 'alice' }, configuredSecrets: ['apiKey'] })
    expect(JSON.stringify(saved)).not.toContain(settings.apiKey)
    await spaces.mutate('provider-service-update', {
      providerId: 'personal-cloud', settings: { ...settings, account: 'personal', apiKey: personalKey }, enabled: true,
    })
    const catalog = await spaces.read('body-directory')
    const work = catalog.items.find(item => item.provider.id === 'work-cloud')
    const personal = catalog.items.find(item => item.provider.id === 'personal-cloud')
    expect(work).toBeDefined()
    expect(personal).toBeDefined()
    const receipt = await spaces.mutate('remember', { memoryBodyId: work.id, content: 'Synthetic work-only memory.', category: 'fact' })
    expect(receipt).toMatchObject({ action: 'stored' })
    expect((await spaces.read('search', { query: 'work-only', memoryBodyIds: [work.id] })).results).toHaveLength(1)
    expect((await spaces.read('search', { query: 'work-only', memoryBodyIds: [personal.id] })).results).toHaveLength(0)
    await expect(spaces.mutate('provider-service-update', { providerId: 'work-cloud', settings: { ...settings, discoveryUser: 'bob' }, enabled: true })).rejects.toThrow('cannot access')
    expect((await spaces.read('body-directory')).items.map(item => item.id)).toEqual(catalog.items.map(item => item.id))
    const services = await spaces.read('provider-services')
    expect(services.items.find(item => item.providerId === 'work-cloud').settings.discoveryUser).toBe('alice')
    expect((await spaces.read('search', { query: 'work-only', memoryBodyIds: [work.id] })).results).toHaveLength(1)
    expect(backend.requests.filter(request => request.path.includes('/admin/'))).toHaveLength(2)
    expect(backend.requests.filter(request => !request.path.includes('/admin/')).every(request => !request.identityHeaders)).toBe(true)
    expect((await f.graph.source('runtime').read('snapshot')).entries).toEqual([])
    expect(await f.graph.source('documents').read('snapshot')).toMatchObject({ activeCount: 0, archivedCount: 0 })
    await spaces.mutate('forget', { memoryBodyId: work.id, id: receipt.id })
    expect(backend.files.size).toBe(0)
  })
})
