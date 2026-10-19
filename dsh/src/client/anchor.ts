/**
 * Lightweight anchor channel between conversation-scoped Mnemon surfaces
 * (turnTail bar and assistant actions) and the Mnemon workspace view.
 *
 * A dispatch asks the Mnemon view to open a page (optionally with a seed) in
 * the session the dispatch came from. The view may not be mounted when the
 * user clicks — the dispatch is then held per session and consumed the next
 * time that session's Mnemon workspace mounts, so opening Memory System from
 * the sidebar afterwards still lands on the requested page or write dialog.
 */

/** The canonical workspace owns status; every other page belongs to a Source. */
export type MnemonAnchorPage = 'status' | `${string}/${string}`

export interface MnemonAnchor {
  /** Target workspace page. */
  page: MnemonAnchorPage
  /**
   * Optional context seed the target page opens with: a recall query for
   * Memory Spaces explore, candidate text for remember, a document id for
   * Project Documents, an entry's text for runtime memory.
   */
  seed?: string
  /** Session the dispatch belongs to; omitted dispatches address every session. */
  sessionId?: string
}

export const MNEMON_ANCHOR_EVENT = 'mnemon:anchor'

const pendingBySession = new Map<string, MnemonAnchor>()

function keyOf(sessionId?: string): string {
  return sessionId === undefined || sessionId === '' ? '*' : sessionId
}

/** Ask the Mnemon view to open a page; held until a matching view consumes it. */
export function dispatchMnemonAnchor(anchor: MnemonAnchor): void {
  pendingBySession.set(keyOf(anchor.sessionId), anchor)
  window.dispatchEvent(new CustomEvent<MnemonAnchor>(MNEMON_ANCHOR_EVENT, { detail: anchor }))
}

/** Take the anchor held for this session (usually at mount time), or null. */
export function consumeMnemonAnchor(sessionId?: string): MnemonAnchor | null {
  const key = keyOf(sessionId)
  const anchor = pendingBySession.get(key)
  if (anchor === undefined) return null
  pendingBySession.delete(key)
  return anchor
}

/** Subscribe to anchors addressed to this session; returns an unsubscribe. */
export function subscribeMnemonAnchor(sessionId: string | undefined, onAnchor: (anchor: MnemonAnchor) => void): () => void {
  const key = keyOf(sessionId)
  const handler = (event: Event): void => {
    const anchor = (event as CustomEvent<MnemonAnchor>).detail
    if (anchor !== undefined && keyOf(anchor.sessionId) === key) {
      // A mounted view consumes the event immediately. Clear only this exact
      // dispatch so a newer anchor cannot be lost if listeners navigate again.
      if (pendingBySession.get(key) === anchor) pendingBySession.delete(key)
      onAnchor(anchor)
    }
  }
  window.addEventListener(MNEMON_ANCHOR_EVENT, handler)
  return () => window.removeEventListener(MNEMON_ANCHOR_EVENT, handler)
}
