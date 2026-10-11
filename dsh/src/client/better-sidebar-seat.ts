export interface MnemonWorkspaceScope {
  sessionId: string
  cwd?: string
}

interface MnemonBetterSidebarPlacement {
  target: HTMLElement
  scope: MnemonWorkspaceScope
  visible: boolean
}

/**
 * Carries only Better Sidebar's DOM seat and scope into the DSH-owned renderer
 * tree. Source child Slots never cross this boundary: the shell entry renders
 * them itself and portals the resulting workspace into the supplied seat.
 */
export class MnemonBetterSidebarSeat {
  private placement: MnemonBetterSidebarPlacement | undefined
  private owner: symbol | undefined
  private readonly listeners = new Set<() => void>()

  getSnapshot = (): MnemonBetterSidebarPlacement | undefined => this.placement

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  attach(target: HTMLElement, scope: MnemonWorkspaceScope, visible: boolean): () => void {
    const owner = Symbol('better-sidebar-seat')
    this.owner = owner
    this.placement = { target, scope, visible }
    this.emit()
    return () => {
      if (this.owner !== owner) return
      this.owner = undefined
      this.placement = undefined
      this.emit()
    }
  }

  private emit(): void {
    for (const listener of this.listeners) listener()
  }
}
