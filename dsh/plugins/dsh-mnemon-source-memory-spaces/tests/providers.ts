import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import * as p0 from 'dsh-mnemon-provider-mnemon-native'
import * as p1 from 'dsh-mnemon-provider-openviking'
import * as p2 from 'dsh-mnemon-provider-honcho'
import * as p3 from 'dsh-mnemon-provider-mem0'
import * as p4 from 'dsh-mnemon-provider-hindsight'
import * as p5 from 'dsh-mnemon-provider-holographic'
import * as p6 from 'dsh-mnemon-provider-retaindb'
import * as p7 from 'dsh-mnemon-provider-byterover'
import * as p8 from 'dsh-mnemon-provider-supermemory'
import { MemoryProviderCatalog } from '../src/providers/catalog.ts'
import { MemoryProviderAdapterRegistry } from '../src/providers/registry.ts'
import { MemorySpaceRegistry } from '../src/memory-spaces.ts'
import { MemorySpacesService } from '../src/service.ts'
import { NORMALIZED_RELEVANCE_SCORE } from '../src/providers/adapter.ts'

/** Explicit integration-test assembly; production has no built-in Provider map. */
export const modules = [p0, p1, p2, p3, p4, p5, p6, p7, p8]
export const descriptors = modules.map(module => module.descriptor)
export const providerEntries = modules.map(module => ({ instanceId: module.descriptor.id, module: module.default, config: undefined }))
export const catalog = new MemoryProviderCatalog(descriptors)
export function adapterRegistry() {
  return new MemoryProviderAdapterRegistry(modules.map(({ definition }) => ({
    id: definition.manifest.typeId,
    scoreSemantics: definition.manifest.scoreSemantics === 'normalized-relevance' ? NORMALIZED_RELEVANCE_SCORE : { kind: 'provider-native' as const },
    create: context => definition.create({ ...context, providerInstanceId: definition.manifest.typeId, manifest: definition.manifest }),
  })))
}
export function createRegistry(
  runner: ConstructorParameters<typeof MemorySpaceRegistry>[0],
  enabled = true,
  now?: ConstructorParameters<typeof MemorySpaceRegistry>[2],
  providers = catalog,
) { return new MemorySpaceRegistry(runner, enabled, now, providers) }
export function createService(
  runner: ConstructorParameters<typeof MemorySpacesService>[0],
  config: ConstructorParameters<typeof MemorySpacesService>[1],
  bodies?: MemorySpaceRegistry,
  quality?: ConstructorParameters<typeof MemorySpacesService>[3],
  adapters = adapterRegistry(),
  providers = catalog,
) { return new MemorySpacesService(runner, config, bodies ?? createRegistry(runner), quality, adapters, providers) }

/** An executable stub so the runner sees an installed Mnemon CLI; tests fake the process it would start. */
export function installedCliStub(): string {
  const path = join(mkdtempSync(join(tmpdir(), 'mnemon-cli-stub-')), process.platform === 'win32' ? 'mnemon.exe' : 'mnemon')
  writeFileSync(path, '#!/bin/sh\nexit 0\n', { mode: 0o755 })
  return path
}
