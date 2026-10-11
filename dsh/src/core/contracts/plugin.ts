/** Declarative identity and graph relations shared by every Mnemon plugin. */
export const MEMORY_PLUGIN_API_VERSION = 'dsh-mnemon/plugin/v1' as const

export type MemoryPluginRole = 'source' | 'strategy' | 'strategy-extension'

export interface MemoryPluginLocalizedText {
  en: string
  'zh-CN': string
}

/**
 * A capability is a graph edge, not a runtime permission. Multiple plugins may
 * provide the same capability unless the capability is explicitly exclusive.
 */
export interface MemoryPluginProvidedCapability {
  id: string
  exclusive?: true
}

/**
 * JSON-safe module export named `memoryPlugin`.
 *
 * Source and Strategy are contribution roles of one peer plugin model. The
 * descriptor is available before activation, so a Host can explain and verify
 * dependencies without executing the plugin. Core remains the sole View
 * compiler; a plugin never publishes a View directly.
 */
export interface MemoryPluginDescriptor {
  apiVersion: typeof MEMORY_PLUGIN_API_VERSION
  packageName: string
  label: MemoryPluginLocalizedText
  description: MemoryPluginLocalizedText
  roles: MemoryPluginRole[]
  provides: MemoryPluginProvidedCapability[]
  requires?: string[]
}
