import type { MnemonTranslate } from './locales.ts'
import css from './MnemonWorkspace.module.css'
import type { MnemonWorkspaceController } from './workspace-controller.ts'
import { createMemoryIcon } from './memory-icon.tsx'

const FAMILY_SELECTOR = '[data-dsh-taskboard-entry], [data-dsh-ssh-entry], [data-dsh-mnemon-entry]'

function sidebarRoot(): HTMLElement | undefined {
  const column = document.querySelector<HTMLElement>('[data-pane="sidebar"], [class*="sidebarCol"], .dshDesktopUpstreamSidebar')
  if (column === null) return undefined
  return column.querySelector<HTMLElement>('[class*="logoRow"]')?.parentElement
    ?? (column.firstElementChild as HTMLElement | undefined)
}

function newSessionButton(root: HTMLElement): HTMLButtonElement | undefined {
  const nested = root.querySelector<HTMLButtonElement>('button[class*="newSession"]')
  if (nested !== null) return nested
  for (const child of root.children) {
    if (child.tagName === 'BUTTON') return child as HTMLButtonElement
  }
  return undefined
}

function createEntry(controller: MnemonWorkspaceController): { entry: HTMLButtonElement; label: HTMLSpanElement } {
  const entry = document.createElement('button')
  entry.type = 'button'
  entry.dataset.dshMnemonEntry = ''
  entry.dataset.dshPlugin = 'dsh-mnemon'
  entry.dataset.dshPart = 'sidebar-entry'
  entry.className = css.entry ?? ''
  const icon = document.createElement('span')
  icon.className = css.entryIcon ?? ''
  icon.append(createMemoryIcon(16))
  const label = document.createElement('span')
  label.className = css.entryLabel ?? ''
  entry.append(icon, label)
  entry.addEventListener('click', () => { controller.open() })
  return { entry, label }
}

function placeEntry(root: HTMLElement, entry: HTMLButtonElement): boolean {
  const button = newSessionButton(root)
  if (button === undefined) return false
  const row = button.closest('[class*="logoRow"]')
  const base = row !== null && row.parentElement === root ? row : button
  const parent = base.parentElement ?? root
  if (entry.parentElement === parent) return true
  const family = Array.from(parent.children).filter(
    (element): element is HTMLElement => element instanceof HTMLElement && element.matches(FAMILY_SELECTOR),
  )
  const anchor = family.at(-1)?.nextElementSibling ?? base.nextElementSibling
  parent.insertBefore(entry, anchor !== null && anchor.parentElement === parent ? anchor : null)
  return true
}

/** Mount a self-healing official-style entry under the New Session row. */
export function mountMnemonSidebarEntry(
  controller: MnemonWorkspaceController,
  t: MnemonTranslate,
  subscribeLocale: (listener: () => void) => () => void,
): () => void {
  const { entry, label } = createEntry(controller)
  let root: HTMLElement | undefined
  let placed = false

  const syncLabel = (): void => {
    const text = t('tab.label')
    if (entry.getAttribute('aria-label') !== text) entry.setAttribute('aria-label', text)
    if (entry.title !== text) entry.title = text
    if (label.textContent !== text) label.textContent = text
  }

  const rootObserver = new MutationObserver(() => {
    if (root === undefined || !root.isConnected) {
      placed = false
      tryPlace()
      return
    }
    if (!root.contains(entry)) placed = placeEntry(root, entry)
  })

  const tryPlace = (): void => {
    syncLabel()
    if (root !== undefined && !root.isConnected) {
      rootObserver.disconnect()
      root = undefined
      placed = false
    }
    if (placed && document.body.contains(entry)) return
    if (placed) {
      rootObserver.disconnect()
      root = undefined
      placed = false
    }
    root ??= sidebarRoot()
    if (root === undefined) return
    placed = placeEntry(root, entry)
    if (placed) rootObserver.observe(root, { childList: true, subtree: true })
  }

  const waitObserver = new MutationObserver(tryPlace)
  waitObserver.observe(document.body, { childList: true, subtree: true })

  // Like DSH's own panel row, the open workspace is also the current page.
  const syncActive = (): void => {
    if (controller.getSnapshot().open) {
      entry.dataset.active = 'true'
      entry.setAttribute('aria-current', 'page')
    } else {
      delete entry.dataset.active
      entry.removeAttribute('aria-current')
    }
  }
  const unsubscribe = controller.subscribe(syncActive)
  const unsubscribeLocale = subscribeLocale(syncLabel)
  const dispose = (): void => {
    waitObserver.disconnect()
    rootObserver.disconnect()
    unsubscribe()
    unsubscribeLocale()
    entry.remove()
  }
  try {
    syncActive()
    tryPlace()
    return dispose
  } catch (error) {
    dispose()
    throw error
  }
}
