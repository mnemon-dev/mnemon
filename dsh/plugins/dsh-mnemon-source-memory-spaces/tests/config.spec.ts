import { describe, expect, it } from 'vitest'
import { resolveMemorySpacesConfig as resolveConfig } from '../src/config.ts'

describe('Memory Spaces configuration rules', () => {
  it('validates configurable recall quality policy thresholds and expansion', () => {
    expect(resolveConfig({
      recallQuality: { policy: 'team-v2', lowScoreThreshold: 0.2, highScoreThreshold: 0.7, candidateMultiplier: 2, maxMediumResults: 5, maxUnknownResults: 1 },
    }).recallQuality).toEqual({ policy: 'team-v2', lowScoreThreshold: 0.2, highScoreThreshold: 0.7, candidateMultiplier: 2, maxMediumResults: 5, maxUnknownResults: 1 })
    expect(() => resolveConfig({ recallQuality: { lowScoreThreshold: 0.7, highScoreThreshold: 0.6 } })).toThrow('less than')
    expect(() => resolveConfig({ recallQuality: { candidateMultiplier: 1.5 } })).toThrow('integer')
    expect(() => resolveConfig({ recallQuality: { policy: '../unsafe' } })).toThrow('policy id')
    expect(() => resolveConfig({ recallQuality: { maxMediumResults: 51 } })).toThrow('max medium')
    expect(() => resolveConfig({ recallQuality: { maxUnknownResults: -1 } })).toThrow('max unknown')
  })

  it('normalizes and validates DSH-managed Mnemon embedding settings', () => {
    expect(resolveConfig({
      embedding: { enabled: true, endpoint: ' https://ollama.example.test/prefix/// ', model: ' qwen3-embedding:0.6b ', apiKey: '  sk-secret  ' },
    }).embedding).toEqual({
      enabled: true,
      endpoint: 'https://ollama.example.test/prefix',
      model: 'qwen3-embedding:0.6b',
      apiKey: 'sk-secret',
      protocol: 'auto',
    })
    expect(resolveConfig({}).embedding).toMatchObject({ enabled: false, endpoint: 'http://localhost:11434', model: 'nomic-embed-text', apiKey: '', protocol: 'auto' })
    expect(() => resolveConfig({ embedding: { enabled: true, endpoint: 'ftp://ollama.example.test' } })).toThrow('HTTP or HTTPS')
    expect(() => resolveConfig({ embedding: { enabled: true, endpoint: 'http://user:secret@localhost:11434' } })).toThrow('without credentials')
    expect(() => resolveConfig({ embedding: { enabled: true, endpoint: 'http://localhost:11434?tenant=one' } })).toThrow('without credentials')
    expect(() => resolveConfig({ embedding: { enabled: true, endpoint: 'http://localhost:11434?' } })).toThrow('without credentials')
    expect(() => resolveConfig({ embedding: { enabled: true, endpoint: 'http://localhost:11434#' } })).toThrow('without credentials')
    expect(() => resolveConfig({ embedding: { enabled: true, model: 'bad\nmodel' } })).toThrow('control characters')
    expect(() => resolveConfig({ embedding: { enabled: true, apiKey: 'bad\nkey' } })).toThrow('without control characters')
    expect(() => resolveConfig({ embedding: { enabled: true, apiKey: 'k'.repeat(2049) } })).toThrow('0..2048')
    expect(() => resolveConfig({ embedding: { enabled: true, protocol: 'grpc' as never } })).toThrow('unsupported embedding protocol')
  })

  it('resolves a bounded automatic persistence strategy without changing its provider connections', () => {
    expect(resolveConfig({
      persistenceStrategy: {
        mode: 'automatic',
        prompt: 'Prefer shared project memory.',
        rules: {
          allowedProviderIds: ['mnemon-native', 'openviking', 'openviking'],
          dataBoundary: 'allow-remote',
          requiredCapabilities: ['graph'],
          preference: 'shared-first',
        },
        providerConnections: { openviking: { targetUri: 'viking://resources/team' } },
      },
    }).persistenceStrategy).toEqual({
      mode: 'automatic',
      providerId: 'mnemon-native',
      providerDefaulted: true,
      prompt: 'Prefer shared project memory.',
      rules: {
        allowedProviderIds: ['mnemon-native', 'openviking'],
        dataBoundary: 'allow-remote',
        requiredCapabilities: ['graph'],
        preference: 'shared-first',
      },
      providerConnections: { openviking: { targetUri: 'viking://resources/team' } },
    })
  })

  it('marks only an unchosen persistence provider as the default', () => {
    expect(resolveConfig({}).persistenceStrategy).toMatchObject({ mode: 'manual', providerId: 'mnemon-native', providerDefaulted: true })
    expect(resolveConfig({ persistenceStrategy: { providerId: 'mnemon-native' } }).persistenceStrategy).not.toHaveProperty('providerDefaulted')
  })
})
