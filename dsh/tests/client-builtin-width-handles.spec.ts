// @vitest-environment jsdom
import { readFileSync } from 'node:fs'
import { URL as NodeURL } from 'node:url'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

const viewCss = readFileSync(new NodeURL('../src/client/MnemonView.module.css', import.meta.url), 'utf8')

/**
 * JSDOM 26 parses this stylesheet but rejects multi-combinator :has selectors.
 * Resolve each actual rule's relative query against the DOM, then let JSDOM
 * apply that same rule's declarations and remaining selector. This checks
 * ownership and restoration, not paint order or pointer hit testing; those
 * still require the published-package browser regression.
 */
function installStyles(): { refresh(): void; dispose(): void } {
  const native = document.createElement('style')
  native.textContent = `
    [data-width-handle] { position: absolute; z-index: 8; }
    [data-composer-seat] { position: sticky; bottom: 0; z-index: 7; }
  `
  const authored = document.createElement('style')
  authored.textContent = viewCss
  const relational = document.createElement('style')
  document.head.append(native, authored, relational)
  const rules = Array.from(authored.sheet?.cssRules ?? [])
    .filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule && rule.selectorText.includes(':has('))
  const markers = new Set<string>()

  return {
    refresh() {
      for (const marker of markers) {
        for (const element of document.querySelectorAll(`[${marker}]`)) element.removeAttribute(marker)
      }
      markers.clear()
      const elements = Array.from(document.querySelectorAll('*'))
      relational.textContent = rules.map((rule, ruleIndex) => {
        let queryIndex = 0
        const selector = rule.selectorText.replace(/:has\(([^()]*)\)/gu, (_match, relative: string) => {
          const marker = `data-width-handle-test-${ruleIndex}-${queryIndex++}`
          markers.add(marker)
          for (const element of elements) {
            if (element.querySelector(`:scope ${relative}`) !== null) element.setAttribute(marker, '')
          }
          return `[${marker}]`
        })
        return `${selector} { ${rule.style.cssText} }`
      }).join('\n')
    },
    dispose() {
      native.remove()
      authored.remove()
      relational.remove()
    },
  }
}

/** Native ConversationRoot/ConversationContent ancestry in the published DSH 0.1.7 WebUI. */
function mountConversation(parent: HTMLElement = document.body) {
  const body = document.createElement('div')
  body.innerHTML = `
    <div data-conversation-scroll>
      <div data-slot="conversation.session" style="display: contents">
        <div>
          <div data-slot="conversation.view" style="display: contents"></div>
        </div>
      </div>
      <div data-composer-seat><button type="button">Stop</button></div>
    </div>
    <div data-width-handle="left"></div>
    <div data-width-handle="right"></div>
  `
  parent.append(body)
  const view = body.querySelector<HTMLElement>('[data-slot="conversation.view"]')!
  const composer = body.querySelector<HTMLElement>('[data-composer-seat]')!
  const handles = Array.from(body.querySelectorAll<HTMLElement>(':scope > [data-width-handle]'))
  expect(handles).toHaveLength(2)
  return { body, view, composer, handles }
}

function mountView(outlet: HTMLElement, kind: 'builtin' | 'sidebar' | 'peer' | 'foreign') {
  const main = document.createElement('main')
  if (kind === 'builtin' || kind === 'sidebar') {
    main.className = 'shell'
    main.dataset.mnemonSurface = kind
  } else if (kind === 'foreign') {
    // The placement attribute alone must not grant ownership to another plugin.
    main.className = 'peer-shell'
    main.dataset.mnemonSurface = 'builtin'
  }
  main.innerHTML = '<button type="button">Source action</button>'
  outlet.replaceChildren(main)
  return main
}

function interactionStyle(element: HTMLElement) {
  const style = getComputedStyle(element)
  return { visibility: style.visibility, pointerEvents: style.pointerEvents, display: style.display, zIndex: style.zIndex }
}

function expectHandles(handles: HTMLElement[], hidden: boolean): void {
  for (const handle of handles) {
    expect(interactionStyle(handle)).toMatchObject({
      visibility: hidden ? 'hidden' : 'visible',
      pointerEvents: hidden ? 'none' : 'auto',
    })
  }
}

describe('Builtin conversation width-handle ownership', () => {
  let styles: ReturnType<typeof installStyles>
  beforeEach(() => { styles = installStyles() })
  afterEach(() => {
    styles.dispose()
    document.body.replaceChildren()
  })

  it('suppresses both owning handles without changing the resident composer or Source controls', () => {
    const conversation = mountConversation()
    const composerBefore = interactionStyle(conversation.composer)
    const composerMarkup = conversation.composer.outerHTML
    const main = mountView(conversation.view, 'builtin')
    const sourceControl = document.createElement('div')
    sourceControl.dataset.widthHandle = 'source-owned'
    main.append(sourceControl)
    styles.refresh()

    expectHandles(conversation.handles, true)
    expectHandles([sourceControl], false)
    expect(interactionStyle(conversation.composer)).toEqual(composerBefore)
    expect(conversation.composer.outerHTML).toBe(composerMarkup)
    const stop = conversation.composer.querySelector('button')!
    expect(interactionStyle(stop).visibility).toBe('visible')
    stop.focus()
    expect(document.activeElement).toBe(stop)
    expect(interactionStyle(main.querySelector('button')!).pointerEvents).toBe('auto')
  })

  it.each(['peer', 'sidebar', 'foreign'] as const)('retains handles for a mounted %s view', kind => {
    const conversation = mountConversation()
    mountView(conversation.view, kind)
    styles.refresh()
    expectHandles(conversation.handles, false)
  })

  it('isolates sibling and nested conversation occurrences in both directions', () => {
    const owner = mountConversation()
    const ownerMain = mountView(owner.view, 'builtin')
    const nestedPeer = mountConversation(ownerMain)
    mountView(nestedPeer.view, 'peer')
    const sibling = mountConversation()
    mountView(sibling.view, 'peer')
    const outerPeer = mountConversation()
    const outerMain = mountView(outerPeer.view, 'peer')
    const nestedBuiltin = mountConversation(outerMain)
    mountView(nestedBuiltin.view, 'builtin')
    styles.refresh()

    expectHandles(owner.handles, true)
    expectHandles(nestedBuiltin.handles, true)
    for (const peer of [nestedPeer, sibling, outerPeer]) expectHandles(peer.handles, false)
  })

  it('restores the native handles when Builtin unmounts, then reapplies on remount', () => {
    const conversation = mountConversation()
    const before = conversation.handles.map(interactionStyle)
    const main = mountView(conversation.view, 'builtin')
    styles.refresh()
    expectHandles(conversation.handles, true)

    main.remove()
    styles.refresh()
    expect(conversation.handles.map(interactionStyle)).toEqual(before)
    mountView(conversation.view, 'builtin')
    styles.refresh()
    expectHandles(conversation.handles, true)

    mountView(conversation.view, 'peer')
    styles.refresh()
    expect(conversation.handles.map(interactionStyle)).toEqual(before)
  })
})
