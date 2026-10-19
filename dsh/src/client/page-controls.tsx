import { useId, useState, type InputHTMLAttributes, type JSX } from 'react'
import { Button, IconChevronDownOutlineRegular, IconSearchOutlineRegular, Input, Menu, Tag, type TagTone } from '@deepseek-ai/dsh-client-ui-primitives'
import { useT } from './page-kit.tsx'
import type { MnemonKey, MnemonTranslate } from './locales.ts'
import css from './PageControls.module.css'

/**
 * Controls every memory page shares, so a search, a choice or a write receipt
 * looks and behaves the same on each page: the DSH input and its search icon,
 * the DSH selector and its menu, and one receipt for a task Agent's write.
 */

/** A search box: the DSH input with its search icon. */
export function SearchField({ label, className, ...input }: { label: string; className?: string | undefined } & InputHTMLAttributes<HTMLInputElement>): JSX.Element {
  return <Input {...input} aria-label={label} icon={<IconSearchOutlineRegular size={16} />} className={[css.search, className].filter(Boolean).join(' ')} />
}

/** One choice of a {@link SelectField}; the detail explains it in the menu. */
export interface FieldOption<T extends string> {
  value: T
  label: string
  detail?: string | undefined
  disabled?: boolean | undefined
}

/** A labelled choice: the DSH selector, opening a menu of the options. */
export function SelectField<T extends string>(props: {
  label: string
  value: T
  options: readonly FieldOption<T>[]
  disabled?: boolean | undefined
  /** Keep the label for assistive technology only, when the row already says it. */
  hideLabel?: boolean | undefined
  /** `sm` for compact toolbars and headers, as the DSH small button. */
  size?: 'md' | 'sm' | undefined
  /** Label beside the selector instead of above it. */
  inline?: boolean | undefined
  /** A fuller name for assistive technology than the visible label. */
  ariaLabel?: string | undefined
  className?: string | undefined
  onChange: (value: T) => void
}): JSX.Element {
  const [open, setOpen] = useState(false)
  const labelId = useId()
  const nameId = useId()
  const valueId = useId()
  const current = props.options.find(option => option.value === props.value)
  return <span className={[css.field, props.className].filter(Boolean).join(' ')} data-inline={props.inline === true ? '' : undefined} data-size={props.size === 'sm' ? 'sm' : undefined}>
    <span id={labelId} data-part="label" className={props.hideLabel === true ? css.hiddenLabel : css.fieldLabel} aria-hidden={props.ariaLabel === undefined ? undefined : true}>{props.label}</span>
    {props.ariaLabel !== undefined && <span id={nameId} className={css.hiddenLabel}>{props.ariaLabel}</span>}
    <Menu
      open={open}
      onClose={() => setOpen(false)}
      align="start"
      portal
      selectedId={props.value}
      className={css.menuRoot}
      listClassName={css.menu}
      items={props.options.map(option => ({
        id: option.value,
        label: option.detail === undefined ? option.label : <span className={css.option}><span>{option.label}</span><small>{option.detail}</small></span>,
        ...(option.disabled === true ? { disabled: true } : {}),
      }))}
      onSelect={id => { setOpen(false); if (id !== props.value) props.onChange(id as T) }}
      anchor={<button type="button" className={css.selector} aria-haspopup="menu" aria-expanded={open} aria-labelledby={`${props.ariaLabel === undefined ? labelId : nameId} ${valueId}`} disabled={props.disabled === true} onClick={() => setOpen(value => !value)}>
        <span id={valueId}>{current?.label ?? props.value}</span>
        <IconChevronDownOutlineRegular size={12} />
      </button>}
    />
  </span>
}

/** How a task Agent's write ended, as the receipt names it. */
export type WriteOutcome = 'written' | 'updated' | 'pending' | 'skipped' | 'partial' | 'unfinished'

const OUTCOMES: Readonly<Record<string, WriteOutcome>> = {
  stored: 'written', added: 'written', created: 'written', merged: 'written', linked: 'written',
  updated: 'updated', replaced: 'updated',
  accepted: 'pending', candidate: 'pending',
  skipped: 'skipped',
  partial: 'partial',
}

/** Map a write action from a receipt to what the user reads. */
export function writeOutcome(action: string): WriteOutcome {
  return OUTCOMES[action] ?? 'unfinished'
}

const TONES: Readonly<Record<WriteOutcome, TagTone>> = {
  written: 'success', updated: 'success', pending: 'info', skipped: 'neutral', partial: 'warning', unfinished: 'warning',
}
const LABELS: Readonly<Record<WriteOutcome, MnemonKey>> = {
  written: 'receipt.written', updated: 'receipt.updated', pending: 'receipt.pending',
  skipped: 'receipt.skipped', partial: 'receipt.partial', unfinished: 'receipt.unfinished',
}

/**
 * What a task Agent did with a candidate: the outcome, the Agent's own
 * summary, and, when something was written, a way to see it.
 */
export function WriteReceipt(props: { action?: string | undefined; summary?: string | undefined; error?: string | undefined; t?: MnemonTranslate | undefined; onView?: (() => void) | undefined }): JSX.Element {
  const shared = useT()
  const t = props.t ?? shared
  if (props.error !== undefined) {
    return <div className={css.receipt} role="alert" data-outcome="failed">
      <Tag tone="danger">{t('receipt.failed')}</Tag>
      <p>{props.error}</p>
    </div>
  }
  const outcome = writeOutcome(props.action ?? '')
  const viewable = outcome !== 'skipped' && outcome !== 'unfinished'
  return <div className={css.receipt} role="status" data-outcome={outcome}>
    <Tag tone={TONES[outcome]}>{t(LABELS[outcome])}</Tag>
    {props.summary !== undefined && props.summary !== '' && <p>{props.summary}</p>}
    {props.onView !== undefined && viewable && <Button variant="ghost" size="sm" className={css.receiptAction} onClick={props.onView}>{t('receipt.view')}</Button>}
  </div>
}

/** Whether a task Agent can take a write now. */
export function TaskAgentTag(props: { available: boolean; t?: MnemonTranslate | undefined }): JSX.Element {
  const shared = useT()
  const t = props.t ?? shared
  return <Tag tone={props.available ? 'success' : 'warning'}>{t(props.available ? 'taskAgent.ready' : 'taskAgent.unavailable')}</Tag>
}
