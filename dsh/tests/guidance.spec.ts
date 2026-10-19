import { describe, expect, it, vi } from 'vitest'
import { renderContextSnapshot } from '@deepseek-ai/dsh-system-prompt'
import type { HostContextShape } from '../src/host/dsh.ts'
import { applyMemoryViewGuidance, withoutMemoryViewContext, registerGuidance, RUNTIME_MEMORY_CONTEXT_NAME } from '../src/host/guidance.ts'
import type { ComposableMemoryView } from '../src/core/contracts/index.ts'

describe('Source-neutral View guidance', () => {
  it('uses pinned Strategy instructions and removes stale default instructions on a Strategy switch', () => {
    const assembly = {
      sections: [{ name: 'host', text: 'Host protocol' }, { name: 'mnemon:routing', text: 'stale routing' },
        { name: 'mnemon:strategy', text: 'stale strategy' }, { name: 'mnemon:runtime-memory-protocol', text: 'stale legacy' }],
      contexts: [{ name: RUNTIME_MEMORY_CONTEXT_NAME, text: 'stale context' }],
    }
    const selected = { guidance: { system: 'Trusted instruction {{model}}', routing: 'Selected routing' } } as ComposableMemoryView
    const result = applyMemoryViewGuidance(assembly, selected)
    expect(result.sections).toEqual([{ name: 'host', text: 'Host protocol' },
      { name: 'mnemon:strategy', text: 'Trusted instruction {{model}}' }, { name: 'mnemon:routing', text: 'Selected routing' }])
    expect(result.contexts).toEqual([])
    expect(assembly.sections).toHaveLength(4)
    const custom = applyMemoryViewGuidance(result, {} as ComposableMemoryView, false)
    expect(custom.sections).toEqual([{ name: 'host', text: 'Host protocol' }])
  })

  it('registers only View-based guidance and respects the existing preference', () => {
    const section = vi.fn()
    const prompted = { get: (name: string) => name === 'systemPrompt' ? { section } : undefined }
    const inject = vi.fn((_services: string[], callback: (ctx: unknown) => void) => callback(prompted))
    const ctx = { inject } as unknown as HostContextShape
    const preference = { routingGuidance: true }
    registerGuidance(ctx, preference)
    expect(inject).toHaveBeenCalledWith(['systemPrompt'], expect.any(Function))
    const render = section.mock.calls[0]![0].text
    expect(render()).toContain('only for offered ids')
    expect(render()).toContain('An action offer is not authorization')
    expect(render()).not.toMatch(/Documents|Memory Spaces|mnemon_recall|mnemon_runtime_memory/)
    preference.routingGuidance = false
    expect(render()).toBe('')
  })

  it('keeps View snapshots out of shared context while leaving other contributors unchanged', () => {
    const other = { name: 'other-plugin', text: 'Stable context {{model}}.' }
    const assembly = {
      sections: [{ name: 'host', text: 'Host protocol' }],
      contexts: [{ name: RUNTIME_MEMORY_CONTEXT_NAME, text: 'Inherited stale View' }, other],
      tools: [], variables: { model: 'deepseek' },
    }
    const result = withoutMemoryViewContext(assembly)
    expect(result.sections).toBe(assembly.sections)
    expect(result.contexts).toEqual([other])
    expect(result.contexts[0]).toBe(other)
    expect(renderContextSnapshot(result)).toContain('Stable context deepseek.')
    expect(assembly.contexts).toHaveLength(2)
    expect(withoutMemoryViewContext({ contexts: [] })).toEqual({ contexts: [] })
  })
})
