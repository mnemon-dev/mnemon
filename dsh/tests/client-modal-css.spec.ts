import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const viewCss = readFileSync(new URL('../src/client/MnemonView.module.css', import.meta.url), 'utf8')
const sidebarCss = readFileSync(new URL('../src/client/MnemonSidebarView.module.css', import.meta.url), 'utf8')
const saveActionCss = readFileSync(new URL('../src/client/MnemonSaveAction.module.css', import.meta.url), 'utf8')
const dialogSource = readFileSync(new URL('../src/client/MnemonDialog.tsx', import.meta.url), 'utf8')
const viewSource = [
  '../src/client/MnemonWorkbench.tsx',
  '../plugins/dsh-mnemon-source-runtime/src/client/pages.tsx',
  '../plugins/dsh-mnemon-source-documents/src/client/pages.tsx',
  '../plugins/dsh-mnemon-source-memory-spaces/src/client/pages.tsx',
].map(path => readFileSync(new URL(path, import.meta.url), 'utf8')).join('\n')

describe('responsive dialog layout invariants', () => {
  it('keeps the dialog body as the only scrollport and the action footer outside it', () => {
    expect(viewCss).toContain('.modalBody { min-height: 0; overflow-x: hidden; overflow-y: auto; overscroll-behavior: contain;')
    expect(viewCss).toContain('.modalFooter { display: flex; flex: none;')
    expect(sidebarCss).toContain(".shell .modal > [class*='modalBody'] { min-height: 0; overflow-x: hidden; overflow-y: auto;")
  })

  it('applies the fixed sidebar skin to wide dialogs instead of collapsing them to the base width', () => {
    expect(dialogSource).toContain('appearanceClass(css.modalWide, sidebarCss.modalWide)')
  })

  it('uses a safe-area-aware bottom sheet and touch-sized actions on narrow viewports', () => {
    expect(viewCss).toContain('.modalBackdrop { align-items: flex-end; padding: max(10px, env(safe-area-inset-top, 0px)) 0 0; }')
    expect(viewCss).toContain('.modal, .modalWide { width: 100vw;')
    expect(viewCss).toContain('max-height: calc(100dvh - max(10px, env(safe-area-inset-top, 0px)))')
    expect(viewCss).toContain('.modalFooterActions button { min-width: 0; min-height: 44px;')
    expect(viewCss).toContain('.modalBody button { min-height: 44px; }')
    expect(sidebarCss).toContain('.shell .modal, .shell .modal.modalWide { width: 100vw;')
    expect(sidebarCss).toContain(".shell .modal > [class*='modalBody'] button { min-height: 44px; }")
    expect(sidebarCss).toContain(".shell .modal > [class*='modalFooter'] [class*='modalFooterActions'] button { min-height: 44px;")
  })

  it('escapes host stacking contexts through one body-level layer on DSH\'s modal level', () => {
    expect(dialogSource).toContain("import { createPortal } from 'react-dom'")
    expect(dialogSource).toContain('document.body,')
    expect(dialogSource).toContain('data-mnemon-dialog-portal')
    // DSH menus, tooltips and toasts (z 1100) must open above a dialog, as they do over DSH's own Modal (z 1000).
    expect(viewCss).toContain('.modalPortal { position: fixed; z-index: 1000; inset: 0; isolation: isolate; pointer-events: none; }')
    expect(viewCss).toContain('.modalTheme.modalTheme.modalTheme { position: absolute; inset: 0;')
  })

  it('uses compositor motion and a real drag surface for mobile sheets', () => {
    expect(dialogSource).toContain('data-dialog-drag-handle')
    expect(dialogSource).toContain("dialog.style.setProperty('--mn-modal-drag-y'")
    expect(dialogSource).toContain('setPointerCapture?.(event.pointerId)')
    expect(dialogSource).toContain("window.addEventListener('pointermove', moveDrag, { passive: false })")
    expect(dialogSource).toContain('onLostPointerCapture={event =>')
    expect(viewCss).toContain('@keyframes mnemon-sheet-enter')
    expect(viewCss).toContain('transform: translate3d(0, var(--mn-modal-drag-y, 0px), 0);')
    expect(viewCss).toContain('.modalDragHandle { display: grid; width: 100%; height: 28px;')
    expect(viewCss).toContain('.modalBackdrop, .modal { animation: none !important; }')
  })

  it('routes every shared footer cancel action through the exit animation', () => {
    const dialogCallsWithCancel = viewSource.split('\n').filter(line => line.includes('<SidebarModal') && line.includes("t('common.cancel')"))
    expect(dialogCallsWithCancel.length).toBeGreaterThan(10)
    for (const call of dialogCallsWithCancel) expect(call).toContain('data-dialog-close')
  })

  it('keeps every modal control touch-sized on coarse-pointer tablets', () => {
    expect(viewCss).toContain('@media (pointer: coarse)')
    expect(viewCss).toContain('.modalBody button, .modalFooter button { min-height: 44px; }')
    expect(sidebarCss).toContain(".shell .modal > [class*='modalFooter'] button { min-height: 44px; }")
  })

  it('keeps dialog content and actions clear of landscape safe areas', () => {
    expect(viewCss).toContain('padding: 10px max(14px, env(safe-area-inset-right, 0px)) 12px max(14px, env(safe-area-inset-left, 0px));')
    expect(viewCss).toContain('padding: 14px max(14px, env(safe-area-inset-right, 0px)) 18px max(14px, env(safe-area-inset-left, 0px));')
    expect(sidebarCss).toContain(".shell .modal > [class*='modalFooter'] { padding: 10px max(14px, env(safe-area-inset-right, 0px))")
    expect(saveActionCss).toContain('max(12px, env(safe-area-inset-left, 0px))')
    expect(saveActionCss).toContain('max(12px, env(safe-area-inset-right, 0px))')
  })

  it('preserves body space by truncating descriptions in very short viewports', () => {
    expect(viewCss).toContain('@media (max-height: 420px)')
    expect(viewCss).toContain('.modal > header p { display: block; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }')
    expect(sidebarCss).toContain(".shell .modal > header p { display: block; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }")
  })

  it('bounds the conversation save dialog against dynamic and short viewports', () => {
    expect(saveActionCss).toContain('max-height: calc(100dvh - 16px);')
    expect(saveActionCss).toContain('min-height: clamp(140px, 32dvh, 220px);')
    expect(saveActionCss).toContain('min-height: 44px;')
    expect(saveActionCss).toContain('@media (pointer: coarse) and (max-width: 640px), (pointer: coarse) and (max-height: 560px)')
  })
})

describe('entity rail list invariants', () => {
  it('truncates long entity names and keeps the frequency count inside the row', () => {
    const sourceCss = readFileSync(new URL('../plugins/dsh-mnemon-source-memory-spaces/presentation/page.module.css', import.meta.url), 'utf8')
    expect(sourceCss).toContain('.entityList button > span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }')
    expect(sourceCss).toContain('.entityList strong { flex: none;')
  })
})
