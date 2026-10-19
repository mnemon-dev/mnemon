interface MnemonWorkspaceSnapshot {
  open: boolean
}

/** Small framework-neutral state holder shared by the sidebar row and panel. */
export class MnemonWorkspaceController {
  private snapshot: MnemonWorkspaceSnapshot = { open: false }
  private readonly listeners = new Set<() => void>()

  getSnapshot = (): MnemonWorkspaceSnapshot => this.snapshot

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  // Explicit navigation must reassert the panel even if a sibling took over
  // without announcing it and our last snapshot still says open.
  open(): void { this.setOpen(true, true) }
  close(): void { this.setOpen(false) }

  private setOpen(open: boolean, reassert = false): void {
    if (this.snapshot.open === open && !reassert) return
    this.snapshot = { open }
    for (const listener of this.listeners) listener()
  }
}
