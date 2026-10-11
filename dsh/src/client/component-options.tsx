import { useId, useState, type JSX } from 'react'
import { Checkbox } from '@deepseek-ai/dsh-client-ui-primitives'
import type { MemoryJsonValue } from '../core/contracts/index.ts'
import type { MemoryPluginEntryView, MemoryViewDashboard } from '../host/view-protocol.ts'
import { componentCopy } from './component-copy.ts'
import type { MnemonTranslate } from './locales.ts'
import css from './MnemonSettingsCard.module.css'
import { PanelActions } from './settings-panel.tsx'

type Field = MemoryPluginEntryView['fields'][number]
type Config = Record<string, MemoryJsonValue>

/** The Host's limits on a list option, from the configuration contract. */
const LIST_ITEMS = 32
const LIST_ITEM_LENGTH = 500
const TEXT_LENGTH = 4000

/**
 * One option as the panel stages it: the text or the chosen Sources, and
 * whether applying keeps it as the component's own value. An option left at
 * its default shows that default and is not written.
 */
interface Staged {
  text: string
  sources: string[]
  own: boolean
}

function listOf(value: MemoryJsonValue | undefined): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : []
}

function staged(field: Field, value: MemoryJsonValue | undefined, own: boolean): Staged {
  const shown = value ?? field.defaultValue
  const text = field.input === 'string-list' ? listOf(shown).join('\n') : typeof shown === 'number' || typeof shown === 'string' ? String(shown) : ''
  return { text, sources: field.input === 'source-list' ? listOf(shown) : [], own }
}

function initial(entry: MemoryPluginEntryView): Record<string, Staged> {
  return Object.fromEntries(entry.fields.map(field => [field.key, staged(field, entry.config[field.key], Object.hasOwn(entry.config, field.key))]))
}

/** The value an option writes, or why the Host would refuse it. */
function parse(field: Field, value: Staged, t: MnemonTranslate): { value: MemoryJsonValue } | { error: string } {
  if (field.input === 'number') {
    const number = Number(value.text.trim())
    const valid = value.text.trim() !== '' && Number.isInteger(number)
      && (field.minimum === undefined || number >= field.minimum) && (field.maximum === undefined || number <= field.maximum)
    if (valid) return { value: number }
    return { error: field.minimum !== undefined && field.maximum !== undefined ? t('options.between', { min: field.minimum, max: field.maximum })
      : field.minimum !== undefined ? t('options.atLeast', { min: field.minimum })
        : field.maximum !== undefined ? t('options.atMost', { max: field.maximum }) : t('options.integer') }
  }
  if (field.input === 'string-list' || field.input === 'source-list') {
    const items = field.input === 'source-list' ? value.sources : value.text.split('\n').map(item => item.trim()).filter(item => item !== '')
    const valid = items.length <= LIST_ITEMS && items.every(item => item.length <= LIST_ITEM_LENGTH) && new Set(items).size === items.length
    return valid ? { value: items } : { error: t('options.listLimit', { count: LIST_ITEMS, length: LIST_ITEM_LENGTH }) }
  }
  const maximum = field.maximum ?? TEXT_LENGTH
  return value.text.length <= maximum ? { value: value.text } : { error: t('options.tooLong', { max: maximum }) }
}

export interface ComponentOptionsProps {
  entry: MemoryPluginEntryView
  /** The component's name as the page shows it. */
  name: string
  /** The running Source instances a Source list chooses from, named by their components. */
  dashboard: Pick<MemoryViewDashboard, 'sources' | 'entries'>
  language: string
  t: MnemonTranslate
  disabled: boolean
  /** While the options cross the wire. */
  pending: boolean
  onApply: (config: Config) => Promise<boolean>
}

/**
 * A component's options, drawn from the fields it declares, so an installed
 * Strategy or enhancement brings its own settings. Typed values wait for
 * Apply, which appears once something changed; an option left at its default
 * is not written, and one set here can be put back to its default.
 */
export function ComponentOptions(props: ComponentOptionsProps): JSX.Element {
  const { entry, t } = props
  const id = useId()
  const [draft, setDraft] = useState(() => initial(entry))
  const zh = props.language.toLowerCase().startsWith('zh')
  const text = (value: { en: string; 'zh-CN': string } | undefined): string => value === undefined ? '' : zh ? value['zh-CN'] : value.en
  const results = entry.fields.map(field => ({ field, result: parse(field, draft[field.key]!, t) }))
  const invalid = results.some(({ field, result }) => draft[field.key]!.own && 'error' in result)
  const config: Config = Object.fromEntries(results.flatMap(({ field, result }) => draft[field.key]!.own && 'value' in result ? [[field.key, result.value]] : []))
  // Any edit shows Apply, a refused value included, so the page can say what to fix.
  const edited = JSON.stringify(draft) !== JSON.stringify(initial(entry))
  const edit = (field: Field, next: Partial<Staged>): void => setDraft(current => ({ ...current, [field.key]: { ...current[field.key]!, ...next, own: true } }))
  const reset = (field: Field): void => setDraft(current => ({ ...current, [field.key]: staged(field, undefined, false) }))
  const locked = props.disabled || props.pending
  // A Source is named by the component that registered it, as everywhere else.
  const sourceName = (sourceTypeId: string, fallback: string): string => {
    const component = props.dashboard.entries.find(candidate => candidate.roles.includes('source') && candidate.typeId === sourceTypeId)
    return component === undefined ? fallback : componentCopy(component, props.language).label
  }

  return <div className={css.optionsPanel} role="group" aria-label={t('options.aria', { component: props.name })}>
    {results.map(({ field, result }) => {
      const value = draft[field.key]!
      const control = `${id}-${field.key}`
      const error = value.own && 'error' in result ? result.error : undefined
      const description = text(field.description)
      const hint = error ?? [description, field.input === 'string-list' ? t('options.listHint') : ''].filter(part => part !== '').join(t('config.sentenceGap'))
      const describedBy = hint === '' ? undefined : `${control}-hint`
      return <div key={field.key} className={css.optionField}>
        <div className={css.optionLabel}>
          <label htmlFor={field.input === 'source-list' ? undefined : control} id={`${control}-label`}>{text(field.label)}</label>
          {value.own
            ? <button type="button" className={css.optionReset} disabled={locked} onClick={() => reset(field)}>{t('options.reset')}</button>
            : <span className={css.optionDefault}>{t('options.default')}</span>}
        </div>
        {field.input === 'source-list'
          ? <SourceChoice field={field} value={value} sources={props.dashboard.sources} name={sourceName} labelledBy={`${control}-label`} disabled={locked} t={t} onChange={sources => edit(field, { sources })} />
          : field.input === 'textarea' || field.input === 'string-list'
            ? <textarea id={control} className={css.optionInput} rows={field.input === 'string-list' ? 3 : 4} value={value.text} disabled={locked}
              aria-invalid={error !== undefined} aria-describedby={describedBy} onChange={event => edit(field, { text: event.target.value })} />
            : <input id={control} className={css.optionInput} type="text" inputMode={field.input === 'number' ? 'numeric' : undefined} value={value.text} disabled={locked}
              aria-invalid={error !== undefined} aria-describedby={describedBy} onChange={event => edit(field, { text: event.target.value })} />}
        {describedBy !== undefined && <small id={describedBy} className={css.optionHint} data-error={error === undefined ? undefined : ''}>{hint}</small>}
      </div>
    })}
    {/* A refused save keeps the edits; the page's notice says why. */}
    <PanelActions dirty={edited} saving={props.pending} invalid={invalid ? t('options.fix') : null} failed={null} applied={false}
      disabled={props.disabled} t={t} onDiscard={() => setDraft(initial(entry))} onApply={() => { void props.onApply(config) }} />
  </div>
}

/** Sources a Strategy may use, in the order chosen; ones no longer running stay listed so they can be removed. */
function SourceChoice(props: { field: Field; value: Staged; sources: MemoryViewDashboard['sources']; name: (sourceTypeId: string, fallback: string) => string; labelledBy: string; disabled: boolean; t: MnemonTranslate; onChange: (sources: string[]) => void }): JSX.Element {
  const { field, value, t } = props
  const eligible = props.sources.filter(source => field.sourceRoles === undefined || field.sourceRoles.length === 0 || field.sourceRoles.includes(source.role))
  const missing = value.sources.filter(key => !eligible.some(source => source.sourceInstanceKey === key))
  const several = (sourceTypeId: string): boolean => eligible.filter(source => source.sourceTypeId === sourceTypeId).length > 1
  const toggle = (key: string, on: boolean): void => props.onChange(on ? [...value.sources, key] : value.sources.filter(item => item !== key))
  if (eligible.length === 0 && missing.length === 0) return <p className={css.optionEmpty}>{t('options.noSources')}</p>
  return <div className={css.optionChoices} role="group" aria-labelledby={props.labelledBy}>
    {eligible.map(source => {
      const label = props.name(source.sourceTypeId, source.label)
      return <Checkbox key={source.sourceInstanceKey} checked={value.sources.includes(source.sourceInstanceKey)} disabled={props.disabled}
        label={several(source.sourceTypeId) ? `${label} · ${source.sourceInstanceKey}` : label} onChange={on => toggle(source.sourceInstanceKey, on)} />
    })}
    {missing.map(key => <Checkbox key={key} checked disabled={props.disabled} label={t('options.sourceUnavailable', { source: key })} onChange={on => toggle(key, on)} />)}
  </div>
}
