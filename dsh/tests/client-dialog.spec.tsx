// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MnemonDialog } from '../src/client/MnemonDialog.tsx'

const animateDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'animate')
const getAnimationsDescriptor = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'getAnimations')
const matchMediaDescriptor = Object.getOwnPropertyDescriptor(window, 'matchMedia')

interface AnimationCall {
  element: HTMLElement
  keyframes: Keyframe[] | PropertyIndexedKeyframes | null
  finish: () => void
  cancel: ReturnType<typeof vi.fn>
}

function restoreProperty(target: object, key: PropertyKey, descriptor: PropertyDescriptor | undefined): void {
  if (descriptor === undefined) Reflect.deleteProperty(target, key)
  else Object.defineProperty(target, key, descriptor)
}

function setMedia(options: { mobile?: boolean; reduced?: boolean } = {}): void {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: vi.fn((query: string) => ({
      matches: query.includes('max-width') ? options.mobile === true : query.includes('prefers-reduced-motion') && options.reduced === true,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(() => true),
    } satisfies MediaQueryList)),
  })
}

function installAnimationMock(): AnimationCall[] {
  const calls: AnimationCall[] = []
  const animations = new WeakMap<HTMLElement, Animation[]>()
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value(this: HTMLElement): Animation[] {
      return animations.get(this) ?? []
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'animate', {
    configurable: true,
    value(this: HTMLElement, keyframes: Keyframe[] | PropertyIndexedKeyframes | null): Animation {
      let finish = (): void => {}
      const finished = new Promise<void>(resolve => { finish = resolve })
      const cancel = vi.fn()
      const animation = { finished, cancel } as unknown as Animation
      animations.set(this, [...(animations.get(this) ?? []), animation])
      calls.push({ element: this, keyframes, finish, cancel })
      return animation
    },
  })
  return calls
}

function pointer(target: Element, type: 'pointerdown' | 'pointermove' | 'pointerup' | 'pointercancel', clientY: number, pointerId = 7): void {
  const event = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperties(event, {
    button: { value: 0 },
    clientY: { value: clientY },
    isPrimary: { value: true },
    pointerId: { value: pointerId },
    pointerType: { value: 'touch' },
  })
  fireEvent(target, event)
}

function renderDialog(options: { busy?: boolean; onClose?: () => void; cancelClick?: () => void } = {}) {
  const onClose = options.onClose ?? vi.fn()
  const cancelClick = options.cancelClick ?? vi.fn()
  const result = render(
    <MnemonDialog
      title="测试弹窗"
      closeLabel="关闭"
      {...(options.busy === undefined ? {} : { busy: options.busy })}
      onClose={onClose}
      footer={<button type="button" data-dialog-close onClick={cancelClick}>取消</button>}
    >
      <label>名称<input aria-label="名称" /></label>
    </MnemonDialog>,
  )
  return { ...result, onClose, cancelClick }
}

afterEach(() => {
  cleanup()
  restoreProperty(HTMLElement.prototype, 'animate', animateDescriptor)
  restoreProperty(HTMLElement.prototype, 'getAnimations', getAnimationsDescriptor)
  restoreProperty(window, 'matchMedia', matchMediaDescriptor)
  document.body.style.overflow = ''
  vi.restoreAllMocks()
})

describe('MnemonDialog', () => {
  it('portals to document.body, locks background scrolling, and restores both on unmount', () => {
    const host = document.createElement('div')
    document.body.append(host)
    const { unmount } = renderDialog()
    const portal = document.querySelector<HTMLElement>('[data-mnemon-dialog-portal]')

    expect(portal?.parentElement).toBe(document.body)
    expect(host.contains(portal)).toBe(false)
    expect(document.body.style.overflow).toBe('hidden')

    unmount()
    expect(document.querySelector('[data-mnemon-dialog-portal]')).toBeNull()
    expect(document.body.style.overflow).toBe('')
    host.remove()
  })

  it('waits for the smooth downward exit before invoking a footer cancel', async () => {
    setMedia({ mobile: true })
    const calls = installAnimationMock()
    const { onClose, cancelClick } = renderDialog()

    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    const dialog = screen.getByRole('dialog', { name: '测试弹窗' })
    expect(onClose).not.toHaveBeenCalled()
    expect(cancelClick).not.toHaveBeenCalled()
    expect(dialog.dataset.closing).toBe('true')
    expect(calls).toHaveLength(2)
    expect(JSON.stringify(calls[0]?.keyframes)).toContain('calc(100% + 32px)')

    await act(async () => { calls.forEach(call => call.finish()); await Promise.resolve() })
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  })

  it('tracks a mobile drag and closes after crossing the distance threshold', async () => {
    setMedia({ mobile: true })
    const calls = installAnimationMock()
    const { onClose } = renderDialog()
    const dialog = screen.getByRole('dialog', { name: '测试弹窗' })
    vi.spyOn(dialog, 'getBoundingClientRect').mockReturnValue({ x: 0, y: 100, top: 100, right: 390, bottom: 600, left: 0, width: 390, height: 500, toJSON: () => ({}) })
    const handle = dialog.querySelector('[data-dialog-drag-handle]')
    if (handle === null) throw new Error('drag handle missing')

    pointer(handle, 'pointerdown', 100)
    pointer(handle, 'pointermove', 260)
    expect(dialog.style.getPropertyValue('--mn-modal-drag-y')).toBe('160px')

    pointer(handle, 'pointerup', 260)
    expect(dialog.dataset.closing).toBe('true')
    expect(onClose).not.toHaveBeenCalled()
    expect(calls).toHaveLength(2)

    await act(async () => { calls.forEach(call => call.finish()); await Promise.resolve() })
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  })

  it('springs a short drag back without closing', async () => {
    setMedia({ mobile: true })
    const calls = installAnimationMock()
    const { onClose } = renderDialog()
    const dialog = screen.getByRole('dialog', { name: '测试弹窗' })
    vi.spyOn(dialog, 'getBoundingClientRect').mockReturnValue({ x: 0, y: 100, top: 100, right: 390, bottom: 600, left: 0, width: 390, height: 500, toJSON: () => ({}) })
    const handle = dialog.querySelector('[data-dialog-drag-handle]')
    if (handle === null) throw new Error('drag handle missing')

    pointer(handle, 'pointerdown', 100)
    pointer(handle, 'pointermove', 112)
    pointer(handle, 'pointerup', 112)
    expect(onClose).not.toHaveBeenCalled()
    expect(calls).toHaveLength(2)

    await act(async () => { calls.forEach(call => call.finish()); await Promise.resolve() })
    await waitFor(() => expect(dialog.style.getPropertyValue('--mn-modal-drag-y')).toBe(''))
    expect(onClose).not.toHaveBeenCalled()
  })

  it('recovers an active drag when the window loses focus', async () => {
    setMedia({ mobile: true })
    const calls = installAnimationMock()
    const { onClose } = renderDialog()
    const dialog = screen.getByRole('dialog', { name: '测试弹窗' })
    vi.spyOn(dialog, 'getBoundingClientRect').mockReturnValue({ x: 0, y: 100, top: 100, right: 390, bottom: 600, left: 0, width: 390, height: 500, toJSON: () => ({}) })
    const handle = dialog.querySelector('[data-dialog-drag-handle]')
    if (handle === null) throw new Error('drag handle missing')

    pointer(handle, 'pointerdown', 100)
    pointer(handle, 'pointermove', 170)
    expect(dialog.dataset.dragging).toBe('true')
    fireEvent.blur(window)
    expect(dialog.dataset.dragging).toBeUndefined()
    expect(calls).toHaveLength(2)

    await act(async () => { calls.forEach(call => call.finish()); await Promise.resolve() })
    await waitFor(() => expect(dialog.style.getPropertyValue('--mn-modal-drag-y')).toBe(''))
    expect(onClose).not.toHaveBeenCalled()
  })

  it('does not let stale snap-back cleanup interrupt a quick re-grab', async () => {
    setMedia({ mobile: true })
    const calls = installAnimationMock()
    renderDialog()
    const dialog = screen.getByRole('dialog', { name: '测试弹窗' })
    vi.spyOn(dialog, 'getBoundingClientRect').mockReturnValue({ x: 0, y: 100, top: 100, right: 390, bottom: 600, left: 0, width: 390, height: 500, toJSON: () => ({}) })
    const handle = dialog.querySelector('[data-dialog-drag-handle]')
    if (handle === null) throw new Error('drag handle missing')

    pointer(handle, 'pointerdown', 100)
    pointer(handle, 'pointermove', 112)
    pointer(handle, 'pointerup', 112)
    expect(calls).toHaveLength(2)

    pointer(handle, 'pointerdown', 100, 8)
    pointer(handle, 'pointermove', 150, 8)
    const activeDragOffset = dialog.style.getPropertyValue('--mn-modal-drag-y')
    expect(dialog.dataset.dragging).toBe('true')
    expect(activeDragOffset).not.toBe('')

    await act(async () => { calls.slice(0, 2).forEach(call => call.finish()); await Promise.resolve() })
    expect(dialog.dataset.dragging).toBe('true')
    expect(dialog.style.getPropertyValue('--mn-modal-drag-y')).toBe(activeDragOffset)

    pointer(handle, 'pointercancel', 150, 8)
    expect(calls).toHaveLength(4)
    await act(async () => { calls.slice(2).forEach(call => call.finish()); await Promise.resolve() })
    await waitFor(() => expect(dialog.style.getPropertyValue('--mn-modal-drag-y')).toBe(''))
  })

  it('disables dismissal gestures while a destructive action is busy', () => {
    setMedia({ mobile: true })
    const calls = installAnimationMock()
    const { onClose } = renderDialog({ busy: true })
    const dialog = screen.getByRole('dialog', { name: '测试弹窗' })
    const handle = dialog.querySelector('[data-dialog-drag-handle]')
    const backdrop = dialog.parentElement
    if (handle === null || backdrop === null) throw new Error('dialog surface missing')

    pointer(handle, 'pointerdown', 100)
    pointer(handle, 'pointermove', 240)
    fireEvent.pointerDown(backdrop)
    fireEvent.keyDown(window, { key: 'Escape' })

    expect(dialog.style.getPropertyValue('--mn-modal-drag-y')).toBe('')
    expect(onClose).not.toHaveBeenCalled()
    expect(calls).toHaveLength(0)
  })

  it('closes immediately when reduced motion is requested', () => {
    setMedia({ mobile: true, reduced: true })
    const calls = installAnimationMock()
    const { onClose } = renderDialog()

    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(calls).toHaveLength(0)
  })
})
