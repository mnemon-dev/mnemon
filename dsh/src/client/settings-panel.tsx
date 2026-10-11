import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type JSX } from 'react'
import { Button, TextShimmer } from '@deepseek-ai/dsh-client-ui-primitives'
import type { ClientSettingsScope, ClientSettingsSnapshot } from '../host/protocol.ts'
import type { MnemonTranslate } from './locales.ts'
import css from './MnemonSettingsCard.module.css'
import { message } from './page-kit.tsx'

/**
 * The two ways a group of settings saves. A choice (switch or selector)
 * applies when it is made; edits that need checking, or that move data, wait
 * for their group's Apply. No group joins another group's save.
 */

export function useScope<T>(scope: ClientSettingsScope<T>): ClientSettingsSnapshot<T> {
  const subscribe = useMemo(() => scope.subscribe.bind(scope), [scope])
  const getSnapshot = useMemo(() => scope.getSnapshot.bind(scope), [scope])
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}

/**
 * One group's staged edits, applied together with its own button. Edits that
 * come back to the saved values leave nothing to apply.
 */
export function useStaged<D>(value: D, write: (draft: D) => Promise<void>) {
  const [draft, setDraft] = useState(value)
  const [edited, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [applied, setApplied] = useState(false)
  const serialized = JSON.stringify(value)
  const dirty = edited && JSON.stringify(draft) !== serialized
  useEffect(() => { if (!dirty) setDraft(value) }, [serialized])
  return {
    draft, dirty, saving, failed, applied,
    edit: (next: Partial<D>): void => { setDraft(current => ({ ...current, ...next })); setDirty(true); setFailed(null); setApplied(false) },
    discard: (): void => { setDraft(value); setDirty(false); setFailed(null) },
    apply: async (): Promise<void> => {
      setSaving(true); setFailed(null)
      try {
        await write(draft)
        setDirty(false); setApplied(true)
      } catch (reason) {
        setFailed(message(reason))
      } finally {
        setSaving(false)
      }
    },
  }
}

/**
 * A choice that saves when it is made: it shows the chosen value at once, and
 * the saved one again if the write fails.
 */
export function useLive<T>(saved: T, write: (value: T) => Promise<void>) {
  const [pending, setPending] = useState<{ value: T } | null>(null)
  const [failed, setFailed] = useState<string | null>(null)
  const request = useRef(0)
  return {
    value: pending === null ? saved : pending.value,
    failed,
    set: (value: T): void => {
      const current = ++request.current
      setPending({ value }); setFailed(null)
      void write(value).then(
        () => { if (request.current === current) setPending(null) },
        reason => { if (request.current === current) { setPending(null); setFailed(message(reason)) } },
      )
    },
  }
}

/** A failed write, beside the controls that made it. */
export function WriteFailure(props: { error: string | null; t: MnemonTranslate }): JSX.Element | null {
  return props.error === null ? null : <p className={css.panelError} role="alert">{props.t('config.saveFailed', { error: props.error })}</p>
}

/** Discard and Apply for one group, shown while it has edits; `note` says what applying does. */
export function PanelActions(props: { dirty: boolean; saving: boolean; invalid: string | null; failed: string | null; applied: boolean; disabled: boolean; note?: string; t: MnemonTranslate; onDiscard: () => void; onApply: () => void }): JSX.Element | null {
  const { t } = props
  if (!props.dirty) return props.applied ? <p className={css.panelNote} role="status">{t('config.ready')}</p> : null
  const problem = props.invalid ?? (props.failed === null ? null : t('config.saveFailed', { error: props.failed }))
  return <div className={css.panelActions}>
    <span role={problem === null ? undefined : 'alert'} className={problem === null ? undefined : css.panelError}>{problem ?? props.note ?? t('config.unsaved')}</span>
    <Button variant="ghost" size="sm" disabled={props.saving} onClick={props.onDiscard}>{t('config.discard')}</Button>
    <Button variant="primary" size="sm" disabled={props.saving || props.disabled || props.invalid !== null} aria-busy={props.saving || undefined} onClick={props.onApply}>
      {props.saving ? <TextShimmer active>{t('options.applying')}</TextShimmer> : t('options.apply')}
    </Button>
  </div>
}
