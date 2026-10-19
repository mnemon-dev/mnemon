import { createElement, type ReactNode } from 'react'
import type { MemorySourcePageProps } from './source-contracts.ts'
export type { MemorySourcePageProps } from './source-contracts.ts'

export const MNEMON_SOURCE_PAGE_SLOT = 'mnemon.source.page' as const
/** Conventional operations used by Mnemon's descriptor-driven Source page. */
export const MNEMON_SOURCE_CONFIGURATION_READ = 'configuration' as const
export const MNEMON_SOURCE_CONFIGURATION_MUTATE = 'configuration' as const

const SOURCE_TYPE_ID = /^[a-z][a-z0-9-]{0,127}$/u
const PAGE_ID = /^[a-z][a-z0-9-]{0,127}$/u

export type MemorySourcePageComponent = (props: MemorySourcePageProps) => ReactNode

/** The existing DSH Slot capability narrowed to this one child Slot. */
export interface MemorySourceUIContext {
  locale: { bind(namespace: 'mnemon'): import('./locales.ts').MnemonTranslate }
  slots: {
    inject(name: typeof MNEMON_SOURCE_PAGE_SLOT, setup: () => () => void): () => void
    register(options: {
      name: typeof MNEMON_SOURCE_PAGE_SLOT
      id: string
      order: number
      label: string | (() => string)
      priority?: number
    }, component: MemorySourcePageComponent): () => void
  }
}

interface MemorySourcePageDirectoryContext {
  locale: { getSnapshot(): unknown; subscribe(listener: () => void): () => void }
  slots: {
    getVersion(name: typeof MNEMON_SOURCE_PAGE_SLOT): number
    entriesOfSlot(name: typeof MNEMON_SOURCE_PAGE_SLOT): readonly { options: { id?: string; label?: string | (() => string); order?: number }; component?: unknown }[]
    subscribe(name: typeof MNEMON_SOURCE_PAGE_SLOT, listener: () => void): () => void
  }
}

export interface MemorySourcePageNavigation {
  /** Let the Host retain a compact page header while scrolling in a sidebar. */
  stickyHeader?: boolean
  group?: 'storage' | 'tools' | 'sources'
  primary?: boolean
  glyph?: string
  detail?: string | (() => string)
}
export interface MemorySourcePageDefinition {
  id: string
  label: string | (() => string)
  order?: number
  component: MemorySourcePageComponent
  navigation?: MemorySourcePageNavigation
}

export interface MemorySourceUIContribution {
  sourceTypeId: string
  pages: readonly MemorySourcePageDefinition[]
}

export interface MemorySourcePageEntry {
  id: string
  sourceTypeId: string
  pageId: string
  label: string
  order: number
  navigation?: Omit<MemorySourcePageNavigation, 'detail'> & { detail?: string }
}

export interface MemorySourcePageDirectory {
  getSnapshot(): readonly MemorySourcePageEntry[]
  subscribe(listener: () => void): () => void
}

export function memorySourcePageEntryId(sourceTypeId: string, pageId: string): string {
  if (!SOURCE_TYPE_ID.test(sourceTypeId)) throw new Error('memory Source UI sourceTypeId must match [a-z][a-z0-9-]{0,127}')
  if (!PAGE_ID.test(pageId)) throw new Error('memory Source UI page id must match [a-z][a-z0-9-]{0,127}')
  return `${sourceTypeId}/${pageId}`
}

/**
 * Thin Client-Fiber adapter over the DSH child Slot. It creates no service or
 * registry: `slots.inject/register` own declaration waiting and disposal.
 */
export function installMemorySourceUI(
  ctx: MemorySourceUIContext,
  contribution: MemorySourceUIContribution,
): () => void {
  if (contribution.pages.length === 0) throw new Error('memory Source UI requires at least one page')
  const seen = new Set<string>()
  const pages = contribution.pages.map((page, index) => {
    const entryId = memorySourcePageEntryId(contribution.sourceTypeId, page.id)
    if (seen.has(entryId)) throw new Error(`memory Source UI page is duplicated: ${entryId}`)
    seen.add(entryId)
    return { page, entryId, order: page.order ?? 1_000 + index }
  })

  // One declaration wait owns the whole contribution. Once the parent exists,
  // registrations commit transactionally and are released in reverse order.
  return ctx.slots.inject(MNEMON_SOURCE_PAGE_SLOT, () => {
    const disposers: Array<() => void> = []
    try {
      for (const { page, entryId, order } of pages) {
        disposers.push(ctx.slots.register({
          name: MNEMON_SOURCE_PAGE_SLOT,
          id: entryId,
          order,
          label: page.label,
        }, Object.assign((props: MemorySourcePageProps) => createElement(page.component, props), { mnemonNavigation: page.navigation })))
      }
    } catch (error) {
      for (const dispose of disposers.reverse()) dispose()
      throw error
    }
    return () => {
      for (const dispose of disposers.reverse()) dispose()
    }
  })
}

/** Stable uSES directory over the Slot ledger; no parallel page registry. */
export function createMemorySourcePageDirectory(ctx: MemorySourcePageDirectoryContext): MemorySourcePageDirectory {
  let version = -1
  let localeSnapshot: unknown
  let snapshot: readonly MemorySourcePageEntry[] = Object.freeze([])
  const read = (): readonly MemorySourcePageEntry[] => {
    const currentVersion = ctx.slots.getVersion(MNEMON_SOURCE_PAGE_SLOT)
    const currentLocale = ctx.locale.getSnapshot()
    if (currentVersion === version && currentLocale === localeSnapshot) return snapshot
    version = currentVersion
    localeSnapshot = currentLocale
    snapshot = Object.freeze(ctx.slots.entriesOfSlot(MNEMON_SOURCE_PAGE_SLOT).flatMap(entry => {
      const id = entry.options.id
      if (id === undefined) return []
      const separator = id.indexOf('/')
      if (separator <= 0 || separator === id.length - 1) return []
      let label: string | undefined
      try {
        label = typeof entry.options.label === 'function' ? entry.options.label() : entry.options.label
      } catch {
        // Presentation metadata must fail locally; a broken label cannot take
        // down the canonical workspace or any Host/Headless memory behavior.
      }
      const component = entry.component as { mnemonNavigation?: MemorySourcePageNavigation } | undefined
      const metadata = component?.mnemonNavigation
      let navigation: MemorySourcePageEntry['navigation']
      if (metadata !== undefined) {
        let detail: string | undefined
        try { detail = typeof metadata.detail === 'function' ? metadata.detail() : metadata.detail } catch {}
        navigation = { ...metadata, ...(detail === undefined ? { detail: undefined } : { detail }) } as MemorySourcePageEntry['navigation']
      }
      return [{
        id,
        sourceTypeId: id.slice(0, separator),
        pageId: id.slice(separator + 1),
        label: label?.trim() || id.slice(separator + 1),
        order: entry.options.order ?? 0,
        ...(navigation === undefined ? {} : { navigation }),
      }]
    }).sort((left, right) => left.order - right.order || left.id.localeCompare(right.id)))
    return snapshot
  }
  return {
    getSnapshot: read,
    subscribe(listener) {
      const changed = () => {
        read()
        listener()
      }
      const stopSlots = ctx.slots.subscribe(MNEMON_SOURCE_PAGE_SLOT, changed)
      const stopLocale = ctx.locale.subscribe(changed)
      return () => { stopLocale(); stopSlots() }
    },
  }
}
