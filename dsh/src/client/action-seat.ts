/**
 * One action a surface offers while its owner can perform it, such as opening
 * the dsh-mnemon page under DSH Plugins while that page provides navigation.
 * Readers render the control only while an action is present.
 */
export class MnemonActionSeat {
  private action: (() => void) | undefined
  private readonly listeners = new Set<() => void>()

  readonly getSnapshot = (): (() => void) | undefined => this.action

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  /** Offer the action until the returned disposer runs; a newer offer replaces it. */
  provide(action: () => void): () => void {
    this.action = action
    this.emit()
    return () => {
      if (this.action !== action) return
      this.action = undefined
      this.emit()
    }
  }

  private emit(): void {
    for (const listener of [...this.listeners]) listener()
  }
}
