import { useLayoutEffect, useRef } from 'react'
import type { PropsRuntime } from '@deepseek-ai/dsh-client-ui-slots'
import type { MainPanelId } from '@deepseek-ai/dsh-client-ui-layout/client'
import type { MnemonClientContext } from './dsh-context.ts'
import type { MnemonTranslate } from './locales.ts'
import type { MnemonNativeSidebarSeat } from './native-sidebar-seat.ts'
import type { MnemonWorkspaceController } from './workspace-controller.ts'
import { mountMnemonSidebarEntry } from './sidebar-entry.ts'
import { coordinateSidebarPanels } from './workspace-mount.tsx'
import { MemoryIcon } from './memory-icon.tsx'
import css from './MnemonWorkspace.module.css'

export const MNEMON_MAIN_PANEL_ID = 'mnemon' as MainPanelId
const ICON_SELECTOR = '[data-dsh-plugin="dsh-mnemon"][data-dsh-part="sidebar-icon"]'

/** The native Sidebar owns the surrounding button, label, tooltip and state. */
function MnemonSidebarIcon({ size }: Pick<PropsRuntime<'sidebar.panellist'>, 'size'>): JSX.Element {
  return <MemoryIcon size={size} data-dsh-plugin="dsh-mnemon" data-dsh-part="sidebar-icon" />
}

/** DSH selects this seat; the shell registration retains Source render authority. */
function MnemonMainPanel({ seat, controller }: { seat: MnemonNativeSidebarSeat; controller: MnemonWorkspaceController }): JSX.Element {
  const parent = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    if (parent.current === null) return
    const detach = seat.attach(parent.current)
    controller.open()
    return () => { controller.close(); detach() }
  }, [seat, controller])
  return <div ref={parent} className={css.nativeWorkspace} data-dsh-mnemon-main-seat />
}

/** Use the published panel contract, with a fallback for replacement layouts. */
export function mountMnemonSidebarNavigation(
  ctx: MnemonClientContext,
  t: MnemonTranslate,
  controller: MnemonWorkspaceController,
  seat: MnemonNativeSidebarSeat,
): { open(): void; close(): void; dispose(): void } {
  let stopFallback: (() => void) | undefined
  let disposed = false
  const navigation = {
    open(): void {
      if (seat.getSnapshot().available) ctx.layout.selectPanel(MNEMON_MAIN_PANEL_ID)
      controller.open()
    },
    close(): void {
      if (seat.getSnapshot().available && controller.getSnapshot().open) ctx.layout.selectPanel(null)
      controller.close()
    },
  }
  const reconcileFallback = (): void => {
    if (disposed || seat.getSnapshot().available) {
      stopFallback?.()
      stopFallback = undefined
    } else {
      stopFallback ??= mountMnemonSidebarEntry(controller, t, listener => ctx.locale.subscribe(listener))
    }
  }
  const stopPanels = coordinateSidebarPanels(controller, navigation.close)
  let stopNative: (() => void) | undefined
  // Legacy overlay peers can take over before their asynchronous DOM flag is
  // observed. A repeated native navigation must still dismiss those overlays.
  const reassert = (event: MouseEvent): void => {
    if (!seat.getSnapshot().available || !(event.target instanceof Element)) return
    const button = event.target.closest('button')
    if (button?.querySelector(ICON_SELECTOR) != null) controller.open()
  }
  const dispose = (): void => {
    if (disposed) return
    disposed = true
    document.removeEventListener('click', reassert, true)
    stopPanels()
    stopNative?.()
    stopFallback?.()
    controller.close()
  }
  try {
    // Replacement shells may not declare the native panel seats; the fallback entry covers them.
    stopNative = ctx.slots.inject('main', () => ctx.slots.inject('sidebar.panellist', () => {
      const stopMain = ctx.slots.register({
        name: 'main', key: MNEMON_MAIN_PANEL_ID, inject: () => ({ seat, controller }),
      }, MnemonMainPanel)
      let stopIcon: (() => void) | undefined
      try {
        stopIcon = ctx.slots.register({
          name: 'sidebar.panellist', id: MNEMON_MAIN_PANEL_ID, order: 30,
          label: () => t('tab.label'), locale: 'mnemon',
        }, MnemonSidebarIcon)
        seat.setAvailable(true)
        reconcileFallback()
        if (controller.getSnapshot().open) navigation.open()
      } catch (error) {
        seat.setAvailable(false)
        stopIcon?.()
        stopMain()
        throw error
      }
      return () => {
        controller.close()
        seat.setAvailable(false)
        stopIcon?.()
        stopMain()
        reconcileFallback()
      }
    }))
    document.addEventListener('click', reassert, true)
    reconcileFallback()
    return { ...navigation, dispose }
  } catch (error) {
    dispose()
    throw error
  }
}
