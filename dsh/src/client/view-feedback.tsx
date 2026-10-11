import { useCallback, useEffect, useRef, type ReactNode, type RefObject } from 'react'
import type { MemoryPluginEntryView } from '../host/view-protocol.ts'
import { announces, describeChange, primaryOf } from './component-feedback.ts'
import { componentCopy } from './component-copy.ts'
import { locate, useToast } from './feedback.tsx'
import type { MnemonTranslate } from './locales.ts'
import type { MnemonViewState, MnemonViewStore } from './view-store.ts'

/**
 * Say what each component change did, where the user is looking, and point at
 * the rows it touched: this page's own writes with Undo, changes made
 * elsewhere (DSH's component list, another window) with Show, refused writes
 * with Retry.
 * @param root the page, searched for the rows a change touched.
 * @returns the toast to render.
 */
export function useViewFeedback(view: { store: MnemonViewStore; state: MnemonViewState }, root: RefObject<HTMLElement | null>, t: MnemonTranslate, language: string): ReactNode {
  const toast = useToast()
  const show = toast.show
  const name = useCallback((entry: MemoryPluginEntryView) => componentCopy(entry, language).label, [language])
  const { store, state } = view
  const seenChange = useRef(state.change?.seq ?? 0)
  useEffect(() => {
    const change = state.change
    if (change === null || change.seq === seenChange.current) return
    seenChange.current = change.seq
    // A plain switch shows its result in its own row; say only what goes beyond it.
    if (!announces(change)) return
    const summary = describeChange(change.before, change.after, change.origin, name, t, primaryOf(change))
    if (summary === undefined) return
    // The rows it touched; the composition itself only when asked to show it.
    locate(root.current, summary.targets.filter(target => target !== 'overview'))
    // An undo puts back what the user just saw, so it only needs confirming.
    if (change.key?.startsWith('revert:') === true) {
      show({ text: t('feedback.undone'), tone: 'success' })
      return
    }
    show({
      text: summary.text,
      tone: summary.tone,
      actions: change.origin === 'own'
        ? [{ label: t('feedback.undo'), run: () => { void store.revert(change) } }]
        : [{ label: t('feedback.show'), run: () => locate(root.current, ['overview'], { scroll: true }) }],
    })
  }, [state.change, store, name, root, show, t])
  const seenFailure = useRef(state.failure?.seq ?? 0)
  useEffect(() => {
    const failure = state.failure
    if (failure === null || failure.seq === seenFailure.current) return
    seenFailure.current = failure.seq
    if (failure.kind === 'refresh') {
      show({ text: t('config.enhancementsRefreshFailed'), tone: 'warning' })
      return
    }
    const text = failure.key.startsWith('strategy:') ? t('config.strategyFailed') : t('feedback.failed')
    show({ text, tone: 'warning', actions: [{ label: t('feedback.retry'), run: () => { void store.retry() } }] })
  }, [state.failure, store, show, t])
  return toast.element
}
