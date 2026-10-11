import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  type JSX,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from 'react'
import { createPortal } from 'react-dom'
import { appearanceClass } from './view-styles.ts'
import { IconCloseOutlineRegular } from './ui-icons.ts'
import css from './MnemonView.module.css'
import sidebarCss from './MnemonSidebarView.module.css'

const SHEET_MEDIA = '(max-width: 760px)'
const REDUCED_MOTION_MEDIA = '(prefers-reduced-motion: reduce)'
const CLOSE_DURATION_MS = 240
const SNAP_DURATION_MS = 280

interface DragSession {
  pointerId: number
  captureTarget: HTMLDivElement
  startY: number
  initialOffset: number
  lastY: number
  lastTime: number
  velocity: number
  offset: number
  height: number
}

export interface MnemonDialogProps {
  title: string
  closeLabel: string
  description?: string
  busy?: boolean
  contentReady?: boolean
  wide?: boolean
  footer?: ReactNode
  onClose: () => void
  children: ReactNode
}

function matches(query: string): boolean {
  return typeof window.matchMedia === 'function' && window.matchMedia(query).matches
}

function currentTransform(element: HTMLElement): string {
  const value = window.getComputedStyle(element).transform
  return value === '' || value === 'none' ? 'translate3d(0, 0, 0)' : value
}

function currentTranslateY(element: HTMLElement): number {
  const transform = currentTransform(element)
  if (transform === 'translate3d(0, 0, 0)') return 0
  try {
    return Math.max(new DOMMatrixReadOnly(transform).m42, 0)
  } catch {
    const matrix3d = transform.match(/^matrix3d\((.+)\)$/)
    if (matrix3d !== null) return Math.max(Number(matrix3d[1]?.split(',')[13]) || 0, 0)
    const matrix = transform.match(/^matrix\((.+)\)$/)
    return matrix === null ? 0 : Math.max(Number(matrix[1]?.split(',')[5]) || 0, 0)
  }
}

function cancelAnimations(element: HTMLElement | null): void {
  if (element === null || typeof element.getAnimations !== 'function') return
  element.getAnimations().forEach(animation => animation.cancel())
}

function releaseDragCapture(drag: DragSession): void {
  try {
    if (typeof drag.captureTarget.hasPointerCapture !== 'function' || drag.captureTarget.hasPointerCapture(drag.pointerId)) {
      drag.captureTarget.releasePointerCapture?.(drag.pointerId)
    }
  } catch {
    // Pointer capture can already be gone after a native cancellation or app switch.
  }
}

function waitForAnimations(animations: Animation[], duration: number): Promise<void> {
  return new Promise(resolve => {
    let settled = false
    const finish = (): void => {
      if (settled) return
      settled = true
      window.clearTimeout(timeout)
      resolve()
    }
    const timeout = window.setTimeout(finish, duration + 100)
    void Promise.allSettled(animations.map(animation => animation.finished)).then(finish)
  })
}

/** Shared top-layer dialog behavior for every Mnemon workspace action surface. */
export function MnemonDialog(props: MnemonDialogProps): JSX.Element | null {
  const titleId = useId()
  const descriptionId = useId()
  const backdropRef = useRef<HTMLDivElement | null>(null)
  const dialogRef = useRef<HTMLElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const returnFocusRef = useRef<HTMLElement | null>(null)
  const dragRef = useRef<DragSession | null>(null)
  const snapGenerationRef = useRef(0)
  const closingRef = useRef(false)
  const mountedRef = useRef(true)
  const busyRef = useRef(props.busy === true)
  const onCloseRef = useRef(props.onClose)
  busyRef.current = props.busy === true
  onCloseRef.current = props.onClose

  const focusPreferredControl = useCallback(() => {
    const control = dialogRef.current?.querySelector<HTMLElement>('[data-autofocus]:not(:disabled)')
      ?? dialogRef.current?.querySelector<HTMLElement>('input:not(:disabled), textarea:not(:disabled), select:not(:disabled)')
    control?.focus({ preventScroll: true })
  }, [])

  const finishClose = useCallback(() => {
    if (mountedRef.current) onCloseRef.current()
  }, [])

  const requestClose = useCallback(() => {
    if (busyRef.current || closingRef.current) return
    const dialog = dialogRef.current
    const backdrop = backdropRef.current
    if (dialog === null || backdrop === null || matches(REDUCED_MOTION_MEDIA) || typeof dialog.animate !== 'function' || typeof backdrop.animate !== 'function') {
      finishClose()
      return
    }

    closingRef.current = true
    snapGenerationRef.current += 1
    dialog.dataset.closing = 'true'
    backdrop.dataset.closing = 'true'
    const sheet = matches(SHEET_MEDIA)
    const fromTransform = currentTransform(dialog)
    const fromBackdropOpacity = window.getComputedStyle(backdrop).opacity
    cancelAnimations(dialog)
    cancelAnimations(backdrop)
    const easing = sheet ? 'cubic-bezier(.4, 0, 1, 1)' : 'cubic-bezier(.4, 0, .2, 1)'
    const dialogAnimation = dialog.animate(sheet
      ? [
          { opacity: 1, transform: fromTransform },
          { opacity: 1, transform: 'translate3d(0, calc(100% + 32px), 0)' },
        ]
      : [
          { opacity: 1, transform: fromTransform },
          { opacity: 0, transform: 'translate3d(0, 8px, 0) scale(.985)' },
        ], { duration: CLOSE_DURATION_MS, easing, fill: 'forwards' })
    const backdropAnimation = backdrop.animate([{ opacity: fromBackdropOpacity }, { opacity: 0 }], { duration: CLOSE_DURATION_MS, easing: 'ease-out', fill: 'forwards' })
    void waitForAnimations([dialogAnimation, backdropAnimation], CLOSE_DURATION_MS).then(finishClose)
  }, [finishClose])

  const resetDrag = useCallback(() => {
    const dialog = dialogRef.current
    const backdrop = backdropRef.current
    if (dialog === null || backdrop === null) return
    const drag = dragRef.current
    const offset = drag?.offset ?? 0
    const snapGeneration = ++snapGenerationRef.current
    dragRef.current = null
    if (drag !== null) releaseDragCapture(drag)
    delete dialog.dataset.dragging
    if (offset <= 0 || matches(REDUCED_MOTION_MEDIA) || typeof dialog.animate !== 'function' || typeof backdrop.animate !== 'function') {
      dialog.style.removeProperty('--mn-modal-drag-y')
      backdrop.style.removeProperty('opacity')
      return
    }
    const dialogAnimation = dialog.animate([
      { transform: currentTransform(dialog) },
      { transform: 'translate3d(0, 0, 0)' },
    ], { duration: SNAP_DURATION_MS, easing: 'cubic-bezier(.2, .8, .2, 1)', fill: 'forwards' })
    const backdropAnimation = backdrop.animate([
      { opacity: window.getComputedStyle(backdrop).opacity },
      { opacity: 1 },
    ], { duration: SNAP_DURATION_MS, easing: 'ease-out', fill: 'forwards' })
    void waitForAnimations([dialogAnimation, backdropAnimation], SNAP_DURATION_MS).then(() => {
      if (!mountedRef.current || snapGenerationRef.current !== snapGeneration) return
      dialog.style.removeProperty('--mn-modal-drag-y')
      backdrop.style.removeProperty('opacity')
      dialogAnimation.cancel()
      backdropAnimation.cancel()
    })
  }, [])

  const beginDrag = (event: ReactPointerEvent<HTMLDivElement>): void => {
    if (busyRef.current || closingRef.current || !event.isPrimary || !matches(SHEET_MEDIA)) return
    if (event.pointerType === 'mouse' && event.button !== 0) return
    const dialog = dialogRef.current
    if (dialog === null) return
    snapGenerationRef.current += 1
    const initialOffset = currentTranslateY(dialog)
    const backdrop = backdropRef.current
    const initialBackdropOpacity = backdrop === null ? '' : window.getComputedStyle(backdrop).opacity
    cancelAnimations(dialog)
    cancelAnimations(backdrop)
    const time = event.timeStamp
    dragRef.current = {
      pointerId: event.pointerId,
      captureTarget: event.currentTarget,
      startY: event.clientY,
      initialOffset,
      lastY: event.clientY,
      lastTime: time,
      velocity: 0,
      offset: initialOffset,
      height: Math.max(dialog.getBoundingClientRect().height, 1),
    }
    dialog.dataset.dragging = 'true'
    dialog.style.setProperty('--mn-modal-drag-y', `${initialOffset}px`)
    if (backdrop !== null) backdrop.style.opacity = initialBackdropOpacity
    try { event.currentTarget.setPointerCapture?.(event.pointerId) } catch { /* Global pointer listeners keep the gesture alive. */ }
  }

  const moveDrag = useCallback((event: PointerEvent): void => {
    const drag = dragRef.current
    const dialog = dialogRef.current
    const backdrop = backdropRef.current
    if (drag === null || dialog === null || backdrop === null || drag.pointerId !== event.pointerId) return
    event.preventDefault()
    const rawOffset = Math.max(0, drag.initialOffset + event.clientY - drag.startY)
    const offset = rawOffset <= drag.height ? rawOffset : drag.height + (rawOffset - drag.height) * .16
    const elapsed = Math.max(event.timeStamp - drag.lastTime, 1)
    const sampleVelocity = (event.clientY - drag.lastY) / elapsed
    drag.velocity = drag.velocity * .35 + sampleVelocity * .65
    drag.lastY = event.clientY
    drag.lastTime = event.timeStamp
    drag.offset = offset
    dialog.style.setProperty('--mn-modal-drag-y', `${offset}px`)
    backdrop.style.opacity = String(1 - Math.min((offset / drag.height) * .58, .52))
  }, [])

  const endDrag = useCallback((event: PointerEvent): void => {
    const drag = dragRef.current
    if (drag === null || drag.pointerId !== event.pointerId) return
    const threshold = Math.min(Math.max(drag.height * .22, 96), 180)
    const shouldClose = drag.offset >= threshold || (drag.offset >= 28 && drag.velocity >= .55)
    if (shouldClose) {
      dragRef.current = null
      releaseDragCapture(drag)
      if (dialogRef.current !== null) delete dialogRef.current.dataset.dragging
      requestClose()
    } else {
      resetDrag()
    }
  }, [requestClose, resetDrag])

  const cancelDrag = useCallback((event: PointerEvent): void => {
    if (dragRef.current?.pointerId === event.pointerId) resetDrag()
  }, [resetDrag])

  useLayoutEffect(() => {
    returnFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const firstControl = dialogRef.current?.querySelector<HTMLElement>('[data-autofocus]:not(:disabled)')
      ?? dialogRef.current?.querySelector<HTMLElement>('input:not(:disabled), textarea:not(:disabled), select:not(:disabled)')
      ?? dialogRef.current?.querySelector<HTMLElement>('button:not(:disabled)')
    firstControl?.focus({ preventScroll: true })
    return () => { if (returnFocusRef.current?.isConnected === true) returnFocusRef.current.focus({ preventScroll: true }) }
  }, [])

  useLayoutEffect(() => {
    if (props.contentReady !== true) return
    const active = document.activeElement
    if (active !== closeButtonRef.current && active !== dialogRef.current) return
    focusPreferredControl()
  }, [focusPreferredControl, props.contentReady])

  useEffect(() => {
    mountedRef.current = true
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      mountedRef.current = false
      document.body.style.overflow = previousOverflow
    }
  }, [])

  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        event.preventDefault()
        requestClose()
        return
      }
      if (event.key !== 'Tab' || closingRef.current) return
      const controls = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), a[href], [tabindex]:not([tabindex="-1"])') ?? []).filter(control => control.getAttribute('aria-hidden') !== 'true')
      const first = controls[0]
      const last = controls.at(-1)
      if (first === undefined || last === undefined) {
        event.preventDefault()
        return
      }
      const active = document.activeElement
      if (event.shiftKey && (active === first || !dialogRef.current?.contains(active))) {
        event.preventDefault(); last.focus()
      } else if (!event.shiftKey && (active === last || !dialogRef.current?.contains(active))) {
        event.preventDefault(); first.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [requestClose])

  useEffect(() => {
    const cancelOnBlur = (): void => { if (dragRef.current !== null) resetDrag() }
    const cancelWhenHidden = (): void => { if (document.visibilityState === 'hidden') cancelOnBlur() }
    window.addEventListener('pointermove', moveDrag, { passive: false })
    window.addEventListener('pointerup', endDrag)
    window.addEventListener('pointercancel', cancelDrag)
    window.addEventListener('blur', cancelOnBlur)
    document.addEventListener('visibilitychange', cancelWhenHidden)
    return () => {
      window.removeEventListener('pointermove', moveDrag)
      window.removeEventListener('pointerup', endDrag)
      window.removeEventListener('pointercancel', cancelDrag)
      window.removeEventListener('blur', cancelOnBlur)
      document.removeEventListener('visibilitychange', cancelWhenHidden)
    }
  }, [cancelDrag, endDrag, moveDrag, resetDrag])

  const interceptCloseControl = (event: ReactMouseEvent<HTMLElement>): void => {
    const target = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-dialog-close]') : null
    if (target === null || !dialogRef.current?.contains(target)) return
    event.preventDefault()
    event.stopPropagation()
    requestClose()
  }

  if (typeof document === 'undefined') return null
  return createPortal(
    <div className={css.modalPortal} data-mnemon-dialog-portal="">
      <div className={appearanceClass(appearanceClass(css.modalTheme, css.shell), sidebarCss.shell)}>
        <div ref={backdropRef} className={appearanceClass(css.modalBackdrop, sidebarCss.modalBackdrop)} onPointerDown={event => { if (event.target === event.currentTarget) requestClose() }}>
          <section ref={dialogRef} className={appearanceClass(appearanceClass(css.modal, sidebarCss.modal), props.wide === true ? appearanceClass(css.modalWide, sidebarCss.modalWide) : undefined)} role="dialog" aria-modal="true" aria-busy={props.contentReady === false || props.busy === true ? true : undefined} aria-labelledby={titleId} aria-describedby={props.description === undefined ? undefined : descriptionId} onClickCapture={interceptCloseControl}>
            <div className={css.modalDragHandle} data-dialog-drag-handle="" aria-hidden="true" onPointerDown={beginDrag} onLostPointerCapture={event => { if (dragRef.current?.pointerId === event.pointerId) resetDrag() }}><span /></div>
            <header><div><h2 id={titleId}>{props.title}</h2>{props.description !== undefined && <p id={descriptionId}>{props.description}</p>}</div><button ref={closeButtonRef} type="button" className={css.iconButton} disabled={props.busy} onClick={requestClose} aria-label={props.closeLabel} title={props.closeLabel}><IconCloseOutlineRegular size={16} /></button></header>
            <div className={css.modalBody}>{props.children}</div>
            {props.footer !== undefined && <footer className={css.modalFooter}>{props.footer}</footer>}
          </section>
        </div>
      </div>
    </div>,
    document.body,
  )
}
