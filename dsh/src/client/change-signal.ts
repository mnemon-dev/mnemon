/**
 * A revision other surfaces bump when something they cannot observe directly
 * changed, such as a component switched on DSH's Plugins page. Readers reload
 * their own data when the revision moves.
 */
export class MnemonChangeSignal {
  private revision = 0
  private pending: ReturnType<typeof setTimeout> | undefined
  private readonly listeners = new Set<() => void>()

  constructor(private readonly settleMs = 100) {}

  readonly getSnapshot = (): number => this.revision

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  /** A burst of announcements, such as one per component a write switched, moves the revision once. */
  readonly bump = (): void => {
    if (this.pending !== undefined) return
    this.pending = setTimeout(() => {
      this.pending = undefined
      this.revision += 1
      for (const listener of [...this.listeners]) listener()
    }, this.settleMs)
  }
}
