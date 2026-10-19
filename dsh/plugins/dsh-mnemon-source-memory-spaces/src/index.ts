import type { Context } from '@deepseek-ai/cordis'
import z from '@deepseek-ai/schemastery'
import { defineMemoryPlugin, installMemory, memoryConfigurationDigest } from 'dsh-mnemon/extension-sdk'
import { createMemorySpacesSource } from './source.ts'
import { PrivateMemorySpaceProviderHost } from './providers/host.ts'
import { defineMemorySpaceProvider, type MemorySpaceProviderEntry, type MemorySpaceProviderModule } from './providers/definitions.ts'
import { createMemorySpaceProviderPlugin } from './providers/plugin.ts'
import { MemorySpacesConfig, resolveMemorySpacesConfig, type MemorySpacesConfig as SourceConfig } from './config.ts'

export { resolveEmbedding, resolvePersistenceStrategy, resolveRecallQuality } from './config.ts'

export const name = 'dsh-mnemon-source-memory-spaces'
export const inject = ['mnemonMemory']

export const memoryPlugin = defineMemoryPlugin({
  packageName: name,
  label: { en: 'Memory Spaces', 'zh-CN': '记忆空间' },
  description: { en: 'Provider-backed durable evidence recalled on demand across tasks and sessions.', 'zh-CN': '由 Provider 支撑的持久证据，按需跨任务与会话召回。' },
  roles: ['source'],
  provides: [{ id: 'source' }, { id: 'source.durable-evidence' }],
})

export interface Config extends SourceConfig {
  /** Explicit Source-private children; resolved by DSH's module Loader. */
  providers: Array<string | MemorySpaceProviderDeclaration>
}

export const MemorySpaceProviderDeclarationSchema = z.object({
  use: z.string().required(),
  instanceId: z.string(),
  config: z.any(),
}) as unknown as z<MemorySpaceProviderDeclaration>

export const Config = z.intersect([MemorySpacesConfig, z.object({
  providers: z.array(z.union([z.string(), MemorySpaceProviderDeclarationSchema] as const)).default([]),
})]) as unknown as z<Config>

/** DSH's cordis-plugin-loader always provides these members. */
interface LoaderLike {
  locate(fiber?: unknown): string | undefined
  import(specifier: string): Promise<unknown>
  unwrapExports(module: unknown): unknown
}

function sourceInstanceId(ctx: Context, explicit?: string): string {
  const configured = explicit?.trim()
  if (configured !== undefined && configured !== '') return configured
  const loader = ctx.get('loader', false) as LoaderLike | undefined
  const located = loader?.locate(ctx.fiber)?.trim()
  if (located !== undefined && located !== '') return located
  throw new Error('Memory Spaces requires a stable Loader Entry id; pass instanceId for a direct mount')
}

export interface InstallMemorySpacesOptions {
  config?: SourceConfig
  instanceId?: string
}

export interface MemorySpaceProviderDeclaration {
  /** Installed package specifier; no built-in implementation registry. */
  use: string
  /** Stable child identity; defaults to the module type id when omitted. */
  instanceId?: string
  /** Validated by the child module's Cordis Config schema, when supplied. */
  config?: unknown
}

function importedProviderModule(loader: LoaderLike, value: unknown, specifier: string): MemorySpaceProviderModule<unknown> {
  const unwrapped = loader.unwrapExports(value)
  if (typeof unwrapped !== 'object' || unwrapped === null) {
    throw new Error(`Memory Space Provider module ${specifier} did not export a typed child module`)
  }
  return defineMemorySpaceProvider(unwrapped as MemorySpaceProviderModule<unknown>)
}

/** Resolve only the explicitly listed children; no dependency scanning occurs. */
export async function resolveMemorySpaceProviderEntries(
  ctx: Context,
  declarations: readonly (string | MemorySpaceProviderDeclaration)[],
): Promise<MemorySpaceProviderEntry[]> {
  if (declarations.length === 0) throw new Error('Memory Spaces requires at least one explicit Provider child')
  const loader = ctx.get('loader', false) as LoaderLike | undefined
  const entries: MemorySpaceProviderEntry[] = []
  const instanceIds = new Set<string>()
  for (const declaration of declarations) {
    const use = (typeof declaration === 'string' ? declaration : declaration.use).trim()
    if (use === '') throw new Error('Memory Space Provider declaration use is required')
    if (loader === undefined) throw new Error('cannot resolve installed Memory Space Provider module without the DSH Loader: ' + use)
    const module = importedProviderModule(loader, await loader.import(use), use)
    const entry: MemorySpaceProviderEntry = {
      instanceId: typeof declaration === 'string' ? module.id : declaration.instanceId?.trim() || module.id,
      module,
      config: typeof declaration === 'string' ? undefined : declaration.config,
    }
    if (instanceIds.has(entry.instanceId)) throw new Error(`duplicate Memory Space Provider child: ${entry.instanceId}`)
    instanceIds.add(entry.instanceId)
    entries.push(entry)
  }
  return entries
}

/** Compose an explicit Provider child list into one effective Source. */
export async function installMemorySpaces(
  ctx: Context,
  entries: readonly MemorySpaceProviderEntry[],
  options: InstallMemorySpacesOptions = {},
): Promise<void> {
  if (entries.length === 0) throw new Error('Memory Spaces requires at least one explicit Provider child')
  const entryId = sourceInstanceId(ctx, options.instanceId)
  resolveMemorySpacesConfig(options.config, entryId)
  const privateHost = new PrivateMemorySpaceProviderHost(entryId)
  const children: Array<ReturnType<typeof ctx.plugin>> = []
  try {
    for (const entry of entries) children.push(ctx.plugin(createMemorySpaceProviderPlugin(entry, privateHost), entry.config))
    await Promise.all(children.map(child => child.await()))
    for (const entry of entries) {
      if (!privateHost.has(entry.instanceId)) throw new Error(`Memory Space Provider child did not install a definition: ${entry.instanceId}`)
    }
    const snapshot = privateHost.snapshot()
    installMemory(ctx, { plugin: memoryPlugin, sources: [createMemorySpacesSource(snapshot, options.config)] }, {
      instanceId: entryId,
      effectiveDigest: 'providers:' + memoryConfigurationDigest({ providers: snapshot.digest, config: options.config ?? {} }).slice('config:'.length),
    })
  } catch (error) {
    await Promise.allSettled(children.reverse().map(child => child.dispose()))
    throw error
  }
}

export async function apply(ctx: Context, config: Config = { providers: [] }): Promise<void> {
  const { providers, ...sourceConfig } = config
  await installMemorySpaces(ctx, await resolveMemorySpaceProviderEntries(ctx, providers), { config: sourceConfig })
}

export type * from './contracts.ts'
