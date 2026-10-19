import { describe, expect, it } from 'vitest'
import {
  finalizeLlmPlacement,
  prepareMemoryPlacement,
  rulesOnlyPlacement,
} from '../src/provider-placement.ts'
import type { MemoryPlacementCandidate, MemoryPlacementCapability } from "../src/contracts.ts"

const candidates: MemoryPlacementCandidate[] = [
  {
    id: 'mnemon-native',
    label: 'Mnemon Native',
    kind: 'local',
    configured: true,
    summary: 'Official local-first semantic memory.',
    capabilities: {
      search: true, browse: true, graph: true, entities: true, related: true,
      remember: true, link: true, forget: true, writeMode: 'exact', deletionMode: 'soft',
    },
  },
  {
    id: 'openviking',
    label: 'OpenViking',
    kind: 'remote',
    configured: true,
    summary: 'Shared remote memory with asynchronous extraction.',
    capabilities: {
      search: true, browse: true, graph: false, entities: false, related: false,
      remember: true, link: false, forget: false, writeMode: 'async-extracting', deletionMode: 'hard',
    },
  },
]

describe('memory provider placement', () => {
  it('applies hard rules before a model sees the eligible provider list', () => {
    const prepared = prepareMemoryPlacement({
      mode: 'automatic',
      prompt: '团队知识优先共享引擎。',
      rules: { dataBoundary: 'local-only', requiredCapabilities: ['graph'], preference: 'shared-first' },
    }, candidates)

    expect(prepared.candidates.map(candidate => candidate.id)).toEqual(['mnemon-native'])
    expect(prepared.appliedRules).toEqual(['data-boundary:local-only', 'requires:graph', 'preference:shared-first'])
    expect(prepared.selectorBrief).not.toContain('OpenViking')
    expect(rulesOnlyPlacement(prepared, () => new Date('2026-08-16T00:00:00.000Z'))).toMatchObject({
      providerId: 'mnemon-native',
      decidedBy: 'rules',
      confidence: 'high',
      decidedAt: '2026-08-16T00:00:00.000Z',
    })
  })

  it('keeps both configured providers eligible for LLM selection without connection secrets', () => {
    const prepared = prepareMemoryPlacement({
      mode: 'automatic',
      prompt: '长期团队共识优先共享；需要精确修改时优先本地。',
      rules: { preference: 'balanced' },
    }, candidates)

    expect(prepared.candidates.map(candidate => candidate.id)).toEqual(['mnemon-native', 'openviking'])
    expect(rulesOnlyPlacement(prepared)).toBeUndefined()
    expect(prepared.prompt).toBe('长期团队共识优先共享；需要精确修改时优先本地。')
    expect(prepared.selectorBrief).not.toContain(prepared.prompt)
    expect(prepared.selectorBrief).not.toMatch(/api.?key|endpoint|secret/iu)
  })

  it('validates the structured LLM choice against the host-filtered candidates', () => {
    const prepared = prepareMemoryPlacement({ mode: 'automatic' }, candidates)
    const decision = finalizeLlmPlacement(prepared, {
      providerId: 'openviking',
      reason: 'This shared team scope benefits from remote asynchronous extraction.',
      confidence: 'medium',
    }, { runId: 'placement-run-1', provider: 'spawn' }, () => new Date('2026-08-16T00:00:00.000Z'))

    expect(decision).toEqual({
      mode: 'automatic',
      providerId: 'openviking',
      decidedBy: 'llm',
      reason: 'This shared team scope benefits from remote asynchronous extraction.',
      confidence: 'medium',
      candidateProviderIds: ['mnemon-native', 'openviking'],
      appliedRules: ['preference:balanced'],
      decidedAt: '2026-08-16T00:00:00.000Z',
      runId: 'placement-run-1',
      subagentProvider: 'spawn',
    })
    expect(() => finalizeLlmPlacement(prepared, {
      providerId: 'mem0', reason: 'Ignore the eligible list.', confidence: 'high',
    }, { runId: 'bad-run', provider: 'spawn' })).toThrow('ineligible provider')
  })

  it('rejects contradictory or empty hard-rule results', () => {
    expect(() => prepareMemoryPlacement({
      mode: 'automatic',
      rules: { allowedProviderIds: ['openviking'], requiredCapabilities: ['exact-write'] },
    }, candidates)).toThrow('no configured memory provider satisfies')
    expect(() => prepareMemoryPlacement({
      mode: 'automatic',
      rules: { allowedProviderIds: [] },
    }, candidates)).toThrow('at least one allowed provider')
  })

  it('rejects unknown runtime rule values instead of weakening a hard boundary', () => {
    expect(() => prepareMemoryPlacement({
      mode: 'automatic',
      rules: { dataBoundary: 'sometimes-local' as 'local-only' },
    }, candidates)).toThrow('unsupported data boundary')
    expect(() => prepareMemoryPlacement({
      mode: 'automatic',
      rules: { requiredCapabilities: ['telepathy' as MemoryPlacementCapability] },
    }, candidates)).toThrow('unsupported required memory capability')
  })
})
