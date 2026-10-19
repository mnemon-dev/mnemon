/** Mnemon token-usage projection for DSH's fork-backed subagent token metric. */
import { createElement, type ComponentType, type ReactElement } from 'react'
import type { SnapshotSelectorHook } from '@deepseek-ai/dsh-client-store'
import type { StoredEntry } from '@deepseek-ai/dsh-client-ui-slots'
import type { MnemonClientContext, MnemonSessionListState, MnemonSessionSummary } from "./dsh-context.ts"
import { isRecord } from './is-record.ts'

const MNEMON_SUBAGENT_TOKEN_USAGE_KEY = 'mnemonSubagentTokenUsage'
const SUBAGENT_LINEAGE_SLOT = 'conversation.session.header.lineage'
const SUBAGENT_LOCALE = 'subagent'
const MNEMON_SHADOW_PRIORITY = -100

interface MnemonTokenUsageProjection {
  uncachedInputTokens: number
  outputTokens: number
  cacheReadTokens: number
  cacheWriteTokens: number
}

type ProjectionRecord = Readonly<Record<string, unknown>>
type LineageProps = Record<string, unknown> & {
  useSessions: SnapshotSelectorHook<MnemonSessionListState>
}
type LineageComponent = ComponentType<LineageProps>

const scopedSnapshots = new WeakMap<MnemonSessionListState, MnemonSessionListState>()

function isTokenUsage(value: unknown): value is MnemonTokenUsageProjection {
  if (!isRecord(value)) return false
  const usage = value as Record<keyof MnemonTokenUsageProjection, unknown>
  return ['uncachedInputTokens', 'outputTokens', 'cacheReadTokens', 'cacheWriteTokens']
    .every((key) => {
      const count = usage[key as keyof MnemonTokenUsageProjection]
      return typeof count === 'number' && Number.isSafeInteger(count) && count >= 0
    })
}

/**
 * Present the child-local projection under the generic key expected by the
 * released official catalog component. All other consumers continue seeing
 * DSH's complete-log `tokenUsage` value.
 */
export function scopeSubagentTokenUsage(state: MnemonSessionListState): MnemonSessionListState {
  const cached = scopedSnapshots.get(state)
  if (cached !== undefined) return cached
  let byId: MnemonSessionListState['byId'] | undefined
  for (const [id, summary] of Object.entries(state.byId)) {
    if (summary.origin !== 'subagent') continue
    const projections = summary.projectionValues as ProjectionRecord | undefined
    const scopedUsage = projections?.[MNEMON_SUBAGENT_TOKEN_USAGE_KEY]
    if (!isTokenUsage(scopedUsage)) continue
    byId ??= { ...state.byId }
    byId[id as keyof typeof byId] = {
      ...summary,
      projectionValues: {
        ...projections,
        tokenUsage: scopedUsage,
      } as NonNullable<MnemonSessionSummary['projectionValues']>,
    }
  }
  const scoped = byId === undefined ? state : { ...state, byId }
  scopedSnapshots.set(state, scoped)
  return scoped
}

/** Wrap the framework hook while preserving its selector/equality contract. */
export function createScopedUseSessions(
  useSessions: SnapshotSelectorHook<MnemonSessionListState>,
): SnapshotSelectorHook<MnemonSessionListState> {
  return <Selected,>(
    selector: (state: MnemonSessionListState) => Selected,
    equal?: (left: Selected, right: Selected) => boolean,
  ): Selected => useSessions(state => selector(scopeSubagentTokenUsage(state)), equal)
}

function lineageShadow(official: LineageComponent): LineageComponent {
  return function MnemonSubagentHeaderLineage(props: LineageProps): ReactElement {
    return createElement(official, {
      ...props,
      useSessions: createScopedUseSessions(props.useSessions),
    })
  }
}

/** SlotRegistry face used to re-register the official entry's type-erased locale and inject. */
interface ErasedSlots {
  entries(key: string): readonly StoredEntry[]
  subscribe(key: string, listener: () => void): () => void
  register(options: Record<string, unknown>, component: LineageComponent): () => void
}

function officialLineage(entries: readonly StoredEntry[]): StoredEntry | undefined {
  return entries.find(entry => (entry.options.priority ?? 0) === 0
    && entry.locale === SUBAGENT_LOCALE
    && typeof entry.component === 'function'
    && typeof entry.inject === 'function')
}

/**
 * Shadow the released official single-slot entry at a lower priority. The
 * component and action factory are captured from the public slot ledger, so
 * Mnemon keeps the official UI, styles, locale, and navigation behavior.
 */
export function mountSubagentTokenUsageOverride(ctx: MnemonClientContext): () => void {
  const slots = ctx.slots as unknown as ErasedSlots
  let official: StoredEntry | undefined
  let disposeShadow: (() => void) | undefined
  const reconcile = (): void => {
    const entries = slots.entries(SUBAGENT_LINEAGE_SLOT)
    if (official !== undefined && entries.includes(official)) return
    const disposePreviousShadow = disposeShadow
    disposeShadow = undefined
    official = undefined
    disposePreviousShadow?.()
    official = officialLineage(entries)
    if (official === undefined) return
    disposeShadow = slots.register({
      name: SUBAGENT_LINEAGE_SLOT,
      priority: MNEMON_SHADOW_PRIORITY,
      locale: official.locale,
      inject: official.inject,
      registrant: 'dsh-mnemon/subagent-token-usage',
    }, lineageShadow(official.component as LineageComponent))
  }
  const unsubscribe = slots.subscribe(SUBAGENT_LINEAGE_SLOT, reconcile)
  reconcile()
  return () => {
    unsubscribe()
    disposeShadow?.()
  }
}
