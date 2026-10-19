import { homedir } from 'node:os'
import { isAbsolute, join } from 'node:path'
import z from '@deepseek-ai/schemastery'

export interface Config { dataDir?: string; limitBytes?: number }

export const Config: z<Config> = z.object({ dataDir: z.string(), limitBytes: z.number().step(1).min(1).max(1024 * 1024 * 1024) })


export function documentsSourceConfig(value: Config, instanceKey: string): Required<Config> {
  const config = Config(value)
  const dataDir = config.dataDir ?? join(homedir(), '.mnemon', 'sources', encodeURIComponent(instanceKey))
  if (!isAbsolute(dataDir)) throw new Error('Documents Source dataDir must be absolute')
  return { dataDir, limitBytes: config.limitBytes ?? 10 * 1024 * 1024 }
}
