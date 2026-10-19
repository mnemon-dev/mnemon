interface MnemonNativeSidebarPlacement {
  available: boolean
  target?: HTMLElement
}

/** Keep the shell-owned Source tree alive across native main-panel navigation. */
export class MnemonNativeSidebarSeat {
  private snapshot: MnemonNativeSidebarPlacement = { available: false }
  private readonly listeners = new Set<() => void>()

  getSnapshot = (): MnemonNativeSidebarPlacement => this.snapshot

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  setAvailable(available: boolean): void {
    if (this.snapshot.available === available) return
    this.snapshot = { ...this.snapshot, available }
    this.emit()
  }

  attach(parent: HTMLElement): () => void {
    let target = this.snapshot.target
    if (target === undefined) {
      target = document.createElement('div')
      target.style.display = 'contents'
      this.snapshot = { ...this.snapshot, target }
      this.emit()
    }
    parent.append(target)
    return () => {
      if (target.parentElement === parent) target.remove()
    }
  }

  private emit(): void {
    for (const listener of this.listeners) listener()
  }
}
