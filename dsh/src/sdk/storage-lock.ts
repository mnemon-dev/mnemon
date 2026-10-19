import { resolve } from 'node:path'

const queues = new Map<string, Promise<void>>()

/**
 * Serialize in-process work on one local storage root, including work from
 * independently loaded plugins. File locks still own cross-process safety.
 * Callers must not acquire the same root recursively.
 */
export function withMemoryStorageLock<T>(directory: string, operation: () => T | Promise<T>): Promise<T> {
  const key = resolve(directory)
  const result = (queues.get(key) ?? Promise.resolve()).then(operation)
  const settled = result.then(() => {}, () => {})
  queues.set(key, settled)
  void settled.then(() => { if (queues.get(key) === settled) queues.delete(key) })
  return result
}
