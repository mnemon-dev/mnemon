import { MEMORY_SPACE_PROVIDER_API_VERSION, defineMemorySpaceProvider, defineMemorySpaceProviderDefinition } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { SupermemoryProvider } from './driver.ts'
import { descriptor } from './descriptor.ts'

export { SupermemoryProvider, descriptor }

export const definition = defineMemorySpaceProviderDefinition({
  manifest: {
    apiVersion: MEMORY_SPACE_PROVIDER_API_VERSION, kind: 'provider',
    typeId: descriptor.id, packageName: 'dsh-mnemon-provider-supermemory', version: '0.5.5',
    label: descriptor.label, icon: descriptor.icon, summary: descriptor.summary,
    ...(descriptor.summaryI18nKey === undefined ? {} : { summaryI18nKey: descriptor.summaryI18nKey }),
    origin: descriptor.origin, locality: descriptor.kind, workspaceBinding: descriptor.workspaceBinding,
    capabilities: descriptor.capabilities, fields: descriptor.fields,
    secrets: descriptor.fields.filter(field => field.input === 'secret').map(field => field.key),
    scoreSemantics: 'normalized-relevance',
  },
  create: context => new SupermemoryProvider(context.memorySpaces ?? context.memoryBodies, { requestTimeoutMs: context.config.timeoutMs }),
})

export default defineMemorySpaceProvider<undefined>({
  id: descriptor.id,
  apply(ctx, host) { host.install(ctx, definition) },
})
