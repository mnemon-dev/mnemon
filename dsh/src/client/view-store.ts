import { useEffect, useMemo, useSyncExternalStore } from 'react'
import type { MemoryPluginEntryView, MemoryPluginPreference, MemoryViewDashboard } from '../host/view-protocol.ts'
import type { MnemonClient } from './api.ts'
import { message } from './page-kit.tsx'

/** A change to the components or the main Strategy, with the states on either side. */
export interface MnemonViewChange {
  seq: number
  /** `own`: a write from this page; `external`: DSH's component list, another window, a profile reload. */
  origin: 'own' | 'external'
  /** The control whose write made an `own` change. */
  key?: string
  before: MemoryViewDashboard
  after: MemoryViewDashboard
}

/** The memory components and main Strategy choice as the configuration page shows them. */
export interface MnemonViewState {
  status: 'loading' | 'ready' | 'unavailable'
  dashboard: MemoryViewDashboard | null
  /** The write crossing the wire: its control, and the states it moves between. */
  working: { key: string; before: MemoryViewDashboard; after: MemoryViewDashboard } | null
  /** The last write that did not land, or a reload that failed after one did. */
  failure: { seq: number; key: string; kind: 'apply' | 'refresh' } | null
  /** The latest change, for feedback that names what it did. */
  change: MnemonViewChange | null
}

interface ViewRequest {
  key: string
  strategyTypeId: string | undefined
  entries: Record<string, MemoryPluginPreference>
  /** Entries whose options the write sets; the others keep the options the Host holds. */
  configured: readonly string[]
}

/** The Host's answer when a View write was prepared against an older revision. */
const STALE_VIEW = /configuration changed/u
/** How long the plugin manager's announcements of a write's own switches may trail it. */
const ECHO_MS = 5000

/** Keep each requested switch and option, over the configuration the Host holds now. */
function rebase(request: ViewRequest, dashboard: MemoryViewDashboard): Record<string, MemoryPluginPreference> {
  return Object.fromEntries(Object.entries(request.entries).map(([entryId, preference]) => {
    const current = dashboard.entries.find(entry => entry.entryId === entryId)
    const config = request.configured.includes(entryId) ? preference.config : current?.config ?? preference.config
    return [entryId, { enabled: preference.enabled, config: structuredClone(config) }]
  }))
}

/** Entries whose switch differs between two dashboards, as they are after. */
export function switchedEntries(before: MemoryViewDashboard, after: MemoryViewDashboard): MemoryPluginEntryView[] {
  return after.entries.filter(entry => {
    const previous = before.entries.find(candidate => candidate.entryId === entry.entryId)
    return previous !== undefined && previous.enabled !== entry.enabled
  })
}

/** Entries whose options differ between two dashboards, as they are after. */
export function reconfiguredEntries(before: MemoryViewDashboard, after: MemoryViewDashboard): MemoryPluginEntryView[] {
  return after.entries.filter(entry => {
    const previous = before.entries.find(candidate => candidate.entryId === entry.entryId)
    return previous !== undefined && JSON.stringify(previous.config) !== JSON.stringify(entry.config)
  })
}

function moved(before: MemoryViewDashboard, after: MemoryViewDashboard): boolean {
  return before.strategyTypeId !== after.strategyTypeId || switchedEntries(before, after).length > 0 || reconfiguredEntries(before, after).length > 0
}

/**
 * One reader and writer of the View dashboard for the configuration page, so
 * the overview and the Strategy, memory layer and Provider groups show the
 * same component states and switch components through one revision-fenced
 * write. It records each change with the states on either side, so the page
 * can say what changed, undo its own writes and retry a refused one.
 */
export class MnemonViewStore {
  private state: MnemonViewState
  private readonly listeners = new Set<() => void>()
  private ticket = 0
  private seq = 0
  private last: ViewRequest | undefined
  private own: { at: number; request: ViewRequest } | undefined

  constructor(private readonly client: MnemonClient | undefined) {
    this.state = { status: client === undefined ? 'unavailable' : 'loading', dashboard: null, working: null, failure: null, change: null }
  }

  readonly getSnapshot = (): MnemonViewState => this.state

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  /** Re-read the dashboard, keeping the current one on screen until the answer arrives. */
  async load(): Promise<void> {
    // A write re-reads when it finishes, after every change it caused, such
    // as the plugin manager's own announcement of the switches it applied.
    if (this.client === undefined || this.state.working !== null) return
    const ticket = ++this.ticket
    const previous = this.state.status === 'ready' ? this.state.dashboard : null
    if (this.state.status !== 'ready') this.publish({ ...this.state, status: 'loading' })
    try {
      const dashboard = await this.client.viewDashboard()
      if (ticket !== this.ticket) return
      const external = previous !== null && moved(previous, dashboard) && !this.echoes(previous, dashboard)
      this.publish({
        ...this.state, status: 'ready', dashboard, failure: null,
        change: external ? { seq: ++this.seq, origin: 'external', before: previous, after: dashboard } : this.state.change,
      })
    } catch {
      if (ticket === this.ticket) this.publish({ ...this.state, status: 'unavailable', dashboard: null })
    }
  }

  /**
   * Switch components and choose the main Strategy in one View write. The
   * change shows at once and reverts to the Host's state if refused; a write
   * that met a change made elsewhere is applied once more on top of it.
   * @param strategyTypeId the main Strategy to choose; omitted, the write keeps the one the Host holds.
   * @returns whether the Host accepted the write.
   */
  async apply(key: string, strategyTypeId: string | undefined, entries: Record<string, MemoryPluginPreference>, configured: readonly string[] = []): Promise<boolean> {
    const previous = this.state.dashboard
    if (this.client === undefined || previous === null || this.state.working !== null) return false
    const ticket = ++this.ticket
    const request: ViewRequest = { key, strategyTypeId, entries, configured }
    this.last = request
    this.own = { at: Date.now(), request }
    // Shown as if the switches already took effect, so the page does not
    // flash the state between the old and the new configuration.
    const optimistic: MemoryViewDashboard = { ...previous, strategyTypeId: strategyTypeId ?? previous.strategyTypeId, entries: previous.entries.map(entry => entries[entry.entryId] === undefined
      ? entry
      : { ...entry, enabled: entries[entry.entryId]!.enabled, active: entries[entry.entryId]!.enabled, ...configured.includes(entry.entryId) ? { config: entries[entry.entryId]!.config } : {} }) }
    this.publish({ ...this.state, working: { key, before: previous, after: optimistic }, failure: null, dashboard: optimistic })
    let base = previous
    try {
      for (let attempt = 0; ; attempt += 1) {
        try {
          await this.client.applyView({ expectedRevision: base.revision, strategyTypeId: strategyTypeId ?? base.strategyTypeId, entries: rebase(request, base) })
          break
        } catch (reason) {
          // Another save moves the View revision: a group on this page, a
          // switch on DSH's list below it, another window. Apply the same
          // choice once more on top of what the Host holds now.
          const latest = attempt === 0 && STALE_VIEW.test(message(reason)) ? await this.client.viewDashboard().catch(() => undefined) : undefined
          if (latest === undefined || ticket !== this.ticket) throw reason
          base = latest
        }
      }
    } catch {
      if (ticket !== this.ticket) return false
      // Show what the Host now holds; the refused write may have met a newer revision.
      const current = await this.client.viewDashboard().catch(() => previous)
      if (ticket === this.ticket) this.publish({ ...this.state, dashboard: current, working: null, failure: { seq: ++this.seq, key, kind: 'apply' } })
      return false
    }
    try {
      const next = await this.client.viewDashboard()
      if (ticket === this.ticket) this.publish({ ...this.state, status: 'ready', dashboard: next, working: null, change: { seq: ++this.seq, origin: 'own', key, before: base, after: next } })
    } catch {
      // The write committed. Keep its visible value and refuse another write
      // with a stale revision until the page reloads.
      if (ticket === this.ticket) this.publish({ ...this.state, dashboard: this.state.dashboard === null ? null : { ...this.state.dashboard, writable: false }, working: null, failure: { seq: ++this.seq, key, kind: 'refresh' } })
    }
    return true
  }

  /** Switch components on or off, keeping whichever main Strategy the Host holds. */
  setEnabled(key: string, changes: ReadonlyArray<{ entry: MemoryPluginEntryView; enabled: boolean }>): Promise<boolean> {
    return this.apply(key, undefined, Object.fromEntries(changes.map(({ entry, enabled }) => [entry.entryId, { enabled, config: structuredClone(entry.config) }])))
  }

  /** Set one component's options, keeping its switch. */
  configure(key: string, entry: MemoryPluginEntryView, config: Record<string, MemoryPluginPreference['config'][string]>): Promise<boolean> {
    return this.apply(key, undefined, { [entry.entryId]: { enabled: entry.enabled, config: structuredClone(config) } }, [entry.entryId])
  }

  /** Put back the main Strategy, the switches and the options a change moved. */
  revert(change: MnemonViewChange): Promise<boolean> {
    const reconfigured = reconfiguredEntries(change.before, change.after).map(entry => entry.entryId)
    const touched = new Set([...switchedEntries(change.before, change.after).map(entry => entry.entryId), ...reconfigured])
    const entries = Object.fromEntries([...touched].map(entryId => {
      const before = change.before.entries.find(candidate => candidate.entryId === entryId)!
      return [entryId, { enabled: before.enabled, config: structuredClone(before.config) }]
    }))
    return this.apply('revert:' + String(change.seq), change.before.strategyTypeId, entries, reconfigured)
  }

  /** Send the last refused write again, over the state the Host holds now. */
  retry(): Promise<boolean> {
    const last = this.last
    return last === undefined ? Promise.resolve(false) : this.apply(last.key, last.strategyTypeId, last.entries, last.configured)
  }

  /** Whether a reload only shows the switches this page's latest write asked for. */
  private echoes(before: MemoryViewDashboard, after: MemoryViewDashboard): boolean {
    const own = this.own
    if (own === undefined || Date.now() - own.at > ECHO_MS) return false
    if (before.strategyTypeId !== after.strategyTypeId && after.strategyTypeId !== own.request.strategyTypeId) return false
    return switchedEntries(before, after).every(entry => own.request.entries[entry.entryId]?.enabled === entry.enabled)
      && reconfiguredEntries(before, after).every(entry => own.request.configured.includes(entry.entryId))
  }

  private publish(state: MnemonViewState): void {
    this.state = state
    for (const listener of [...this.listeners]) listener()
  }
}

/** The page's View store, re-read whenever `refreshKey` moves. */
export function useViewStore(client: MnemonClient | undefined, refreshKey: number): { store: MnemonViewStore; state: MnemonViewState } {
  const store = useMemo(() => new MnemonViewStore(client), [client])
  const state = useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot)
  useEffect(() => { void store.load() }, [store, refreshKey])
  return { store, state }
}
