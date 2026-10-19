import {
  MNEMON_SETTINGS_CHANNEL,
  MNEMON_SETTINGS_NAMESPACE,
  type ClientConnectionHandle,
  type ClientSettingsScope,
  type ClientSettingsSnapshot,
  type SettingsOperation,
} from "../host/protocol.ts"
import { callMnemonRpc } from './remote-rpc.ts'

export class MnemonSettingsScope<T extends object> implements ClientSettingsScope<T> {
  private snapshot: ClientSettingsSnapshot<T> = { status: 'loading', writable: false, mode: 'host' }
  private readonly listeners = new Set<() => void>()
  private tail = Promise.resolve()

  constructor(
    private readonly connection: ClientConnectionHandle,
    private readonly namespace = MNEMON_SETTINGS_NAMESPACE,
    private readonly requestTimeoutMs = 12_000,
  ) {
    void this.load()
  }

  getSnapshot = (): ClientSettingsSnapshot<T> => this.snapshot

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  /** Queue one revision-fenced Host write of these operations. */
  mutate(ops: SettingsOperation[]): Promise<void> {
    return this.write(ops)
  }

  /**
   * Re-read the Host snapshot after a change made elsewhere. Queued behind
   * writes so an older read never replaces a newer answer; a failed read
   * keeps the current snapshot.
   */
  refresh(): Promise<void> {
    const task = this.tail.then(async () => {
      try {
        const response = await this.call('get', { namespace: this.namespace })
        if (response.ok) this.publish(response.value as ClientSettingsSnapshot<T>)
      } catch {
        // Keep the snapshot the page already shows.
      }
    })
    this.tail = task
    return task
  }

  private async load(): Promise<void> {
    try {
      const response = await this.call('get', { namespace: this.namespace })
      if (!response.ok) {
        this.publish({ status: 'unavailable', writable: false, mode: 'host' })
        return
      }
      this.publish(response.value as ClientSettingsSnapshot<T>)
    } catch {
      this.publish({ status: 'unavailable', writable: false, mode: 'host' })
    }
  }

  private write(ops: SettingsOperation[]): Promise<void> {
    const task = this.tail.then(async () => {
      const response = await this.call('mutate', {
        namespace: this.namespace,
        ops,
        ...(this.snapshot.revision === undefined ? {} : { expectedRevision: this.snapshot.revision }),
      })
      if (!response.ok) {
        await this.load()
        throw new Error(response.error.message)
      }
      this.publish(response.value as ClientSettingsSnapshot<T>)
    })
    this.tail = task.catch(() => {})
    return task
  }

  private async call(endpoint: 'get' | 'mutate', payload: unknown) {
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), Math.max(1, this.requestTimeoutMs))
    try {
      return await callMnemonRpc(this.connection, MNEMON_SETTINGS_CHANNEL, endpoint, payload, controller.signal)
    } catch (error) {
      if (controller.signal.aborted) throw new Error('Mnemon settings request timed out')
      throw error
    } finally {
      clearTimeout(timeout)
    }
  }

  private publish(snapshot: ClientSettingsSnapshot<T>): void {
    this.snapshot = snapshot
    for (const listener of this.listeners) {
      try {
        listener()
      } catch (error) {
        console.warn('dsh-mnemon: settings listener failed; keeping the published Host snapshot', error)
      }
    }
  }
}
