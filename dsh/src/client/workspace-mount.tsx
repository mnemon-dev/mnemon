import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from 'react'
import { createPortal } from 'react-dom'
import type { PropsRenderSlots } from '@deepseek-ai/dsh-client-ui-slots'
import type { ClientSettingsScope, Config } from "../host/protocol.ts"
import type { MnemonClientContext } from "./dsh-context.ts"
import type { MnemonTranslate } from './locales.ts'
import { MnemonWorkbench, type MnemonWorkspaceSelection } from './MnemonWorkbench.tsx'
import type { MNEMON_SOURCE_PAGE_SLOT, MemorySourcePageDirectory } from './source-pages.tsx'
import type { MNEMON_COMPONENT_STATUS_SLOT } from './component-ui.tsx'
import type { MnemonBetterSidebarSeat } from './better-sidebar-seat.ts'
import type { MnemonNativeSidebarSeat } from './native-sidebar-seat.ts'
import type { MnemonActionSeat } from './action-seat.ts'
import type { MnemonChangeSignal } from './change-signal.ts'
import { mountMnemonSidebarEntry } from './sidebar-entry.ts'
import { MnemonWorkspaceController } from './workspace-controller.ts'
import { useMnemonSessionId, type MnemonSessionBinding } from './session-binding.ts'
import css from './MnemonWorkspace.module.css'

const ACTIVE_ATTR = 'data-dsh-mnemon-active'
const TASKBOARD_ACTIVE_ATTR = 'data-dsh-taskboard-active'
const SSH_ACTIVE_ATTR = 'data-dsh-ssh-active'
const ACTIVATE_EVENT = 'dsh-panel-activate'
const SIDEBAR_CONTEXT_SELECTOR = '[data-dsh-taskboard-entry], [data-dsh-ssh-entry], [class*="sessionRow"], [class*="projectRow"], [class*="searchResultRow"], [class*="searchResultWorkspace"], [class*="newSession"]'

/** Conversation column that the Sidebar workspace overlay covers. */
function resolveWorkspaceColumn(): HTMLElement | undefined {
  return document.querySelector<HTMLElement>('[data-pane="conversation"]')
    ?? document.querySelector<HTMLElement>('.dshDesktopConversationSurface')
    ?? document.querySelector<HTMLElement>('[class*="centerCol"]')
    ?? undefined
}

function normalizePath(value: string): string {
  return value.replace(/[\\/]+$/u, '')
}

interface MnemonWorkspaceNavigation {
  open(): void
  close(): void
}

interface MnemonWorkspaceHostProps {
  connection: MnemonClientContext['connection']
  settingsScope: ClientSettingsScope<Config>
  sessions: MnemonClientContext['sessions']
  workspaces: MnemonClientContext['workspaces']
  currentSession: MnemonSessionBinding
  localeRuntime: MnemonClientContext['locale']
  sourcePageDirectory: MemorySourcePageDirectory
  navigation?: MnemonWorkspaceNavigation
  /** Opens the dsh-mnemon page under DSH Plugins while that page offers navigation. */
  configuration?: MnemonActionSeat
  /** Moves when a component is switched through DSH's plugin manager. */
  componentChanges?: MnemonChangeSignal
  t: MnemonTranslate
  sessionId?: string | undefined
  cwd?: string
  active?: boolean
  renderSlot?: PropsRenderSlots<typeof MNEMON_SOURCE_PAGE_SLOT | typeof MNEMON_COMPONENT_STATUS_SLOT>['renderSlot']
}

interface MnemonBuiltinWorkspaceHostProps extends Pick<MnemonWorkspaceHostProps,
  'connection' | 'settingsScope' | 'localeRuntime' | 'sourcePageDirectory' | 'configuration' | 'componentChanges' | 'renderSlot' | 't'> {
  sessionId: string
}

/** Builtin follows its DSH slot session, never the Sidebar's workspace picker. */
export function MnemonBuiltinWorkspaceHost(props: MnemonBuiltinWorkspaceHostProps): JSX.Element {
  const subscribeLocale = useCallback((listener: () => void) => props.localeRuntime.subscribe(listener), [props.localeRuntime])
  const getLocale = useCallback(() => props.localeRuntime.getSnapshot(), [props.localeRuntime])
  const locale = useSyncExternalStore(subscribeLocale, getLocale, getLocale)
  return <MnemonWorkbench
    connection={props.connection}
    settingsScope={props.settingsScope}
    sessionId={props.sessionId}
    surface="builtin"
    t={props.t}
    locale={locale.active}
    sourcePageDirectory={props.sourcePageDirectory}
    {...(props.configuration === undefined ? {} : { configuration: props.configuration })}
    {...(props.componentChanges === undefined ? {} : { componentChanges: props.componentChanges })}
    {...(props.renderSlot === undefined ? {} : { renderSlot: props.renderSlot })}
  />
}

/** Shared workspace body; its DSH registration owns Source child-render authority. */
export function MnemonWorkspaceHost(props: MnemonWorkspaceHostProps): JSX.Element {
  const subscribeLocale = useCallback((listener: () => void) => props.localeRuntime.subscribe(listener), [props.localeRuntime])
  const getLocale = useCallback(() => props.localeRuntime.getSnapshot(), [props.localeRuntime])
  const subscribeSessions = useCallback((listener: () => void) => props.sessions.list.subscribe(listener), [props.sessions.list])
  const getSessions = useCallback(() => props.sessions.list.getSnapshot(), [props.sessions.list])
  const subscribeWorkspaces = useCallback((listener: () => void) => props.workspaces.list.subscribe(listener), [props.workspaces.list])
  const getWorkspaces = useCallback(() => props.workspaces.list.getSnapshot(), [props.workspaces.list])
  const locale = useSyncExternalStore(subscribeLocale, getLocale, getLocale)
  const sessions = useSyncExternalStore(subscribeSessions, getSessions, getSessions)
  const workspaces = useSyncExternalStore(subscribeWorkspaces, getWorkspaces, getWorkspaces)
  const mainSessionId = useMnemonSessionId(props.currentSession)
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<string>()
  const sessionId = Object.hasOwn(props, 'sessionId') ? props.sessionId : mainSessionId
  const currentCwd = props.cwd ?? (sessionId === undefined ? undefined : Object.entries(sessions.byId).find(([id]) => id === sessionId)?.[1]?.cwd)
  const effectiveWorkspace = currentCwd === undefined
    ? undefined
    : workspaces.items.find(workspace => normalizePath(workspace.path) === normalizePath(currentCwd))
  const fallbackWorkspace = effectiveWorkspace ?? workspaces.items[0]
  const selectedExists = selectedWorkspaceId !== undefined && workspaces.items.some(workspace => String(workspace.workspaceId) === selectedWorkspaceId)
  const resolvedSelectedId = selectedExists ? selectedWorkspaceId : fallbackWorkspace === undefined ? undefined : String(fallbackWorkspace.workspaceId)

  useEffect(() => {
    if (resolvedSelectedId !== selectedWorkspaceId) setSelectedWorkspaceId(resolvedSelectedId)
  }, [resolvedSelectedId, selectedWorkspaceId])

  const selection = useMemo<MnemonWorkspaceSelection>(() => ({
    options: workspaces.items.map(workspace => ({ id: String(workspace.workspaceId), title: workspace.title, path: workspace.path })),
    ...(resolvedSelectedId === undefined ? {} : { selectedWorkspaceId: resolvedSelectedId }),
    ...(effectiveWorkspace === undefined ? {} : { effectiveWorkspaceId: String(effectiveWorkspace.workspaceId) }),
    onSelect: setSelectedWorkspaceId,
    onAlign: () => {
      if (effectiveWorkspace !== undefined) setSelectedWorkspaceId(String(effectiveWorkspace.workspaceId))
    },
  }), [effectiveWorkspace, resolvedSelectedId, workspaces.items])

  return <MnemonWorkbench
    connection={props.connection}
    settingsScope={props.settingsScope}
    {...(sessionId === undefined ? {} : { sessionId })}
    {...(resolvedSelectedId === undefined ? {} : { workspaceId: resolvedSelectedId })}
    workspaceSelection={selection}
    active={props.active ?? true}
    t={props.t}
    locale={locale.active}
    sourcePageDirectory={props.sourcePageDirectory}
    {...(props.configuration === undefined ? {} : { configuration: props.configuration })}
    {...(props.componentChanges === undefined ? {} : { componentChanges: props.componentChanges })}
    {...(props.renderSlot === undefined ? {} : { renderSlot: props.renderSlot })}
    {...(props.navigation === undefined ? {} : { onClose: props.navigation.close })}
  />
}

/** Sidebar presentation in DSH's additive shell.overlay, also without a session. */
export function MnemonSidebarWorkspaceHost(props: MnemonWorkspaceHostProps & { controller: MnemonWorkspaceController; betterSidebarSeat?: MnemonBetterSidebarSeat; nativeSidebarSeat?: MnemonNativeSidebarSeat }): JSX.Element {
  const state = useSyncExternalStore(props.controller.subscribe, props.controller.getSnapshot, props.controller.getSnapshot)
  const subscribeBetterSidebar = useCallback((listener: () => void) => props.betterSidebarSeat?.subscribe(listener) ?? (() => {}), [props.betterSidebarSeat])
  const getBetterSidebar = useCallback(() => props.betterSidebarSeat?.getSnapshot(), [props.betterSidebarSeat])
  const betterSidebar = useSyncExternalStore(subscribeBetterSidebar, getBetterSidebar, getBetterSidebar)
  const subscribeNativeSidebar = useCallback((listener: () => void) => props.nativeSidebarSeat?.subscribe(listener) ?? (() => {}), [props.nativeSidebarSeat])
  const getNativeSidebar = useCallback(() => props.nativeSidebarSeat?.getSnapshot(), [props.nativeSidebarSeat])
  const nativeSidebar = useSyncExternalStore(subscribeNativeSidebar, getNativeSidebar, getNativeSidebar)
  const [bounds, setBounds] = useState<{ left: number; top: number; width: number; height: number }>()
  useEffect(() => {
    if (!state.open || nativeSidebar?.available) return
    let column: HTMLElement | undefined
    let previousInert = false
    const update = (): void => {
      if (column === undefined) return
      const { left, top, width, height } = column.getBoundingClientRect()
      setBounds(previous => previous?.left === left && previous.top === top && previous.width === width && previous.height === height ? previous : { left, top, width, height })
    }
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(update)
    const detach = (): void => {
      if (column !== undefined) {
        observer?.unobserve(column)
        column.inert = previousInert
      }
      column = undefined
    }
    const connect = (): void => {
      const next = resolveWorkspaceColumn()
      if (next !== column) {
        detach()
        column = next
        if (column !== undefined) {
          previousInert = column.inert
          column.inert = true
          observer?.observe(column)
        }
      }
      update()
    }
    connect()
    const shell = new MutationObserver(connect)
    shell.observe(document.body, { childList: true, subtree: true })
    window.addEventListener('resize', update)
    return () => {
      shell.disconnect()
      observer?.disconnect()
      window.removeEventListener('resize', update)
      detach()
    }
  }, [state.open, props.controller, nativeSidebar?.available])
  useEffect(() => {
    if (!state.open) return
    const escape = (event: KeyboardEvent): void => {
      if (event.key === 'Escape' && !event.defaultPrevented && document.querySelector('[role="dialog"]') === null) {
        if (props.navigation !== undefined) props.navigation.close()
        else props.controller.close()
      }
    }
    window.addEventListener('keydown', escape)
    return () => { window.removeEventListener('keydown', escape) }
  }, [state.open, props.controller, props.navigation])
  const betterSidebarView = betterSidebar === undefined ? null : createPortal(<MnemonWorkspaceHost
    connection={props.connection}
    settingsScope={props.settingsScope}
    sessions={props.sessions}
    workspaces={props.workspaces}
    currentSession={props.currentSession}
    localeRuntime={props.localeRuntime}
    sourcePageDirectory={props.sourcePageDirectory}
    {...(props.configuration === undefined ? {} : { configuration: props.configuration })}
    {...(props.componentChanges === undefined ? {} : { componentChanges: props.componentChanges })}
    sessionId={betterSidebar.scope.sessionId}
    {...(betterSidebar.scope.cwd === undefined ? {} : { cwd: betterSidebar.scope.cwd })}
    active={betterSidebar.visible}
    t={props.t}
    {...(props.renderSlot === undefined ? {} : { renderSlot: props.renderSlot })}
  />, betterSidebar.target)
  // Hide the existing DSH subtree so panel navigation retains Source page state.
  return <>
    {betterSidebarView}
    {nativeSidebar?.target !== undefined && nativeSidebar.available && createPortal(<section data-dsh-mnemon-view className={css.nativeWorkspace} hidden={!state.open} aria-label={props.t('tab.label')}>
      <MnemonWorkspaceHost {...props} active={state.open} />
    </section>, nativeSidebar.target)}
    {!nativeSidebar?.available && bounds !== undefined && <section data-dsh-mnemon-view hidden={!state.open} className={css.workspacePanel} style={{ ...bounds, display: state.open ? undefined : 'none' }} aria-label={props.t('tab.label')}>
      <MnemonWorkspaceHost {...props} active={state.open} />
    </section>}
  </>
}

/** Core-owned sidebar row; presentation state is shared with its DSH seat. */
export function mountMnemonSidebarLauncher(
  ctx: MnemonClientContext,
  t: MnemonTranslate,
  controller: MnemonWorkspaceController,
): () => void {
  if (typeof document === 'undefined' || typeof window === 'undefined') return () => {}
  const stopEntry = mountMnemonSidebarEntry(controller, t, listener => ctx.locale.subscribe(listener))
  const stopPanels = coordinateSidebarPanels(controller)
  return () => { stopPanels(); stopEntry() }
}

/** Coordinate released peer panels; their DOM flags also cover lost events. */
export function coordinateSidebarPanels(controller: MnemonWorkspaceController, close: () => void = () => controller.close()): () => void {
  let announcing = false
  const applyActive = (): void => {
    const html = document.documentElement
    if (!controller.getSnapshot().open) { html.removeAttribute(ACTIVE_ATTR); return }
    // Released Taskboard and SSH only close for each other's event names.
    announcing = true
    try {
      document.dispatchEvent(new CustomEvent(ACTIVATE_EVENT, { detail: 'ssh' }))
      document.dispatchEvent(new CustomEvent(ACTIVATE_EVENT, { detail: 'taskboard' }))
    } finally { announcing = false }
    html.removeAttribute(TASKBOARD_ACTIVE_ATTR)
    html.removeAttribute(SSH_ACTIVE_ATTR)
    html.setAttribute(ACTIVE_ATTR, '')
    document.dispatchEvent(new CustomEvent(ACTIVATE_EVENT, { detail: 'mnemon' }))
  }
  const onActivate = (event: Event): void => {
    if (announcing || !controller.getSnapshot().open) return
    const detail = (event as CustomEvent<unknown>).detail
    if (detail === 'taskboard' || detail === 'ssh') close()
  }
  const onContext = (event: MouseEvent): void => {
    if (controller.getSnapshot().open && event.target instanceof Element && event.target.closest(SIDEBAR_CONTEXT_SELECTOR) !== null) close()
  }
  const observer = new MutationObserver(() => {
    if (!controller.getSnapshot().open) return
    const html = document.documentElement
    if (!html.hasAttribute(ACTIVE_ATTR) || html.hasAttribute(TASKBOARD_ACTIVE_ATTR) || html.hasAttribute(SSH_ACTIVE_ATTR)) close()
  })
  observer.observe(document.documentElement, { attributes: true, attributeFilter: [ACTIVE_ATTR, TASKBOARD_ACTIVE_ATTR, SSH_ACTIVE_ATTR] })
  document.addEventListener('click', onContext, true)
  document.addEventListener(ACTIVATE_EVENT, onActivate)
  const unsubscribe = controller.subscribe(applyActive)
  applyActive()
  return () => {
    unsubscribe()
    observer.disconnect()
    document.removeEventListener('click', onContext, true)
    document.removeEventListener(ACTIVATE_EVENT, onActivate)
    document.documentElement.removeAttribute(ACTIVE_ATTR)
  }
}
