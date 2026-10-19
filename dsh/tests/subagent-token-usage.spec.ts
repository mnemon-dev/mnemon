import { describe, expect, it, vi } from 'vitest'
import type { HostSessionEvent } from "../src/host/dsh.ts"
import {
  mnemonSubagentTokenUsageProjectionDefinition as projection,
  registerMnemonSubagentTokenUsageProjection,
} from "../src/host/subagent-token-usage.ts"

function event(type: string, data: Record<string, unknown> = {}): HostSessionEvent {
  return { type, data }
}

function usage(
  kind: 'chunk' | 'message',
  turn: number,
  step: number,
  inputTokens: number,
  outputTokens: number,
  cacheReadTokens = 0,
  cacheWriteTokens = 0,
): HostSessionEvent {
  const value = { inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens }
  return kind === 'chunk'
    ? event('assistant/chunk', { turn, step, chunk: { type: 'usage', usage: value } })
    : event('assistant/message', { turn, step, usage: value })
}

describe('Mnemon subagent token-usage projection', () => {
  it('ignores a fork seed and totals only usage after the child descriptor', () => {
    let state = projection.init()
    state = projection.apply(state, usage('message', 0, 0, 1_000, 100, 200, 50))
    expect(projection.wire.view(state)).toBeNull()

    state = projection.apply(state, event('subagent/descriptor'))
    state = projection.apply(state, usage('chunk', 1, 0, 10, 2, 3, 4))
    state = projection.apply(state, usage('message', 1, 0, 12, 5, 3, 4))
    state = projection.apply(state, usage('message', 1, 1, 7, 1, 2, 0))

    expect(projection.wire.view(state)).toEqual({
      uncachedInputTokens: 19,
      outputTokens: 6,
      cacheReadTokens: 5,
      cacheWriteTokens: 4,
    })
  })

  it('replaces same-step samples and resets again at a later descriptor', () => {
    let state = projection.apply(projection.init(), event('subagent/descriptor'))
    state = projection.apply(state, usage('chunk', 1, 0, 8, 1))
    const duplicate = projection.apply(state, usage('chunk', 1, 0, 8, 1))
    expect(duplicate).toBe(state)

    state = projection.apply(state, event('subagent/descriptor'))
    expect(projection.wire.view(state)).toEqual({
      uncachedInputTokens: 0,
      outputTokens: 0,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
    })
  })

  it('registers through the optional projection service without making it a core dependency', () => {
    const register = vi.fn()
    const inject = vi.fn((_services: string[], callback: (ctx: unknown) => void) => {
      callback({ sessionProjections: { register } })
    })
    registerMnemonSubagentTokenUsageProjection({ inject } as never)

    expect(inject).toHaveBeenCalledWith(['sessionProjections'], expect.any(Function))
    expect(register).toHaveBeenCalledWith(projection)
  })

  it.each([null, undefined, [], 'invalid', { turn: 1, step: 0, chunk: null }])(
    'ignores malformed event data without changing child usage: %j',
    (data) => {
      const state = projection.apply(projection.init(), event('subagent/descriptor'))
      expect(projection.apply(state, { type: 'assistant/chunk', data })).toBe(state)
    },
  )
})
