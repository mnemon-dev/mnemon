import { Context } from '@deepseek-ai/cordis'
import { describe, expect, it } from 'vitest'
import { PrivateMemorySpaceProviderHost } from '../src/providers/host.ts'
import { createMemorySpaceProviderPlugin } from '../src/providers/plugin.ts'
import { providerEntries } from './providers.ts'

describe('Memory Spaces private Provider Fibers', () => {
  it('registers a Provider through a child Fiber closure and removes it with that Fiber', async () => {
    const ctx = new Context()
    const privateHost = new PrivateMemorySpaceProviderHost('memory-spaces-first')
    const entry = providerEntries.find(candidate => candidate.instanceId === 'openviking')!
    const fiber = ctx.plugin(createMemorySpaceProviderPlugin(entry, privateHost), entry.config)
    await fiber.await()

    expect(privateHost.snapshot().entries.map(candidate => candidate.instanceId)).toEqual(['openviking'])
    expect(ctx.get('mnemonProvider', false)).toBeUndefined()

    await fiber.dispose()
    expect(privateHost.snapshot().entries).toEqual([])
  })

  it('gives each Memory Spaces parent an isolated Provider host', async () => {
    const ctx = new Context()
    const first = new PrivateMemorySpaceProviderHost('memory-spaces-first')
    const second = new PrivateMemorySpaceProviderHost('memory-spaces-second')
    const entry = providerEntries.find(candidate => candidate.instanceId === 'honcho')!
    const fiber = ctx.plugin(createMemorySpaceProviderPlugin(entry, first), entry.config)
    await fiber.await()

    expect(first.snapshot().entries.map(candidate => candidate.instanceId)).toEqual(['honcho'])
    expect(second.snapshot().entries).toEqual([])
    await fiber.dispose()
  })
})
