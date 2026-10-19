import { homedir } from 'node:os'
import { isAbsolute, join } from 'node:path'
import z from '@deepseek-ai/schemastery'

import { DEFAULT_RUNTIME_MEMORY_LIMIT_BYTES, DEFAULT_RUNTIME_USER_LIMIT_BYTES, MAX_RUNTIME_MEMORY_LIMIT_BYTES } from './defaults.ts'

export interface Config {
  dataDir?: string
  userDataDir?: string
  memoryLimitBytes?: number
  userLimitBytes?: number
}

export const Config: z<Config> = z.object({
  dataDir: z.string(), userDataDir: z.string(),
  memoryLimitBytes: z.number().step(1).min(1).max(MAX_RUNTIME_MEMORY_LIMIT_BYTES),
  userLimitBytes: z.number().step(1).min(1).max(MAX_RUNTIME_MEMORY_LIMIT_BYTES),
})

export interface RuntimeMemoryStorage { effectiveDataDir(): string }


export function runtimeSourceConfig(value: Config, instanceKey: string): Required<Config> {
  const config = Config(value)
  const dataDir = config.dataDir ?? join(homedir(), '.mnemon', 'sources', encodeURIComponent(instanceKey))
  const userDataDir = config.userDataDir ?? dataDir
  if (!isAbsolute(dataDir) || !isAbsolute(userDataDir)) throw new Error('Runtime Source dataDir and userDataDir must be absolute')
  return {
    dataDir, userDataDir,
    memoryLimitBytes: config.memoryLimitBytes ?? DEFAULT_RUNTIME_MEMORY_LIMIT_BYTES,
    userLimitBytes: config.userLimitBytes ?? DEFAULT_RUNTIME_USER_LIMIT_BYTES,
  }
}
