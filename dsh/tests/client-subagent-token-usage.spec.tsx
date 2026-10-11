import type { ReactElement } from 'react'
import { describe, expect, it, vi } from 'vitest'
import type { SnapshotSelectorHook } from '@deepseek-ai/dsh-client-store'
import type { StoredEntry } from '@deepseek-ai/dsh-client-ui-slots'
import type { MnemonSessionListState } from "../src/client/dsh-context.ts"
import {
  createScopedUseSessions,
  mountSubagentTokenUsageOverride,
  scopeSubagentTokenUsage,
} from '../src/client/subagent-token-usage.tsx'

const generic = {
  uncachedInputTokens: 1_000,
  outputTokens: 100,
  cacheReadTokens: 200,
  cacheWriteTokens: 50,
}

const child = {
  uncachedInputTokens: 12,
  outputTokens: 5,
  cacheReadTokens: 3,
  cacheWriteTokens: 4,
}

const PARENT = 'parent'
const CHILD = 'child'

function sessions(): MnemonSessionListState {
  return {
    ids: [PARENT, CHILD],
    current: PARENT,
    phase: 'ready',
    subagentsByParent: {},
    byId: {
      [PARENT]: {
        id: PARENT, displayTitle: 'Parent', running: false, blank: false, updatedAt: 1,
        projectionValues: { tokenUsage: generic },
      },
      [CHILD]: {
        id: CHILD, displayTitle: 'Child', origin: 'subagent', parentId: PARENT,
        running: false, blank: false, updatedAt: 1,
        projectionValues: { tokenUsage: generic, mnemonSubagentTokenUsage: child },
      },
    },
  } as MnemonSessionListState
}

function tokenUsage(state: MnemonSessionListState, id: string): unknown {
  return (state.byId[id]?.projectionValues as Readonly<Record<string, unknown>> | undefined)?.tokenUsage
}

describe('Mnemon subagent catalog token override', () => {
  it('rewrites only subagent rows and caches the transformed snapshot', () => {
    const state = sessions()
    const scoped = scopeSubagentTokenUsage(state)

    expect(scoped).not.toBe(state)
    expect(scopeSubagentTokenUsage(state)).toBe(scoped)
    expect(scoped.byId[PARENT]).toBe(state.byId[PARENT])
    expect(tokenUsage(scoped, CHILD)).toEqual(child)
    expect(tokenUsage(state, CHILD)).toEqual(generic)
  })

  it('keeps the official aggregate when a legacy child has no scoped projection', () => {
    const state = sessions()
    state.byId[CHILD] = {
      ...state.byId[CHILD]!,
      projectionValues: { tokenUsage: generic, mnemonSubagentTokenUsage: null } as never,
    }

    expect(scopeSubagentTokenUsage(state)).toBe(state)
    expect(tokenUsage(state, CHILD)).toEqual(generic)
  })

  it('preserves the selector hook contract while presenting child-local usage', () => {
    const state = sessions()
    const base: SnapshotSelectorHook<MnemonSessionListState> = selector => selector(state)
    const useSessions = createScopedUseSessions(base)

    expect(useSessions(value => tokenUsage(value, CHILD))).toEqual(child)
    expect(useSessions(value => tokenUsage(value, PARENT))).toEqual(generic)
  })

  it('shadows the official lineage component and reuses its actions and locale', () => {
    const entries: StoredEntry[] = []
    let changed = (): void => {}
    let shadow: ((props: Record<string, unknown>) => ReactElement) | undefined
    const disposeShadow = vi.fn()
    const unsubscribe = vi.fn()
    const register = vi.fn((options: Record<string, unknown>, component: unknown) => {
      shadow = component as typeof shadow
      return disposeShadow
    })
    const slots = {
      entries: vi.fn(() => entries),
      subscribe: vi.fn((_key: string, listener: () => void) => {
        changed = listener
        return unsubscribe
      }),
      register,
    }
    const dispose = mountSubagentTokenUsageOverride({ slots } as never)
    const actions = vi.fn(() => ({ openChild: vi.fn(), refresh: vi.fn(), setCatalogOpen: vi.fn() }))
    const official = vi.fn(() => null)
    const officialEntry: StoredEntry = {
      component: official,
      options: {},
      locale: 'subagent',
      inject: actions as never,
    }
    entries.push(officialEntry)
    changed()

    expect(register).toHaveBeenCalledWith(expect.objectContaining({
      name: 'conversation.session.header.lineage',
      priority: -100,
      locale: 'subagent',
      inject: actions,
    }), expect.any(Function))

    const state = sessions()
    const useSessions: SnapshotSelectorHook<MnemonSessionListState> = selector => selector(state)
    const element = shadow!({ useSessions })
    expect(element.type).toBe(official)
    expect((element.props as { useSessions: SnapshotSelectorHook<MnemonSessionListState> }).useSessions(
      value => tokenUsage(value, CHILD),
    )).toEqual(child)

    entries.splice(0)
    changed()
    expect(disposeShadow).toHaveBeenCalledTimes(1)
    dispose()
    expect(unsubscribe).toHaveBeenCalledTimes(1)
  })
})
