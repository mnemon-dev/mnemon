import { Context } from '@deepseek-ai/cordis'
import { createVolatile, isVolatile } from '@deepseek-ai/cosmokit'
import z from '@deepseek-ai/schemastery'
import { describe, expect, it, vi } from 'vitest'
import { Config as PlainConfig } from '../src/host/config.ts'
import { LiveConfig, plainHostConfig } from '../src/host/live-config.ts'

describe('live Host configuration', () => {
  it('keeps form fields inspectable and leaves transport authority outside live edits', () => {
    const schema = new z(JSON.parse(JSON.stringify(LiveConfig.toJSON())))
    expect(schema.type).toBe('object')
    expect(Object.keys(schema.dict ?? {})).toEqual(Object.keys(PlainConfig.dict ?? {}))
    expect(schema.dict?.remoteAccess?.meta.volatile).not.toBe(true)
    expect(schema.dict?.displayMode?.meta.volatile).toBe(true)
    expect(schema.dict?.conversationInteraction?.meta.volatile).toBe(true)
    expect(schema.dict?.memoryTopology?.meta.volatile).toBe(true)
    expect(JSON.stringify(LiveConfig.toJSON())).not.toContain('validate(value')
    expect(PlainConfig.dict?.displayMode?.meta).not.toHaveProperty('volatile')
  })

  it('activates through the published Cordis validator and persists plain values', async () => {
    const root = new Context()
    let received: unknown
    try {
      const fiber = await root.plugin({
        Config: LiveConfig,
        apply(_ctx: Context, config: unknown) { received = config },
      }, { displayMode: 'builtin', remoteAccess: 'trusted-host' })
      const config = received as ReturnType<typeof LiveConfig>
      expect(isVolatile(config.displayMode)).toBe(true)
      if (!isVolatile(config.displayMode)) throw new Error('The live schema must provide references on this host')
      expect(config.displayMode.get()).toBe('builtin')
      expect(isVolatile(config.remoteAccess)).toBe(false)
      expect(config.remoteAccess).toBe('trusted-host')
      expect(LiveConfig.simplify(fiber.config)).toEqual({ displayMode: 'builtin', remoteAccess: 'trusted-host' })
    } finally {
      await root.fiber.dispose()
    }
  })

  it.each([
    [{ storageScope: 'custom' }, 'a custom dataDir is required'],
    [{ taskAgentModel: { mode: 'fixed', provider: 'example' } }, 'provider and model'],
    [{ customPackId: 'missing' }, 'unknown custom Pack'],
  ])('rejects cross-field-invalid profile input before returning references: %j', async (input, message) => {
    const result = await LiveConfig['~standard'].validate(input)
    expect(result.issues?.some(issue => issue.message.includes(String(message)))).toBe(true)
  })

  it('retains schema errors and accepts a valid cross-field configuration', async () => {
    const invalid = await LiveConfig['~standard'].validate({ defaultRecallLimit: 0 })
    expect(invalid.issues?.length).toBeGreaterThan(0)
    const valid = await LiveConfig['~standard'].validate({
      taskAgentModel: { mode: 'fixed', provider: 'example', model: 'fixture' },
      displayMode: 'buildin',
    })
    expect(valid.issues).toBeUndefined()
    if (valid.issues) throw new Error('valid configuration was rejected')
    expect(plainHostConfig(valid.value)).toMatchObject({
      taskAgentModel: { mode: 'fixed', provider: 'example', model: 'fixture' },
      displayMode: 'buildin',
    })
  })

  it('detaches plain values and nested reference snapshots without invoking lookalike getters', () => {
    const legacy = { conversationInteraction: { turnBar: false }, customPacks: [{ id: 'work', name: 'Work', dataDir: '/fixture' }] }
    const detached = plainHostConfig(legacy)
    expect(detached).toEqual(legacy)
    expect(detached.conversationInteraction).not.toBe(legacy.conversationInteraction)
    expect(detached.customPacks?.[0]).not.toBe(legacy.customPacks[0])

    const snapshot = createVolatile({ turnBar: false, saveAction: true })
    const expanded = plainHostConfig({ conversationInteraction: snapshot })
    expanded.conversationInteraction!.turnBar = true
    expect(snapshot.get().turnBar).toBe(false)
    expect(plainHostConfig(createVolatile(legacy))).toEqual(legacy)

    const get = vi.fn(() => 'must not run')
    expect(plainHostConfig({ taskAgentModel: { get } })).toEqual({ taskAgentModel: { get } })
    expect(get).not.toHaveBeenCalled()
  })
})
