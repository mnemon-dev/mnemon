import { vi } from 'vitest'
import type { ClientSettingsScope, ClientSettingsSnapshot } from '../../src/host/protocol.ts'

/**
 * Settings scope fake over one published snapshot. Reads go through the
 * receiver like DSH's own scopes, so an unbound read fails the test; writes
 * reach `mutate`, which callers pass in when they assert on it.
 */
export function settingsScope<T>(snapshot: ClientSettingsSnapshot<T>, mutate: ClientSettingsScope<T>['mutate'] = vi.fn(async () => {})) {
  return {
    snapshot,
    getSnapshot() { return this.snapshot },
    subscribe: () => () => {},
    mutate,
  } satisfies ClientSettingsScope<T> & { snapshot: ClientSettingsSnapshot<T> }
}

/**
 * Settings scope fake that publishes each write as the next snapshot, as DSH's
 * scopes do once the Host saves it, so a control that applies at once shows
 * what it saved. `mutate` sees every write first and can refuse it.
 */
export function liveSettingsScope<T extends object>(snapshot: ClientSettingsSnapshot<T>, mutate: ClientSettingsScope<T>['mutate'] = vi.fn(async () => {})) {
  const listeners = new Set<() => void>()
  const scope = {
    snapshot,
    getSnapshot() { return this.snapshot },
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } },
    async mutate(operations: Parameters<ClientSettingsScope<T>['mutate']>[0]) {
      await mutate(operations)
      const value = structuredClone(scope.snapshot.value ?? {}) as Record<string, unknown>
      for (const operation of operations) {
        let target = value
        for (const segment of operation.path.slice(0, -1)) target = (target[segment] ??= {}) as Record<string, unknown>
        const last = operation.path.at(-1)!
        if (operation.op === 'unset') delete target[last]
        else target[last] = structuredClone(operation.value)
      }
      scope.snapshot = { ...scope.snapshot, value: value as T, revision: (scope.snapshot.revision ?? 0) + 1 }
      for (const listener of listeners) listener()
    },
  } satisfies ClientSettingsScope<T> & { snapshot: ClientSettingsSnapshot<T> }
  return scope
}
