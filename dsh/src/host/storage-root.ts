import { homedir } from 'node:os'
import { join, resolve } from 'node:path'
import type { ResolvedConfig } from './config.ts'
import { withMemoryStorageLock } from '../sdk/storage-lock.ts'
import { workspaceStorageId } from './workspace-storage.ts'

export interface StorageRoot {
  effectiveDataDir(): string
  withExclusive<T>(operation: () => T | Promise<T>): Promise<T>
}

function expandDirectory(value: string): string {
  return resolve(value === '~' ? homedir() : value.startsWith('~/') ? join(homedir(), value.slice(2)) : value)
}

/** Host owns every built-in layout; Sources own the data within the chosen root. */
function storageDirectory(config: Pick<ResolvedConfig, 'storageScope' | 'dataDir'>, workspaceRoot?: string): string {
  const globalRoot = process.env.MNEMON_DATA_DIR?.trim() || '~/.mnemon'
  switch (config.storageScope) {
    case 'global': return expandDirectory(globalRoot)
    case 'custom': return expandDirectory(config.dataDir!)
    case 'workspace': return resolve(workspaceRoot ?? process.cwd(), '.mnemon')
    case 'workspaces': return join(expandDirectory(config.dataDir ?? globalRoot), 'workspaces', workspaceStorageId(resolve(workspaceRoot ?? process.cwd())))
  }
}

export function createStorageRoot(config: Pick<ResolvedConfig, 'storageScope' | 'dataDir'>, workspaceRoot?: string): StorageRoot {
  const directory = storageDirectory(config, workspaceRoot)
  return {
    effectiveDataDir: () => directory,
    withExclusive: operation => withMemoryStorageLock(directory, operation),
  }
}
