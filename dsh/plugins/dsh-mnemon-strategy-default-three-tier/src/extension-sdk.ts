import { defineMemoryStrategyExtension, memoryViewExtensionValues, validateMemoryViewExtension } from 'dsh-mnemon/extension-sdk'
import { COMPOSABLE_MEMORY_API_VERSION, type MemoryAvailableSource, type MemoryJsonValue, type MemoryStrategyContribution, type MemoryStrategyExtensionDefinition, type MemoryViewExtensionSlot, type MemoryViewExtensionValues, type MemoryViewRequest } from 'dsh-mnemon/contracts'

/** The three-tier slots are Core's standard View extension slots. */
export type ThreeTierExtensionValues = MemoryViewExtensionValues
export type ThreeTierExtensionSlot = MemoryViewExtensionSlot

/** Pure default-product policy. The Host executes it through public Source protocols. */
export function threeTierActionWorkflow(strategyTypeId: string, sourceTypeId: string, actionId: string): 'runtime-capacity' | undefined {
  return strategyTypeId === 'default-three-tier' && sourceTypeId === 'runtime' && actionId === 'mutate'
    ? 'runtime-capacity' : undefined
}

export function validateThreeTierExtension<K extends ThreeTierExtensionSlot>(slot: K, value: MemoryJsonValue): ThreeTierExtensionValues[K] {
  return validateMemoryViewExtension(slot, value)
}

/** An extension only for this Strategy; `defineMemoryViewExtension` also follows other Strategies. */
export function defineThreeTierExtension<K extends ThreeTierExtensionSlot>(definition: {
  typeId: string
  packageName: string
  slot: K
  contribute(request: MemoryViewRequest, sources: readonly MemoryAvailableSource[]): ThreeTierExtensionValues[K]
}): MemoryStrategyExtensionDefinition {
  return defineMemoryStrategyExtension({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy-extension', typeId: definition.typeId,
      packageName: definition.packageName, strategyTypeId: 'default-three-tier', slot: definition.slot, deterministic: true },
    contribute: (request, sources) => validateMemoryViewExtension(definition.slot, definition.contribute(request, sources)),
  })
}

export function threeTierContributions(values: readonly MemoryStrategyContribution[]): Partial<ThreeTierExtensionValues> {
  return memoryViewExtensionValues(values)
}
