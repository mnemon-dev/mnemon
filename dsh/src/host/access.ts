import type { MemoryCapability } from '../core/contracts/index.ts'
import type { ResolvedConfig } from './config.ts'
import type { MemoryLayerParticipation, MemoryParticipationChannel } from './protocol.ts'

export function participationChannel(capability: MemoryCapability): MemoryParticipationChannel {
  if (capability === 'project') return 'projection'
  if (['write', 'archive', 'link', 'forget', 'import'].includes(capability)) return 'write'
  if (['maintain', 'export', 'status'].includes(capability)) return 'maintenance'
  return 'recall'
}

export function allowsParticipation(config: ResolvedConfig, sourceTypeId: string, capability: MemoryCapability, trigger: 'manual' | 'automatic'): boolean {
  const source = config.memoryTopology.layers[sourceTypeId]
  if (source === undefined) return true
  return source.enabled && source.participation[participationChannel(capability)] !== 'off'
    && (trigger === 'manual' || source.participation[participationChannel(capability)] === 'automatic')
}

export function assertParticipation(config: ResolvedConfig, sourceTypeId: string, capability: MemoryCapability, trigger: 'manual' | 'automatic'): void {
  if (!allowsParticipation(config, sourceTypeId, capability, trigger)) {
    throw new Error(`Memory Source ${sourceTypeId} does not allow ${trigger} ${participationChannel(capability)} in the current configuration`)
  }
}

export const DEFAULT_PARTICIPATION: MemoryLayerParticipation = { recall: 'automatic', write: 'automatic', projection: 'automatic', maintenance: 'automatic' }
