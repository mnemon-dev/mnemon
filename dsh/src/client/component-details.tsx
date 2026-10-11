import type { JSX, ReactNode } from 'react'
import { Button, IconChevronLeftOutlineRegular, Modal, StateDot, Tag, TextShimmer, type StateDotState } from '@deepseek-ai/dsh-client-ui-primitives'
import type { MemoryJsonValue } from '../core/contracts/index.ts'
import type { MemoryPluginEntryView, MemoryViewDashboard } from '../host/view-protocol.ts'
import { isShipped, nameList } from './component-copy.ts'
import { relationsOf, switchPlan } from './component-model.ts'
import { ComponentOptions } from './component-options.tsx'
import type { MnemonTranslate } from './locales.ts'
import css from './MnemonSettingsCard.module.css'

/** A component's state as its row shows it. */
export interface ComponentState {
  tone?: StateDotState | undefined
  text: string
  pending?: boolean | undefined
}

export interface ComponentDetailsProps {
  entry: MemoryPluginEntryView
  dashboard: MemoryViewDashboard
  name: (entry: MemoryPluginEntryView) => string
  hint: string
  state: ComponentState | undefined
  /** The row's own control: its switch, or for a main Strategy the choice of it. */
  control: ReactNode
  /** Whether the options can be written now. */
  writable: boolean
  /** While this component's options cross the wire. */
  applying: boolean
  language: string
  t: MnemonTranslate
  /** The settings the component contributed, rendered after what its declarations give. */
  settings?: ReactNode | undefined
  /** The page this one was opened from, to go back to. */
  back?: { name: string; go: () => void } | undefined
  /** The page's container already heads it with the component's name, package and description, as DSH's row page does. */
  headed?: boolean | undefined
  /** Open a related component's page in place of this one. */
  onOpen: (entry: MemoryPluginEntryView) => void
  /** Save the options; resolves whether they were saved. The page stays open. */
  onApply: (config: Record<string, MemoryJsonValue>) => Promise<boolean>
  onClose: () => void
}

/** A component's page in a dialog over the configuration, the way the board opens it. */
export function ComponentDetails(props: ComponentDetailsProps): JSX.Element {
  return <Modal open onClose={props.onClose} title={props.name(props.entry)} description={props.hint} closeLabel={props.t('common.close')} className={css.detailsDialog ?? ''} contentClassName={css.detailsScroll ?? ''}>
    <ComponentPage {...props} />
  </Modal>
}

/**
 * Everything one component brings, in one place: what it is and where it
 * came from, how it relates to the other components installed and what its
 * switch would move, the options it declares and the settings it contributed.
 * Relations and options are drawn from the component's own declarations, so an
 * installed extension gets the same page as a shipped component. The same
 * page shows in a dialog over the configuration and on DSH's own row page.
 */
export function ComponentPage(props: Omit<ComponentDetailsProps, 'onClose'>): JSX.Element {
  const { entry, dashboard, name, t } = props
  const relations = relationsOf(dashboard, entry)
  const names = (entries: readonly MemoryPluginEntryView[]): string => nameList(entries.map(name), t)
  // What the switch would move beyond the component itself.
  const plan = entry.roles.includes('strategy') ? undefined : switchPlan(dashboard, entry, !entry.enabled)
  const others = plan?.changes?.filter(change => change.entry !== entry) ?? []
  const on = names(others.filter(change => change.enabled).map(change => change.entry))
  const off = names(others.filter(change => !change.enabled).map(change => change.entry))
  const effect = plan?.blocked === 'last-source' ? t('details.effectLast')
    : entry.enabled ? off === '' ? undefined : t('details.effectOff', { names: off })
      : on === '' && off === '' ? undefined : off === '' ? t('details.effectOn', { names: on }) : on === '' ? t('details.effectOnReplace', { names: off }) : t('details.effectOnMixed', { on, off })
  const needs = relations.needs.filter(need => need.providers.length > 0 || !need.met)
  const hasRelations = needs.length > 0 || relations.neededBy.length > 0 || relations.conflictsWith.length > 0 || effect !== undefined
  // Every component named here opens its own page, as the names on the board do.
  const link = (other: MemoryPluginEntryView, on: boolean): JSX.Element => <button key={other.entryId} type="button" className={css.detailsName} onClick={() => props.onOpen(other)}>
    <StateDot state={on ? 'done' : 'idle'} /><span>{name(other)}</span>
  </button>

  return <div className={`${css.surface} ${css.detailsBody}`}>
    {props.back !== undefined && <button type="button" className={css.detailsBack} onClick={props.back.go}>
      <IconChevronLeftOutlineRegular size={12} aria-hidden="true" />{t('details.back', { component: props.back.name })}
    </button>}
    <div className={css.detailsHead}>
      <div className={css.detailsHeadLine}>
        {props.state !== undefined && <span className={css.boardState} data-tone={props.state.pending === true ? 'ongoing' : props.state.tone}>
          {props.state.pending === true
            ? <><StateDot state="ongoing" /><TextShimmer active>{props.state.text}</TextShimmer></>
            : <>{props.state.tone !== undefined && <StateDot state={props.state.tone} />}<span>{props.state.text}</span></>}
        </span>}
        <Tag tone={isShipped(entry) ? 'neutral' : 'info'}>{t(isShipped(entry) ? 'details.shipped' : 'details.installed')}</Tag>
        <span className={css.detailsControl}>{props.control}</span>
      </div>
      {props.headed !== true && <code className={css.detailsPackage}>{entry.packageName}</code>}
    </div>
    {hasRelations && <section className={css.detailsSection} aria-label={t('details.relations')}>
      <h3>{t('details.relations')}</h3>
      <dl className={css.detailsRelations}>
        {needs.map(need => <div key={need.requirement}>
          <dt>{t(need.providers.length > 1 ? 'details.needsAny' : 'details.needs')}</dt>
          <dd>{need.providers.length === 0
            ? <span className={css.detailsMissing}>{t('details.needsMissing', { capability: need.requirement })}</span>
            : need.providers.map(provider => link(provider, provider.enabled))}</dd>
        </div>)}
        {relations.neededBy.length > 0 && <div>
          <dt>{t('details.neededBy')}</dt>
          <dd>{relations.neededBy.map(dependent => link(dependent, true))}</dd>
        </div>}
        {relations.conflictsWith.length > 0 && <div>
          <dt>{t('details.conflicts')}</dt>
          <dd>{relations.conflictsWith.map(other => link(other, other.enabled))}</dd>
        </div>}
      </dl>
      {effect !== undefined && <p className={css.detailsEffect}>{effect}</p>}
    </section>}
    {entry.fields.length > 0 && <section className={css.detailsSection} aria-label={t('details.options')}>
      <h3>{t('details.options')}</h3>
      <ComponentOptions key={entry.entryId + JSON.stringify(entry.config)} entry={entry} name={name(entry)} dashboard={dashboard} language={props.language} t={t}
        disabled={!props.writable || !entry.writable} pending={props.applying} onApply={props.onApply} />
    </section>}
    {props.settings !== undefined && props.settings !== null && <div className={css.detailsContributed}>{props.settings}</div>}
  </div>
}

/** The choice of a main Strategy inside its page: the current one says so, another offers to switch. */
export function MainChoice(props: { selected: boolean; disabled: boolean; t: MnemonTranslate; onChoose: () => void }): JSX.Element {
  return props.selected
    ? <Tag tone="solid">{props.t('details.currentMain')}</Tag>
    : <Button variant="outline" size="sm" disabled={props.disabled} onClick={props.onChoose}>{props.t('details.useMain')}</Button>
}
