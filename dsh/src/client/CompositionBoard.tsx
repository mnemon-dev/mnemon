import { useId, useMemo, useState, type JSX, type ReactNode } from 'react'
import { IconChevronDownOutlineRegular, IconLinkOutlineRegular, IconSearchOutlineRegular, IconSettingsOutlineRegular, StateDot, Switch, TextShimmer } from '@deepseek-ai/dsh-client-ui-primitives'
import type { MemoryCompositionStatus } from '../host/protocol.ts'
import type { MemoryPluginEntryView, MemoryPluginPreference, MemoryViewDashboard } from '../host/view-protocol.ts'
import { componentCopy, nameList } from './component-copy.ts'
import { ComponentDetails, ComponentPage, MainChoice, type ComponentDetailsProps, type ComponentState } from './component-details.tsx'
import { componentModel, enhancementApplies, layerOf, mainPlan, relationsOf, sourceOf, switchPlan, unmetRequirements, type MemoryComponentModel, type SwitchPlan } from './component-model.ts'
import { ActionButton, Callout, Reveal, TARGET } from './feedback.tsx'
import type { MnemonKey, MnemonTranslate } from './locales.ts'
import css from './MnemonSettingsCard.module.css'
import { SettingSelect } from './settings-controls.tsx'
import { switchedEntries, type MnemonViewState, type MnemonViewStore } from './view-store.ts'

/**
 * The groups the composition is drawn in, by the role a component declares.
 * A role missing here still gets a group of its own, titled by its id, so a
 * new kind of component appears without a change to this page.
 */
const ROLE_GROUPS: ReadonlyArray<{ role: string; title: MnemonKey }> = [
  { role: 'source', title: 'board.sources' },
  { role: 'strategy-extension', title: 'board.enhancements' },
]
/** More components than this, and the board offers a search. */
const SEARCH_FROM = 8

type View = { store: MnemonViewStore; state: MnemonViewState }
type Name = (entry: MemoryPluginEntryView) => string

/** The settings components contribute to their own pages. */
export interface ComponentSettingsRenderer {
  has(packageName: string): boolean
  render(entry: MemoryPluginEntryView, writable: boolean): ReactNode
}

/**
 * The component pages open, newest last. A page opened from another page can
 * go back to it; one opened from the configuration starts a new trail.
 */
export interface PageTrail {
  trail: readonly string[]
  open(entryId: string, from: 'configuration' | 'page'): void
  back(): void
  close(): void
}

export function usePageTrail(): PageTrail {
  const [trail, setTrail] = useState<readonly string[]>([])
  return useMemo(() => ({
    trail,
    open: (entryId, from) => setTrail(current => from === 'page' ? current.at(-1) === entryId ? current : [...current, entryId] : [entryId]),
    back: () => setTrail(current => current.slice(0, -1)),
    close: () => setTrail([]),
  }), [trail])
}

/** A component's name, which opens its page; underlined on hover. */
export function PageLink(props: { label: string; onOpen: () => void }): JSX.Element {
  return <button type="button" className={css.pageLink} aria-haspopup="dialog" onClick={props.onOpen}><span>{props.label}</span></button>
}

/** The gear that opens a component's page where its settings are: the page's visible way in. */
export function SettingsGear(props: { label: string; onOpen: () => void }): JSX.Element {
  return <button type="button" className={css.boardIconButton} aria-haspopup="dialog" aria-label={props.label} title={props.label} onClick={props.onOpen}>
    <IconSettingsOutlineRegular size={16} />
  </button>
}

/** Components named as chips, each opening its page; the dot-less chip is one switched off. */
export function ComponentChips(props: { label?: string | undefined; chips: readonly { key: string; name: string; on: boolean; title: string; open: (() => void) | undefined }[] }): JSX.Element | null {
  if (props.chips.length === 0) return null
  return <span className={css.boardLinks}>
    {props.label !== undefined && <span className={css.boardLinksLabel}>{props.label}</span>}
    {props.chips.map(chip => chip.open === undefined
      ? <span key={chip.key} className={css.boardLink} title={chip.title} data-on={chip.on ? '' : undefined}><IconLinkOutlineRegular size={12} />{chip.name}</span>
      : <button key={chip.key} type="button" className={css.boardLink} aria-haspopup="dialog" title={chip.title} data-on={chip.on ? '' : undefined} onClick={chip.open}>
        <IconLinkOutlineRegular size={12} />{chip.name}
      </button>)}
  </span>
}

/** The saved layer switch, for a layer an earlier configuration turned off. */
export interface LayerSettings {
  set(layerId: string, enabled: boolean): Promise<void>
}

/** The View request for a plan: every switch it moves, each keeping its options. */
function requestOf(plan: SwitchPlan): Record<string, MemoryPluginPreference> | undefined {
  return plan.changes === undefined ? undefined : Object.fromEntries(plan.changes.map(({ entry, enabled }) => [entry.entryId, { enabled, config: structuredClone(entry.config) }]))
}

/**
 * One main Strategy composes memory. Choosing one switches it on and every
 * other main Strategy off, with whatever that brings or takes along.
 */
export function chooseMain(store: MnemonViewStore, dashboard: MemoryViewDashboard, entry: MemoryPluginEntryView): Promise<boolean> {
  const entries = requestOf(mainPlan(dashboard, entry))
  return entries === undefined ? Promise.resolve(false) : store.apply('strategy:' + entry.entryId, entry.typeId!, entries)
}

/** Switch one component, with whatever it brings or takes along. */
export function switchComponent(store: MnemonViewStore, dashboard: MemoryViewDashboard, entry: MemoryPluginEntryView, enabled: boolean): Promise<boolean> {
  const plan = switchPlan(dashboard, entry, enabled)
  return plan.changes === undefined ? Promise.resolve(false) : store.setEnabled('component:' + entry.entryId, plan.changes)
}

/**
 * Turn a memory layer on or off with its Source component, which DSH's
 * component list shows as the same switch. A layer an earlier configuration
 * turned off comes back on through that saved switch as well.
 */
export async function switchLayer(view: View, system: MemoryCompositionStatus | null, layers: LayerSettings, layerId: string, on: boolean): Promise<void> {
  const dashboard = view.state.dashboard
  const component = dashboard === null ? undefined : sourceOf(dashboard, layerId)
  const saved = system?.configuration.layers[layerId]?.enabled !== false
  if (component === undefined || !component.writable) {
    if (saved !== on) await layers.set(layerId, on)
    return
  }
  if (on && !saved) await layers.set(layerId, true)
  if (component.enabled !== on) await switchComponent(view.store, dashboard!, component, on)
}

/** A component a row names at a glance: one it needs, or one that depends on it. */
interface Relation {
  key: string
  name: string
  on: boolean
  /** Which way the relation goes, for the tooltip. */
  title: string
  open: () => void
}

/** One row of the board: a component, or a saved memory layer whose component cannot be read. */
interface BoardItem {
  key: string
  entry: MemoryPluginEntryView | undefined
  label: string
  hint: string
  relations: Relation[]
  state: ComponentState | undefined
  checked: boolean
  disabled: boolean
  target: string
  onSwitch: (on: boolean) => void
}

/**
 * A component as a preference row: its name and what it does; the components
 * it relates to, each opening its own page; its state as DSH shows it; a gear
 * where it has settings of its own; and its switch. The gear and the name
 * both open the component's page.
 */
function BoardRow(props: { item: BoardItem; configurable: boolean; t: MnemonTranslate; onOpen: (() => void) | undefined }): JSX.Element {
  const { item, t } = props
  return <div className={css.boardRow} {...{ [TARGET]: item.target }}>
    <div className={css.boardRowLine}>
      <div className={css.settingCopy}>
        {props.onOpen === undefined ? <strong>{item.label}</strong> : <PageLink label={item.label} onOpen={props.onOpen} />}
        {item.hint !== '' && <small>{item.hint}</small>}
        <ComponentChips chips={item.relations} />
      </div>
      <div className={css.boardControl}>
        {item.state !== undefined && <StateLabel state={item.state} />}
        {props.onOpen !== undefined && props.configurable && <SettingsGear label={t('options.aria', { component: item.label })} onOpen={props.onOpen} />}
        <Switch className={css.switch} checked={item.checked} label={item.label} disabled={item.disabled} onChange={item.onSwitch} />
      </div>
    </div>
  </div>
}

function StateLabel(props: { state: ComponentState }): JSX.Element {
  const { state } = props
  return <span className={css.boardState} data-tone={state.pending === true ? 'ongoing' : state.tone}>
    {state.pending === true
      ? <><StateDot state="ongoing" /><TextShimmer active>{state.text}</TextShimmer></>
      : <>{state.tone !== undefined && <StateDot state={state.tone} />}<span>{state.text}</span></>}
  </span>
}

interface Problem {
  tone: 'error' | 'warning' | 'idle'
  text: string
  actions: Array<{ key: string; label: string; primary?: boolean; run: () => void }>
}

export interface CompositionBoardProps {
  view: View
  /** Whether a composition serves, and the saved memory layers. */
  system: MemoryCompositionStatus | null
  layers: LayerSettings
  /** While the saved memory layers are still being read. */
  systemPending?: boolean
  /** The settings components contribute to their pages. */
  componentSettings?: ComponentSettingsRenderer
  /** The pages open, when the configuration opens them from elsewhere too. */
  pages?: PageTrail
  /** Show only this component's page, by its package name, as DSH's row page does; related pages open over it. */
  page?: string | undefined
  readOnly: boolean
  language: string
  t: MnemonTranslate
}

/**
 * The memory composition, drawn from the components installed: the main
 * Strategy as a choice of one, then a group of switches for each role the
 * other components declare. Shipped components and installed extensions are
 * named, related and configured the same way, from their own declarations.
 * Every switch applies at once and brings or takes along what depends on it;
 * a component's page says what that would be and holds its options. The
 * board speaks up only when memory is not composed as chosen.
 */
export function CompositionBoard(props: CompositionBoardProps): JSX.Element | null {
  const { view, system, t, language } = props
  const instance = useId()
  const [layerPending, setLayerPending] = useState<{ layerId: string; on: boolean } | null>(null)
  const ownPages = usePageTrail()
  const pages = props.pages ?? ownPages
  const [othersOpen, setOthersOpen] = useState(false)
  const [query, setQuery] = useState('')
  const dashboard = view.state.dashboard
  // Without the View the components cannot be read, but the saved memory
  // layers can still be switched.
  const componentsUnavailable = dashboard === null && view.state.status === 'unavailable'
  const pageMode = props.page !== undefined
  if (componentsUnavailable && (pageMode || system === null)) return pageMode ? <p className={css.boardEmpty} role="status">{t('board.componentsUnavailable')}</p> : null
  const name: Name = entry => componentCopy(entry, language).label
  const model = dashboard === null ? undefined : componentModel(dashboard)
  const working = view.state.working
  const changing = new Map(working === null ? [] : switchedEntries(working.before, working.after).map(entry => [entry.entryId, entry.enabled]))
  const busy = working !== null || layerPending !== null || props.readOnly
  const locked = busy || dashboard?.writable !== true
  // The Host validates each write against the selected main Strategy, so
  // components wait while it is not running; the problem line offers the fix.
  const selectedRunning = model !== undefined && model.selected !== undefined && model.selected === model.composing
  const pendingText = (on: boolean): string => t(on ? 'board.turningOn' : 'board.turningOff')
  const placeholders = (count: number): JSX.Element => <div className={css.boardGroup} aria-hidden="true">
    {Array.from({ length: count }, (_, index) => <div key={index} className={css.placeholderRow}><span /><span /></div>)}
  </div>
  const frame = (content: ReactNode, pending: boolean): JSX.Element => <section className={`${css.section} ${css.boardSection}`} aria-labelledby={`${instance}-heading`} aria-busy={pending} {...{ [TARGET]: 'overview' }}>
    <div className={css.sectionHeading}>
      <h2 id={`${instance}-heading`}>{t('board.title')}</h2>
      <p>{t('board.description')}</p>
      <span className={css.appliesNow}>{t('config.appliesNow')}</span>
    </div>
    <div className={css.board}>{content}</div>
  </section>
  if (model === undefined && !componentsUnavailable) {
    if (pageMode) return <p className={css.boardLoading} role="status"><StateDot state="ongoing" /><TextShimmer active>{t('board.loading')}</TextShimmer></p>
    return frame(<>
      <p className={css.boardLoading} role="status"><StateDot state="ongoing" /><TextShimmer active>{t('board.loading')}</TextShimmer></p>
      {placeholders(4)}
    </>, true)
  }

  const problem: Problem | undefined = model === undefined ? { tone: 'idle', text: t('board.componentsUnavailable'), actions: [] }
    : working === null ? problemOf(props, model, dashboard!, name) : undefined

  const stateOf = (entry: MemoryPluginEntryView): ComponentState | undefined => {
    const pending = changing.get(entry.entryId)
    if (pending !== undefined) return { text: pendingText(pending), pending: true }
    if (!entry.enabled) return entry.roles.includes('source') ? { tone: 'idle', text: t('board.off') } : undefined
    const unmet = unmetRequirements(dashboard!, entry)[0]
    if (unmet !== undefined) return { tone: 'warning', text: unmet.providers[0] === undefined ? t('board.needsMissing') : t('board.needs', { component: name(unmet.providers[0]) }) }
    if (!entry.active) return { tone: 'warning', text: t('board.notRunning') }
    if (entry.roles.includes('strategy-extension') && !enhancementApplies(entry, model!.composing)) return { tone: 'idle', text: t('board.unused') }
    if (entry.roles.includes('source') && switchPlan(dashboard!, entry, false).blocked === 'last-source') return { tone: 'done', text: t('board.lastSource') }
    return { tone: 'done', text: t('board.running') }
  }
  /**
   * The components a row names: each one it needs that only a single installed
   * component provides, and each running one that depends on it alone.
   */
  const relationsFor = (entry: MemoryPluginEntryView): Relation[] => {
    const relations = relationsOf(dashboard!, entry)
    const needs = relations.needs.flatMap(need => need.providers.length === 1 ? [need.providers[0]!] : [])
      .map(other => ({ key: 'needs:' + other.entryId, name: name(other), on: other.enabled, title: t('board.relationNeeds', { component: name(other) }), open: () => pages.open(other.entryId, 'configuration') }))
    const neededBy = relations.neededBy.map(other => ({ key: 'by:' + other.entryId, name: name(other), on: true, title: t('board.relationNeededBy', { component: name(other) }), open: () => pages.open(other.entryId, 'configuration') }))
    return [...needs, ...neededBy]
  }
  /** A component's own row: its switch brings or takes along what depends on it. */
  const componentItem = (entry: MemoryPluginEntryView, target: string): BoardItem => {
    const plan = switchPlan(dashboard!, entry, !entry.enabled)
    const missing = !entry.enabled && unmetRequirements(dashboard!, entry).some(unmet => unmet.providers.length === 0)
    return {
      key: entry.entryId, entry, label: name(entry), hint: componentCopy(entry, language).hint, relations: relationsFor(entry),
      state: stateOf(entry), checked: changing.get(entry.entryId) ?? entry.enabled, target,
      disabled: locked || !entry.writable || !selectedRunning || missing || plan.blocked !== undefined,
      onSwitch: next => { void switchComponent(view.store, dashboard!, entry, next) },
    }
  }

  // Every memory layer and every Source component, including one switched off
  // that has registered nothing yet; each layer's switch is its component's.
  const layerIds = Object.keys(system?.configuration.layers ?? {})
  const sources = dashboard?.entries.filter(entry => entry.roles.includes('source')) ?? []
  const sourceKeys = [...new Set([...layerIds, ...sources.map(entry => layerOf(entry) ?? 'component:' + entry.entryId)])]
  const sourceItems = sourceKeys.map((key): BoardItem => {
    const layerId = key.startsWith('component:') ? undefined : key
    const component = layerId === undefined ? sources.find(entry => 'component:' + entry.entryId === key) : dashboard === null ? undefined : sourceOf(dashboard, layerId)
    if (layerId === undefined) return componentItem(component!, key)
    const saved = system?.configuration.layers[layerId]?.enabled !== false
    const pending = layerPending !== null && layerPending.layerId === layerId ? layerPending.on : undefined
    const run = (next: boolean): void => {
      if (component === undefined || !component.writable || !saved) setLayerPending({ layerId, on: next })
      void switchLayer(view, system, props.layers, layerId, next).finally(() => setLayerPending(current => current?.layerId === layerId ? null : current))
    }
    if (component !== undefined) {
      const item = componentItem(component, 'layer:' + layerId)
      // A layer an earlier configuration turned off reads as off; its switch turns both back on.
      return {
        ...item, onSwitch: run,
        checked: pending ?? (saved && item.checked),
        state: pending !== undefined ? { text: pendingText(pending), pending: true } : !saved ? { tone: 'idle', text: t('board.off') } : item.state,
        disabled: busy || (item.disabled && component.writable && saved),
      }
    }
    // A saved layer whose component cannot be read switches through its setting alone.
    const running = system?.sources.some(source => source.sourceTypeId === layerId) === true
    const label = system?.sources.find(source => source.sourceTypeId === layerId)?.management.label ?? layerId
    return {
      key, entry: undefined, label, hint: '', relations: [], target: 'layer:' + layerId, onSwitch: run, disabled: busy,
      checked: pending ?? saved,
      state: pending !== undefined ? { text: pendingText(pending), pending: true } : !saved ? { tone: 'idle', text: t('board.off') } : running ? { tone: 'done', text: t('board.running') } : { tone: 'warning', text: t('board.notRunning') },
    }
  })

  // The other roles: enhancements, and any role a future component declares.
  const others = dashboard?.entries.filter(entry => !entry.roles.includes('strategy') && !entry.roles.includes('source')) ?? []
  const roles = [...new Set(others.map(entry => entry.roles.find(role => role !== 'strategy' && role !== 'source')!))]
    .sort((left, right) => rank(left) - rank(right) || left.localeCompare(right))
  const total = sourceItems.length + others.length
  const needle = query.trim().toLowerCase()
  const matches = (item: BoardItem): boolean => needle === '' || [item.label, item.hint, item.entry?.packageName ?? ''].some(text => text.toLowerCase().includes(needle))
  const openRow = (entry: MemoryPluginEntryView | undefined): (() => void) | undefined => entry === undefined ? undefined : () => pages.open(entry.entryId, 'configuration')
  // A gear marks a component with settings of its own: declared options, or settings it contributed.
  const configurable = (entry: MemoryPluginEntryView | undefined): boolean => entry !== undefined && (entry.fields.length > 0 || props.componentSettings?.has(entry.packageName) === true)
  const rows = (items: readonly BoardItem[]): JSX.Element[] => items.filter(matches).map(item => <BoardRow key={item.key} item={item} configurable={configurable(item.entry)} t={t} onOpen={openRow(item.entry)} />)
  const count = (items: readonly BoardItem[]): string => t('board.enabledCount', { on: items.filter(item => item.checked).length, total: items.length })
  const otherItems = others.map(entry => componentItem(entry, 'enhancement:' + entry.entryId))

  const groups = roles.map(role => {
    const items = otherItems.filter(item => item.entry!.roles.includes(role))
    const title = ROLE_GROUPS.find(group => group.role === role)?.title
    // Enhancements the composing main Strategy takes come first; ones for other
    // main Strategies wait in a closed group, unless one is switched on.
    const fits = (entry: MemoryPluginEntryView): boolean => role !== 'strategy-extension' || entry.enabled || changing.has(entry.entryId) || enhancementApplies(entry, model?.composing ?? model?.selected)
    const shown = items.filter(item => fits(item.entry!))
    const later = items.filter(item => !fits(item.entry!))
    const visible = rows(shown)
    const hidden = rows(later)
    if (needle !== '' && visible.length === 0 && hidden.length === 0) return null
    return <div key={role} className={css.boardGroup}>
      <div className={css.boardGroupHead}><h3>{title === undefined ? role : t(title)}</h3><span>{count(items)}</span></div>
      {visible}
      {later.length > 0 && (needle === ''
        ? <div className={css.boardOthers}>
          <button type="button" className={css.boardOthersToggle} aria-expanded={othersOpen} onClick={() => setOthersOpen(value => !value)}>
            <IconChevronDownOutlineRegular size={12} />
            <span>{t('board.otherEnhancements', { count: later.length })}</span>
          </button>
          <Reveal>{othersOpen ? <div className={css.boardOthersList}>{hidden}</div> : null}</Reveal>
        </div>
        : hidden)}
    </div>
  })
  const sourceRows = rows(sourceItems)

  const mainState = (entry: MemoryPluginEntryView): ComponentState => {
    const pending = changing.get(entry.entryId)
    if (pending !== undefined) return { text: pendingText(pending), pending: true }
    if (entry === model?.composing) return { tone: 'done', text: t('board.composing') }
    return entry.enabled ? { tone: 'warning', text: t('board.notRunning') } : { tone: 'idle', text: t('board.off') }
  }
  /** A component's page: its state and control, as its row has them, and what it declares and contributed. */
  const pageOf = (entry: MemoryPluginEntryView, board: MemoryViewDashboard): Omit<ComponentDetailsProps, 'onClose' | 'onOpen' | 'back'> => {
    const isMain = entry.roles.includes('strategy')
    const item = isMain ? undefined : [...sourceItems, ...otherItems].find(candidate => candidate.entry === entry)
    const control = isMain
      ? <MainChoice selected={entry.typeId === board.strategyTypeId} disabled={locked || !entry.writable} t={t} onChoose={() => { void chooseMain(view.store, board, entry) }} />
      : item === undefined ? null : <Switch className={css.switch} checked={item.checked} label={item.label} disabled={item.disabled} onChange={item.onSwitch} />
    return {
      entry, dashboard: board, name, hint: componentCopy(entry, language).hint,
      state: isMain ? mainState(entry) : item?.state, control, writable: !locked, applying: working?.key === 'options:' + entry.entryId, language, t,
      settings: props.componentSettings?.has(entry.packageName) === true ? props.componentSettings.render(entry, !props.readOnly) : undefined,
      onApply: config => view.store.configure('options:' + entry.entryId, entry, config),
    }
  }
  const openedId = pages.trail.at(-1)
  const open = openedId === undefined || dashboard === null ? undefined : dashboard.entries.find(entry => entry.entryId === openedId)
  const previous = pages.trail.length < 2 || dashboard === null ? undefined : dashboard.entries.find(entry => entry.entryId === pages.trail.at(-2))
  const details: ReactNode = open === undefined || dashboard === null ? null : <ComponentDetails {...pageOf(open, dashboard)}
    {...(previous === undefined ? {} : { back: { name: name(previous), go: pages.back } })}
    onOpen={other => pages.open(other.entryId, 'page')}
    onClose={pages.close} />

  // DSH's row page shows the component's own page in place; a related name opens over it.
  if (pageMode) {
    const own = dashboard?.entries.find(entry => entry.packageName === props.page)
    return <section className={css.componentRowPage} aria-busy={working !== null}>
      {own === undefined || dashboard === null
        ? <p className={css.boardEmpty} role="status">{t('board.componentsUnavailable')}</p>
        : <ComponentPage {...pageOf(own, dashboard)} headed onOpen={other => pages.open(other.entryId, 'configuration')} />}
      {details}
    </section>
  }

  return frame(<>
    <Reveal>{problem === undefined ? null : <Callout tone={problem.tone} className={css.boardProblem} title={problem.text}
      {...(problem.actions.length === 0 ? {} : { actions: problem.actions.map(action => <ActionButton key={action.key} label={action.label} disabled={locked}
        {...(action.primary === true ? { primary: true } : {})} onClick={action.run} />) })} />}</Reveal>
    {model !== undefined && <MainStrategyRow {...props} model={model} dashboard={dashboard!} locked={locked} name={name}
      pending={working?.key.startsWith('strategy:') === true} onOpen={model.selected === undefined ? undefined : () => pages.open(model.selected!.entryId, 'configuration')} />}
    {total > SEARCH_FROM && <label className={css.boardSearch}>
      <IconSearchOutlineRegular size={14} />
      <input type="search" value={query} placeholder={t('board.search')} aria-label={t('board.search')} onChange={event => setQuery(event.target.value)} />
    </label>}
    {sourceRows.length > 0 ? <div className={css.boardGroup}>
      <div className={css.boardGroupHead}><h3>{t('board.sources')}</h3><span>{count(sourceItems)}</span></div>
      {sourceRows}
    </div> : needle === '' && props.systemPending === true ? placeholders(3) : null}
    {groups}
    {needle !== '' && sourceRows.length === 0 && groups.every(group => group === null) && <p className={css.boardEmpty}>{t('board.searchEmpty', { query: query.trim() })}</p>}
    <p className={css.boardFootnote}>{t('board.more')}</p>
    {details}
  </>, working !== null)
}

function rank(role: string): number {
  const index = ROLE_GROUPS.findIndex(group => group.role === role)
  return index < 0 ? ROLE_GROUPS.length : index
}

/**
 * The main Strategy as a choice of one, the same way however many are
 * installed: a selector naming each with its description. Its gear opens the
 * selected Strategy's page, with its options and settings.
 */
function MainStrategyRow(props: CompositionBoardProps & { model: MemoryComponentModel; dashboard: MemoryViewDashboard; locked: boolean; pending: boolean; name: Name; onOpen: (() => void) | undefined }): JSX.Element | null {
  const { model, dashboard, t } = props
  const id = useId()
  const { mains, selected } = model
  if (mains.length === 0) return null
  const choose = (typeId: string): void => {
    const entry = mains.find(candidate => candidate.typeId === typeId)
    if (entry !== undefined) void chooseMain(props.view.store, dashboard, entry)
  }
  const hint = selected === undefined ? t('board.strategyMissing') : componentCopy(selected, props.language).hint
  return <div className={`${css.boardRow} ${css.boardMain}`} {...{ [TARGET]: 'strategy' }}>
    <div className={css.boardRowLine}>
      <div className={css.settingCopy}>
        <strong id={`${id}-title`}>{t('config.strategyTitle')}</strong>
        <small>{hint}</small>
      </div>
      <div className={css.boardControl}>
        {props.pending && <StateLabel state={{ text: t('board.switching'), pending: true }} />}
        {props.onOpen !== undefined && <SettingsGear label={t('details.open', { component: props.name(selected!) })} onOpen={props.onOpen} />}
        {mains.length === 1
          ? <span className={css.boardValue}>{props.name(mains[0]!)}</span>
          : <SettingSelect labelledBy={`${id}-title`} value={dashboard.strategyTypeId} disabled={props.locked} onChange={choose}
            options={mains.map(entry => ({ value: entry.typeId!, label: props.name(entry), detail: componentCopy(entry, props.language).hint, ...(entry.writable ? {} : { disabled: true }) }))} />}
      </div>
    </div>
  </div>
}

/** Whether memory is composed as chosen; when it is not, what is wrong and the one switch that fixes it. */
function problemOf(props: CompositionBoardProps, model: MemoryComponentModel, dashboard: MemoryViewDashboard, name: Name): Problem | undefined {
  const { view, system, t } = props
  const { selected, composing, idle, contenders } = model
  const choose = (entry: MemoryPluginEntryView) => (): void => { void chooseMain(view.store, dashboard, entry) }
  const enableSelected = selected !== undefined && !selected.enabled
    ? [{ key: 'strategy:' + selected.entryId, label: t('config.enableStrategy', { strategy: name(selected) }), primary: true, run: choose(selected) }]
    : []
  if (composing === undefined) {
    const text = selected === undefined ? t('board.noMainMissing')
      : contenders.length > 0 ? t('board.contenders', { strategy: name(selected), strategies: nameList(contenders.map(name), t) })
        : !selected.enabled ? t('board.noMainOff', { strategy: name(selected) })
          : t('board.noMainWaiting', { strategy: name(selected) })
    const alternatives = selected === undefined ? model.mains.filter(entry => entry.writable).slice(0, 2).map(entry => ({
      key: 'strategy:' + entry.entryId, label: t('config.useStrategy', { strategy: name(entry) }), run: choose(entry),
    })) : []
    return { tone: 'error', text, actions: [...enableSelected, ...alternatives] }
  }
  if (composing !== selected) {
    return {
      tone: 'warning',
      text: selected === undefined ? t('board.fallbackMissing', { composing: name(composing) }) : t('board.fallback', { selected: name(selected), composing: name(composing) }),
      actions: [...enableSelected, { key: 'strategy:' + composing.entryId, label: t('config.useStrategy', { strategy: name(composing) }), run: choose(composing) }],
    }
  }
  if (system !== null && !system.serving) {
    // A main Strategy runs, yet nothing is composed: most often no Source runs.
    const missing = dashboard.entries.find(entry => entry.roles.includes('source') && !entry.enabled && entry.writable)
    const noSource = system.evaluation.diagnostics.some(diagnostic => diagnostic.code === 'missing-source')
    return {
      tone: 'error', text: t(noSource ? 'board.noSource' : 'board.memoryOff'),
      actions: missing === undefined ? [] : [{ key: 'component:' + missing.entryId, label: t('config.enableComponentNamed', { component: name(missing) }), primary: true,
        run: () => { void switchComponent(view.store, dashboard, missing, true) } }],
    }
  }
  if (system !== null && system.evaluation.state !== 'ready') return { tone: 'warning', text: t('board.stale'), actions: [] }
  if (idle.length > 0) {
    const names = nameList(idle.map(name), t)
    return {
      tone: 'warning', text: t('board.idle', { strategies: names }),
      actions: [{ key: 'strategy:idle', label: t('config.stopStrategies', { strategies: names }),
        run: () => { void view.store.setEnabled('strategy:idle', idle.map(entry => ({ entry, enabled: false }))) } }],
    }
  }
  return undefined
}
